package deviceplan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	clabnodes "github.com/srl-labs/containerlab/nodes"
)

// CheckReadiness invokes the imported Node's IsHealthy hook from the target application
// container. The adapter reconstructs the Node only from the immutable normalized Input; it does
// not know or branch on kind identity.
func (a Adapter) CheckReadiness(
	ctx context.Context,
	input Input,
	plan Plan,
	containerID,
	scratchRoot string,
) error {
	if ctx == nil {
		return fmt.Errorf("readiness context is nil")
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
		return fmt.Errorf("readiness worker revision differs from the accepted plan")
	}

	container, node, nodeInput, nodeIndex, err := readinessTarget(
		normalizedInput,
		normalizedPlan,
		containerID,
	)
	if err != nil {
		return err
	}
	root := filepath.Clean(scratchRoot)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return fmt.Errorf("readiness scratch root must be a scoped absolute path")
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("creating readiness scratch root: %w", err)
	}
	workspace, err := os.MkdirTemp(root, "check-")
	if err != nil {
		return fmt.Errorf("creating readiness workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	registry := a.Registry
	if registry == nil {
		registry = NewContainerlabRegistry()
		if err = validateLiveCompatibility(registry, normalizedInput.Compatibility); err != nil {
			return err
		}
	}
	entry := registry.Kind(nodeInput.Kind)
	if entry == nil {
		return fmt.Errorf("readiness kind is absent from the imported registry")
	}
	definition, err := decodeNodeDefinition(nodeInput)
	if err != nil {
		return err
	}
	config, err := nodeConfigFromDefinition(nodeInput, definition, entry)
	if err != nil {
		return err
	}
	config.Index = nodeIndex
	config.LabDir = workspace
	management := managementForNode(normalizedInput.Management, nodeInput.ID)
	applyManagementInput(config, management)
	runtime := newRecordingRuntime(normalizedInput.Images, management, workspace)
	implementation, err := registry.NewNodeOfKind(nodeInput.Kind)
	if err != nil {
		return fmt.Errorf("constructing imported readiness Node: %w", err)
	}
	if err = invokeImported(
		nodeInput.ID,
		"readiness.initialization",
		"imported-readiness-init",
		"containerlab readiness initialization panicked",
		func() error {
			return implementation.Init(
				config,
				clabnodes.WithRuntime(runtime),
				clabnodes.WithMgmtNet(runtime.Mgmt()),
			)
		},
	); err != nil {
		return fmt.Errorf("initializing imported readiness hook: %w", err)
	}
	if _, err = evaluateInterfaces(
		implementation,
		interfacesForNode(normalizedInput.Interfaces, nodeInput.ID),
		nodeInput.ID,
	); err != nil {
		return err
	}
	if err = runtime.Failure(); err != nil {
		return withNodeID(err, nodeInput.ID)
	}
	runtime.BeginReadinessObservation()
	healthy := false
	healthErr := invokeImported(
		nodeInput.ID,
		"readiness",
		"imported-readiness",
		"containerlab readiness hook panicked",
		func() error {
			var hookErr error
			healthy, hookErr = implementation.IsHealthy(ctx)

			return hookErr
		},
	)
	if err = runtime.Failure(); err != nil {
		return withNodeID(err, nodeInput.ID)
	}
	// Containerlab callers treat a true health result as authoritative even when a hook also
	// returns advisory detail (for example, a component for which the check has no effect).
	if healthy {
		return nil
	}
	if healthErr != nil {
		return fmt.Errorf("imported readiness hook is not healthy: %w", healthErr)
	}

	return fmt.Errorf(
		"imported readiness hook is not healthy for Node %q container %q",
		node.Name,
		container.ID,
	)
}

// ValidatePlanInputIdentity verifies that a plan was produced from exactly the supplied immutable
// normalized Input before a target-side helper executes any plan command or imported hook.
func ValidatePlanInputIdentity(input Input, plan Plan) error {
	normalizedInput, err := NormalizeInput(input)
	if err != nil {
		return err
	}
	normalizedPlan, err := NormalizePlan(plan)
	if err != nil {
		return err
	}
	inputDigest, err := normalizedInput.Digest()
	if err != nil {
		return err
	}
	if inputDigest != normalizedPlan.InputDigest ||
		normalizedInput.Compatibility != normalizedPlan.Compatibility {
		return fmt.Errorf("device plan and input identities differ")
	}

	return nil
}

func readinessTarget(
	input Input,
	plan Plan,
	containerID string,
) (ContainerPlan, NodePlan, NodeInput, int, error) {
	var container ContainerPlan
	foundContainer := false
	for _, candidate := range plan.Containers {
		if candidate.ID == containerID {
			container = candidate
			foundContainer = true

			break
		}
	}
	if !foundContainer {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("readiness target container is absent from the plan")
	}
	var node NodePlan
	foundNode := false
	for _, candidate := range plan.Nodes {
		if candidate.ID == container.NodeID {
			node = candidate
			foundNode = true

			break
		}
	}
	if !foundNode || !slices.Contains(node.ReadinessContainerIDs, containerID) {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("readiness target is not owned by a planned logical Node")
	}
	actionCount := 0
	for _, action := range plan.Actions {
		if action.Phase == PhaseReadiness && action.Kind == ActionImportedReadiness &&
			action.Target.ContainerID == containerID {
			if action.Target.NodeID != node.ID || action.ImportedReadiness == nil {
				return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
					fmt.Errorf("imported readiness action crosses logical Node ownership")
			}
			actionCount++
		}
	}
	if actionCount != 1 {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("readiness target requires exactly one imported readiness action")
	}
	for index, candidate := range input.Nodes {
		if candidate.ID == node.ID {
			return container, node, candidate, index, nil
		}
	}

	return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
		fmt.Errorf("readiness target Node is absent from the normalized input")
}
