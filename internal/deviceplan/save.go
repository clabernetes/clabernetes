package deviceplan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	clabnodes "github.com/srl-labs/containerlab/nodes"
	clabruntime "github.com/srl-labs/containerlab/runtime"
)

// RunSave reconstructs one imported Node from immutable input and invokes its package-owned
// SaveConfig hook inside the target application container. The supplied runtime can address only
// the already-running planned container and cannot launch a nested device.
func (a Adapter) RunSave(
	ctx context.Context,
	input Input,
	plan Plan,
	containerID,
	artifactRoot string,
	runtime clabruntime.ContainerRuntime,
) error {
	if ctx == nil {
		return fmt.Errorf("save context is nil")
	}
	if runtime == nil {
		return fmt.Errorf("save runtime is nil")
	}
	normalizedInput, err := NormalizeInput(input)
	if err != nil {
		return err
	}
	normalizedPlan, err := NormalizePlan(plan)
	if err != nil {
		return err
	}
	if err = ValidatePlanInputIdentity(normalizedInput, normalizedPlan); err != nil {
		return err
	}
	finishEntropy, err := a.beginEntropy(normalizedInput)
	if err != nil {
		return err
	}
	defer finishEntropy()
	if strings.TrimSpace(a.Revision) == "" || a.Revision != normalizedPlan.Planner.Revision {
		return fmt.Errorf("save worker revision differs from the accepted plan")
	}
	_, targetNode, targetInput, nodeIndex, err := importedSaveTarget(
		normalizedInput,
		normalizedPlan,
		containerID,
	)
	if err != nil {
		return err
	}
	root, err := scopedDirectory(artifactRoot, "save artifact root")
	if err != nil {
		return err
	}
	targetLabDir := filepath.Join(root, ArtifactNodeDirectory(targetNode.ID))
	if err = requireRealDirectory(targetLabDir, "save Node artifact root"); err != nil {
		return err
	}

	registry := a.Registry
	if registry == nil {
		registry = NewContainerlabRegistry()
		if err = validateLiveCompatibility(registry, normalizedInput.Compatibility); err != nil {
			return err
		}
	}
	entry := registry.Kind(targetInput.Kind)
	if entry == nil {
		return fmt.Errorf("save kind is absent from the imported registry")
	}
	definition, err := decodeNodeDefinition(targetInput)
	if err != nil {
		return err
	}
	config, err := nodeConfigFromDefinition(targetInput, definition, entry)
	if err != nil {
		return err
	}
	config.Index = nodeIndex
	config.LabDir = targetLabDir
	if err = materializeEmbeddedStartupConfig(targetInput.ID, config); err != nil {
		return err
	}
	management := managementForNode(normalizedInput.Management, targetInput.ID)
	applyManagementInput(config, management)
	implementation, err := registry.NewNodeOfKind(targetInput.Kind)
	if err != nil {
		return fmt.Errorf("constructing imported save Node: %w", err)
	}
	if err = runImportedRuntimeHook(
		targetInput.ID,
		"save.initialization",
		"imported-save-init",
		"containerlab save initialization panicked",
		"initializing imported save hook",
		runtime,
		func() error {
			return implementation.Init(
				config,
				clabnodes.WithRuntime(runtime),
				clabnodes.WithMgmtNet(runtimeManagement(management)),
			)
		},
	); err != nil {
		return err
	}
	if _, err = evaluateInterfaces(
		implementation,
		interfacesForNode(normalizedInput.Interfaces, targetInput.ID),
		targetInput.ID,
	); err != nil {
		return err
	}
	if err = importedRuntimeBoundaryFailure(runtime, targetInput.ID); err != nil {
		return err
	}
	var result *clabnodes.SaveConfigResult
	if err = runImportedRuntimeHook(
		targetInput.ID,
		"save",
		"imported-save",
		"containerlab save hook panicked",
		"running imported save hook",
		runtime,
		func() error {
			var hookErr error
			result, hookErr = implementation.SaveConfig(ctx)

			return hookErr
		},
	); err != nil {
		return err
	}
	if result == nil || result.ConfigPath == "" {
		return nil
	}

	return validateImportedSavePath(targetLabDir, result.ConfigPath)
}

func importedSaveTarget(
	input Input,
	plan Plan,
	containerID string,
) (ContainerPlan, NodePlan, NodeInput, int, error) {
	var container ContainerPlan
	for _, candidate := range plan.Containers {
		if candidate.ID == containerID {
			container = candidate

			break
		}
	}
	if container.ID == "" {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("save target container is absent from the plan")
	}
	var node NodePlan
	for _, candidate := range plan.Nodes {
		if candidate.ID == container.NodeID {
			node = candidate

			break
		}
	}
	if node.ID == "" || len(node.ContainerIDs) == 0 || node.ContainerIDs[0] != containerID {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("save target is not the primary container of a planned logical Node")
	}
	actionCount := 0
	for _, action := range plan.Actions {
		if action.Phase != PhaseSave || action.Kind != ActionSave ||
			action.Target.ContainerID != containerID {
			continue
		}
		if action.Target.NodeID != node.ID || action.Save == nil ||
			action.Save.Method != SaveMethodImported {
			return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
				fmt.Errorf("imported save action crosses logical Node ownership")
		}
		actionCount++
	}
	if actionCount != 1 {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("save target requires exactly one imported save action")
	}
	for index, candidate := range input.Nodes {
		if candidate.ID == node.ID {
			return container, node, candidate, index, nil
		}
	}

	return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
		fmt.Errorf("save target Node is absent from the normalized input")
}

func validateImportedSavePath(root, savedPath string) error {
	cleaned := filepath.Clean(savedPath)
	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("imported save result path is not absolute")
	}
	relative, err := filepath.Rel(root, cleaned)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("imported save result escapes the plan-owned artifact root")
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return fmt.Errorf("inspecting imported save result: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("imported save result is not a regular file")
	}

	return nil
}
