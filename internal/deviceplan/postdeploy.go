//nolint:nlreturn,wsl_v5 // The imported lifecycle boundary uses explicit fail-closed guards.
package deviceplan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	clabcert "github.com/srl-labs/containerlab/cert"
	clabnodes "github.com/srl-labs/containerlab/nodes"
	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
)

// RunPostDeploy rehydrates one registry Node and invokes the imported deployment and post-deploy
// hooks from inside its already-running direct application container. The supplied runtime maps
// package runtime calls to that container and never launches an inner container.
func (a Adapter) RunPostDeploy(
	ctx context.Context,
	input Input,
	plan Plan,
	containerID,
	scratchRoot,
	artifactRoot,
	certificateRoot string,
	runtime clabruntime.ContainerRuntime,
) error {
	if ctx == nil {
		return fmt.Errorf("post-deploy context is nil")
	}
	if runtime == nil {
		return fmt.Errorf("post-deploy runtime is nil")
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
	normalizedInput.Management = completeRuntimeManagement(
		normalizedInput.Management,
		normalizedInput.Nodes,
		normalizedPlan.Management,
		a.PodAddress,
		a.PodGateway,
	)
	finishEntropy, err := a.beginEntropy(normalizedInput)
	if err != nil {
		return err
	}
	defer finishEntropy()
	if strings.TrimSpace(a.Revision) == "" || a.Revision != normalizedPlan.Planner.Revision {
		return fmt.Errorf("post-deploy worker revision differs from the accepted plan")
	}
	targetContainer, targetNode, targetInput, _, err := importedPostDeployTarget(
		normalizedInput,
		normalizedPlan,
		containerID,
	)
	if err != nil {
		return err
	}

	state, err := a.rehydrateImportedDeployment(
		ctx,
		normalizedInput,
		normalizedPlan,
		targetContainer,
		targetNode,
		targetInput,
		scratchRoot,
		artifactRoot,
		certificateRoot,
		runtime,
		false,
	)
	if err != nil {
		return err
	}
	defer state.close()
	if err = runImportedRuntimeHook(
		targetNode.ID,
		"postDeployment",
		"imported-post-deploy",
		"containerlab post-deployment hook panicked",
		"running imported post-deployment hook",
		state.runtime,
		func() error {
			return state.target.PostDeploy(
				ctx,
				&clabnodes.PostDeployParams{Nodes: state.implementations},
			)
		},
	); err != nil {
		return err
	}

	return nil
}

// ImportedHookExecutor runs one imported hook in the OS execution context selected by a direct
// helper. Endpoint deployment uses this boundary to run from the worker host network namespace
// while the supplied runtime points ExecFunction at the target Pod namespace.
type ImportedHookExecutor func(operation func() error) error

// RunDeployEndpoints reconstructs one imported Node from immutable plan input and invokes its
// package-owned endpoint deployment hook. c9s realizes the RuntimeEndpoints themselves; the hook
// retains any package-defined endpoint fixup without dispatching on kind identity.
func (a Adapter) RunDeployEndpoints(
	ctx context.Context,
	input Input,
	plan Plan,
	containerID,
	scratchRoot,
	artifactRoot,
	certificateRoot string,
	runtime clabruntime.ContainerRuntime,
	execute ImportedHookExecutor,
) error {
	if ctx == nil {
		return fmt.Errorf("endpoint lifecycle context is nil")
	}
	if runtime == nil {
		return fmt.Errorf("endpoint lifecycle runtime is nil")
	}
	if execute == nil {
		return fmt.Errorf("endpoint lifecycle executor is nil")
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
	normalizedInput.Management = completeRuntimeManagement(
		normalizedInput.Management,
		normalizedInput.Nodes,
		normalizedPlan.Management,
		a.PodAddress,
		a.PodGateway,
	)
	finishEntropy, err := a.beginEntropy(normalizedInput)
	if err != nil {
		return err
	}
	defer finishEntropy()
	if strings.TrimSpace(a.Revision) == "" || a.Revision != normalizedPlan.Planner.Revision {
		return fmt.Errorf("endpoint lifecycle worker revision differs from the accepted plan")
	}
	targetContainer, targetNode, targetInput, _, err := importedDeployEndpointsTarget(
		normalizedInput,
		normalizedPlan,
		containerID,
	)
	if err != nil {
		return err
	}
	endpointBoundary, ok := runtime.(interface{ DirectEndpointLifecycleBoundary() bool })
	if !ok || !endpointBoundary.DirectEndpointLifecycleBoundary() {
		return &Error{
			Code: ErrorUnsupported, NodeID: targetNode.ID, Field: "runtime.networkNamespace",
			Behavior: "runtime.GetNSPath",
			Message:  "a distinct host worker and target application network namespace are required",
		}
	}
	state, err := a.rehydrateImportedDeployment(
		ctx,
		normalizedInput,
		normalizedPlan,
		targetContainer,
		targetNode,
		targetInput,
		scratchRoot,
		artifactRoot,
		certificateRoot,
		runtime,
		true,
	)
	if err != nil {
		return err
	}
	defer state.close()

	return execute(func() error {
		if err := runImportedRuntimeHook(
			targetNode.ID,
			"postDeployment.interfaces",
			"imported-deploy-endpoints",
			"containerlab endpoint deployment hook panicked",
			"running imported endpoint deployment hook",
			state.runtime,
			func() error { return state.target.DeployEndpoints(ctx) },
		); err != nil {
			return err
		}

		return runImportedRuntimeHook(
			targetNode.ID,
			"postDeployment.interfaces",
			"imported-post-deploy-endpoints",
			"containerlab post-endpoint deployment hook panicked",
			"running imported post-endpoint deployment hook",
			state.runtime,
			func() error { return state.target.PostDeployEndpoints(ctx) },
		)
	})
}

type importedDeploymentState struct {
	target          clabnodes.Node
	implementations map[string]clabnodes.Node
	runtime         clabruntime.ContainerRuntime
	cleanup         func()
}

func (s *importedDeploymentState) close() {
	if s != nil && s.cleanup != nil {
		s.cleanup()
	}
}

func (a Adapter) rehydrateImportedDeployment(
	ctx context.Context,
	normalizedInput Input,
	normalizedPlan Plan,
	targetContainer ContainerPlan,
	targetNode NodePlan,
	targetInput NodeInput,
	scratchRoot,
	artifactRoot,
	certificateRoot string,
	runtime clabruntime.ContainerRuntime,
	preRealizedInterfaces bool,
) (*importedDeploymentState, error) {
	scratchRoot, err := scopedDirectory(scratchRoot, "post-deploy scratch root")
	if err != nil {
		return nil, err
	}
	workspace, err := os.MkdirTemp(scratchRoot, "run-")
	if err != nil {
		return nil, fmt.Errorf("creating post-deploy workspace: %w", err)
	}
	keepWorkspace := false
	defer func() {
		if !keepWorkspace {
			_ = os.RemoveAll(workspace)
		}
	}()
	artifactRoot, err = scopedDirectory(artifactRoot, "post-deploy artifact root")
	if err != nil {
		return nil, err
	}
	targetLabDir := filepath.Join(artifactRoot, ArtifactNodeDirectory(targetNode.ID))
	if err = requireRealDirectory(targetLabDir, "post-deploy Node artifact root"); err != nil {
		return nil, err
	}
	replayRuntime := newImportedDeploymentReplayRuntime(
		runtime,
		normalizedInput.Images,
		managementForNode(normalizedInput.Management, targetNode.ID),
		filepath.Join(workspace, "runtime-replay"),
		LoadRuntimeArtifactDigests(artifactRoot, targetNode.ID),
	)

	registry := a.Registry
	if registry == nil {
		registry = NewContainerlabRegistry()
		if err = validateLiveCompatibility(registry, normalizedInput.Compatibility); err != nil {
			return nil, err
		}
	}
	certificateInfrastructure, err := mountedTargetCertificateInfrastructure(
		normalizedInput.Certificates,
		targetNode.ID,
		certificateRoot,
	)
	if err != nil {
		return nil, err
	}

	state := &importedDeploymentState{
		implementations: make(map[string]clabnodes.Node, len(normalizedInput.Nodes)*2),
		runtime:         replayRuntime,
		cleanup:         func() { _ = os.RemoveAll(workspace) },
	}
	for index, nodeInput := range normalizedInput.Nodes {
		entry := registry.Kind(nodeInput.Kind)
		if entry == nil {
			return nil, fmt.Errorf("post-deploy kind is absent from the imported registry")
		}
		definition, decodeErr := decodeNodeDefinition(nodeInput)
		if decodeErr != nil {
			return nil, decodeErr
		}
		// Payload destinations exist only inside device containers; re-run hooks read the
		// preparer-staged, digest-verified bytes from the shared artifact tree instead.
		if rewriteErr := rewriteStagedPayloadPaths(
			nodeInput.ID,
			definition,
			normalizedInput,
			normalizedPlan,
			artifactRoot,
		); rewriteErr != nil {
			return nil, rewriteErr
		}
		config, configErr := nodeConfigFromDefinition(nodeInput, definition, entry)
		if configErr != nil {
			return nil, configErr
		}
		config.Index = index
		config.LabDir = filepath.Join(workspace, ArtifactNodeDirectory(nodeInput.ID))
		if nodeInput.ID == targetNode.ID {
			config.LabDir = targetLabDir
		}
		// Embedded startup configuration must exist as a workspace file here exactly as it did
		// during planning and preparation; for the target this rewrite is idempotent with the
		// prepared artifact.
		if embeddedErr := materializeEmbeddedStartupConfig(
			nodeInput.ID,
			config,
		); embeddedErr != nil {
			return nil, embeddedErr
		}
		management := managementForNode(normalizedInput.Management, nodeInput.ID)
		applyManagementInput(config, management)
		implementation, constructErr := registry.NewNodeOfKind(nodeInput.Kind)
		if constructErr != nil {
			return nil, fmt.Errorf("constructing imported post-deploy Node: %w", constructErr)
		}
		managementNetwork := runtimeManagement(management)
		if initErr := runImportedRuntimeHook(
			nodeInput.ID,
			"postDeployment.initialization",
			"imported-post-deploy-init",
			"containerlab post-deployment initialization panicked",
			"initializing imported post-deploy hook",
			replayRuntime,
			func() error {
				return implementation.Init(
					config,
					clabnodes.WithRuntime(replayRuntime),
					clabnodes.WithMgmtNet(managementNetwork),
				)
			},
		); initErr != nil {
			return nil, initErr
		}
		interfaceEvaluator := evaluateInterfaces
		if preRealizedInterfaces {
			interfaceEvaluator = evaluatePreRealizedInterfaces
		}
		if _, interfaceErr := interfaceEvaluator(
			implementation,
			interfacesForNode(normalizedInput.Interfaces, nodeInput.ID),
			nodeInput.ID,
		); interfaceErr != nil {
			return nil, interfaceErr
		}
		state.implementations[nodeInput.Name] = implementation
		if shortName := implementation.GetShortName(); shortName != "" {
			state.implementations[shortName] = implementation
		}
		if nodeInput.ID == targetNode.ID {
			state.target = implementation
		}
	}
	if state.target == nil || targetInput.ID != targetNode.ID ||
		targetContainer.NodeID != targetNode.ID {
		return nil, fmt.Errorf("post-deploy target could not be reconstructed")
	}

	preDeploy := &clabnodes.PreDeployParams{
		Cert: certificateInfrastructure, TopologyName: normalizedInput.TopologyName,
	}
	if err = runImportedRuntimeHook(
		targetNode.ID,
		"postDeployment.preparation",
		"imported-pre-deploy",
		"containerlab target preparation panicked",
		"rehydrating imported target preparation",
		replayRuntime,
		func() error { return state.target.PreDeploy(ctx, preDeploy) },
	); err != nil {
		return nil, err
	}
	replayRuntime.beginDeployment()
	if err = runImportedRuntimeHook(
		targetNode.ID,
		"postDeployment.images",
		"imported-image-pull",
		"containerlab target image hook panicked",
		"rehydrating imported target image lifecycle",
		replayRuntime,
		func() error { return state.target.PullImage(ctx) },
	); err != nil {
		return nil, err
	}
	if err = runImportedRuntimeHook(
		targetNode.ID,
		"postDeployment.deployment",
		"imported-deploy",
		"containerlab target deployment hook panicked",
		"rehydrating imported target deployment",
		replayRuntime,
		func() error {
			return state.target.Deploy(
				ctx,
				&clabnodes.DeployParams{Nodes: state.implementations},
			)
		},
	); err != nil {
		return nil, err
	}
	if err = runImportedRuntimeHook(
		targetNode.ID,
		"postDeployment.runtimeInfo",
		"imported-runtime-info",
		"containerlab target runtime-information hook panicked",
		"rehydrating imported target runtime information",
		replayRuntime,
		func() error { return state.target.UpdateConfigWithRuntimeInfo(ctx) },
	); err != nil {
		return nil, err
	}
	readinessRuntimeIDs, err := importedContainerInventory(
		ctx,
		targetNode.ID,
		state.target,
		replayRuntime.recorder,
	)
	if err != nil {
		return nil, err
	}
	if err = verifyReplayedReadinessInventory(
		readinessRuntimeIDs,
		normalizedPlan,
		targetNode,
	); err != nil {
		return nil, err
	}
	if err = replayRuntime.completeDeployment(
		normalizedPlan,
		targetNode,
		targetContainer,
	); err != nil {
		return nil, err
	}

	keepWorkspace = true

	return state, nil
}

func verifyReplayedReadinessInventory(
	runtimeIDs []string,
	plan Plan,
	node NodePlan,
) error {
	expected := make(map[string]bool, len(node.ReadinessContainerIDs))
	readinessContainerIDs := make(map[string]bool, len(node.ReadinessContainerIDs))
	for _, containerID := range node.ReadinessContainerIDs {
		readinessContainerIDs[containerID] = true
	}
	for _, container := range plan.Containers {
		if container.NodeID == node.ID && readinessContainerIDs[container.ID] {
			expected[container.RuntimeID] = true
		}
	}
	if len(expected) != len(node.ReadinessContainerIDs) || len(runtimeIDs) != len(expected) {
		return deploymentReplayError(
			node.ID,
			"component readiness inventory differs from the accepted plan",
		)
	}
	for _, runtimeID := range runtimeIDs {
		if !expected[runtimeID] {
			return deploymentReplayError(
				node.ID,
				"component readiness inventory differs from the accepted plan",
			)
		}
	}

	return nil
}

// importedDeploymentReplayRuntime first delegates to the side-effect-free planning recorder and
// switches to the supplied live runtime only after the reconstructed Deploy operation stream has
// matched the accepted plan. Embedding the generic interface keeps this boundary independent of
// containerlab kinds and of future methods added to concrete runtime implementations.
type importedDeploymentReplayRuntime struct {
	clabruntime.ContainerRuntime
	live       clabruntime.ContainerRuntime
	recorder   *recordingRuntime
	replayRoot string
	liveActive bool
	// runtimeArtifactDigests are the preparation-recorded digests of generator files rendered
	// with the Pod's runtime management identity; the replay renders with the same identity, so
	// these are accepted wherever the plan digest is.
	runtimeArtifactDigests map[string]string
}

func newImportedDeploymentReplayRuntime(
	live clabruntime.ContainerRuntime,
	images []ImageInput,
	management *ManagementInput,
	replayRoot string,
	runtimeArtifactDigests map[string]string,
) *importedDeploymentReplayRuntime {
	recorder := newRecordingRuntime(images, management, replayRoot)

	return &importedDeploymentReplayRuntime{
		ContainerRuntime:       recorder,
		live:                   live,
		recorder:               recorder,
		replayRoot:             replayRoot,
		runtimeArtifactDigests: runtimeArtifactDigests,
	}
}

func (r *importedDeploymentReplayRuntime) beginDeployment() {
	r.recorder.BeginMutationRecording()
}

func (r *importedDeploymentReplayRuntime) completeDeployment(
	plan Plan,
	node NodePlan,
	target ContainerPlan,
) error {
	if err := r.recorder.Failure(); err != nil {
		return withNodeID(err, node.ID)
	}
	if err := verifyReplayedContainers(r.recorder.Containers(), plan, node.ID); err != nil {
		return err
	}
	if err := verifyReplayedDeploymentOperations(
		r.recorder,
		r.replayRoot,
		plan,
		node,
		target,
		r.runtimeArtifactDigests,
	); err != nil {
		return err
	}
	r.ContainerRuntime = r.live
	r.liveActive = true

	return nil
}

func (r *importedDeploymentReplayRuntime) BoundaryFailure() error {
	if !r.liveActive {
		return r.recorder.Failure()
	}
	auditor, ok := r.live.(importedRuntimeBoundaryAuditor)
	if !ok {
		return nil
	}

	return auditor.BoundaryFailure()
}

func verifyReplayedContainers(
	recorded []RecordedContainer,
	plan Plan,
	nodeID string,
) error {
	expected := map[string]bool{}
	for _, container := range plan.Containers {
		if container.NodeID == nodeID {
			expected[container.RuntimeID] = false
		}
	}
	if len(recorded) != len(expected) {
		return deploymentReplayError(nodeID, "container count differs from the accepted plan")
	}
	for _, container := range recorded {
		started, exists := expected[container.RuntimeID]
		if !exists || started || !container.Started {
			return deploymentReplayError(
				nodeID,
				"container lifecycle differs from the accepted plan",
			)
		}
		expected[container.RuntimeID] = true
	}

	return nil
}

type replayedDeploymentOperation struct {
	kind        ActionKind
	runtimeID   string
	command     []string
	wait        bool
	destination string
	writeMode   FileWriteMode
	digest      string
	// alternateDigest is the preparation-recorded runtime-management render of the same
	// generator file; a replay performed with the runtime identity legitimately produces it.
	alternateDigest string
}

// runtimeGeneratorDigest returns the runtime-rendered digest accepted for a prepared artifact,
// or empty for every other file source.
func runtimeGeneratorDigest(file FilePlan, runtimeArtifactDigests map[string]string) string {
	if file.SourceKind != FileSourceGenerator && file.SourceKind != FileSourceCertificate {
		return ""
	}

	return runtimeArtifactDigests[file.ArtifactPath]
}

func verifyReplayedDeploymentOperations(
	recorder *recordingRuntime,
	replayRoot string,
	plan Plan,
	node NodePlan,
	target ContainerPlan,
	runtimeArtifactDigests map[string]string,
) error {
	postDeployOrder := -1
	for _, action := range plan.Actions {
		if action.Phase == PhasePostStart && action.Kind == ActionImportedPostDeploy &&
			action.Target.NodeID == node.ID && action.Target.ContainerID == target.ID {
			postDeployOrder = action.Order

			break
		}
	}
	if postDeployOrder < 0 {
		return deploymentReplayError(node.ID, "imported post-deployment boundary is absent")
	}
	files := make(map[string]FilePlan, len(plan.Files))
	for _, file := range plan.Files {
		files[file.ID] = file
	}
	containers := make(map[string]ContainerPlan, len(plan.Containers))
	for _, container := range plan.Containers {
		containers[container.ID] = container
	}
	expected := make(map[int]replayedDeploymentOperation, postDeployOrder)
	for _, action := range plan.Actions {
		if action.Phase != PhasePostStart || action.Target.NodeID != node.ID ||
			action.Order >= postDeployOrder {
			continue
		}
		container, exists := containers[action.Target.ContainerID]
		if !exists {
			return deploymentReplayError(node.ID, "operation target is absent from the plan")
		}
		operation := replayedDeploymentOperation{kind: action.Kind, runtimeID: container.RuntimeID}
		switch action.Kind {
		case ActionExec:
			if action.Exec == nil {
				return deploymentReplayError(node.ID, "exec payload is incomplete")
			}
			operation.command = slices.Clone(action.Exec.Command)
			operation.wait = action.Exec.Wait
		case ActionFile:
			if action.File == nil {
				return deploymentReplayError(node.ID, "file payload is incomplete")
			}
			file, exists := files[action.File.FileID]
			if !exists {
				return deploymentReplayError(node.ID, "file source is absent from the plan")
			}
			operation.destination = action.File.Destination
			operation.writeMode = action.File.WriteMode
			operation.digest = file.Digest
			operation.alternateDigest = runtimeGeneratorDigest(file, runtimeArtifactDigests)
		case ActionWriteStdin:
			if action.WriteStdin == nil {
				return deploymentReplayError(node.ID, "stdin payload is incomplete")
			}
			file, exists := files[action.WriteStdin.FileID]
			if !exists {
				return deploymentReplayError(node.ID, "stdin source is absent from the plan")
			}
			operation.digest = file.Digest
			operation.alternateDigest = runtimeGeneratorDigest(file, runtimeArtifactDigests)
		default:
			return deploymentReplayError(node.ID, "operation kind differs from the accepted plan")
		}
		if _, exists := expected[action.Order]; exists {
			return deploymentReplayError(node.ID, "operation order is duplicated")
		}
		expected[action.Order] = operation
	}
	if len(expected) != postDeployOrder {
		return deploymentReplayError(node.ID, "operation order is incomplete")
	}

	artifacts, err := scanGeneratedArtifacts(replayRoot)
	if err != nil {
		return withNodeID(err, node.ID)
	}
	digests := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		digests[artifact.Path] = artifact.Digest
	}
	actual := make(map[int]replayedDeploymentOperation, postDeployOrder)
	appendOperation := func(order int, operation replayedDeploymentOperation) error {
		if order < 0 || order >= postDeployOrder {
			return deploymentReplayError(node.ID, "operation order is outside the accepted plan")
		}
		if _, exists := actual[order]; exists {
			return deploymentReplayError(node.ID, "operation order is duplicated")
		}
		actual[order] = operation

		return nil
	}
	for _, operation := range recorder.Execs() {
		if err = appendOperation(operation.Order, replayedDeploymentOperation{
			kind: ActionExec, runtimeID: operation.RuntimeID,
			command: operation.Command, wait: operation.Wait,
		}); err != nil {
			return err
		}
	}
	for _, operation := range recorder.Copies() {
		if err = appendOperation(operation.Order, replayedDeploymentOperation{
			kind: ActionFile, runtimeID: operation.RuntimeID,
			destination: operation.Destination, writeMode: operation.WriteMode,
			digest: digests[operation.ArtifactPath],
		}); err != nil {
			return err
		}
	}
	for _, operation := range recorder.Stdins() {
		if err = appendOperation(operation.Order, replayedDeploymentOperation{
			kind: ActionWriteStdin, runtimeID: operation.RuntimeID,
			digest: digests[operation.ArtifactPath],
		}); err != nil {
			return err
		}
	}
	if len(actual) != len(expected) {
		return deploymentReplayError(node.ID, "operation count differs from the accepted plan")
	}
	for order := 0; order < postDeployOrder; order++ {
		want, wantExists := expected[order]
		got, gotExists := actual[order]
		if !wantExists || !gotExists || !sameReplayedDeploymentOperation(got, want) {
			return deploymentReplayError(node.ID, "operation stream differs from the accepted plan")
		}
	}

	return nil
}

func sameReplayedDeploymentOperation(
	left,
	right replayedDeploymentOperation,
) bool {
	if left.kind != right.kind || left.runtimeID != right.runtimeID ||
		!slices.Equal(left.command, right.command) || left.wait != right.wait ||
		left.destination != right.destination || left.writeMode != right.writeMode {
		return false
	}
	if left.kind == ActionFile || left.kind == ActionWriteStdin {
		if left.digest == "" || right.digest == "" {
			return false
		}

		return left.digest == right.digest ||
			left.digest == right.alternateDigest ||
			right.digest == left.alternateDigest
	}

	return true
}

func deploymentReplayError(nodeID, message string) error {
	return &Error{
		Code: ErrorInvariant, NodeID: nodeID, Field: "deployment.replay",
		Behavior: "imported-deploy", Message: message,
	}
}

type importedRuntimeBoundaryAuditor interface {
	BoundaryFailure() error
}

func runImportedRuntimeHook(
	nodeID,
	field,
	behavior,
	panicMessage,
	failureMessage string,
	runtime clabruntime.ContainerRuntime,
	operation func() error,
) error {
	hookErr := invokeImported(nodeID, field, behavior, panicMessage, operation)
	if boundaryErr := importedRuntimeBoundaryFailure(runtime, nodeID); boundaryErr != nil {
		return boundaryErr
	}
	if hookErr != nil {
		return fmt.Errorf("%s: %w", failureMessage, hookErr)
	}

	return nil
}

func importedRuntimeBoundaryFailure(runtime clabruntime.ContainerRuntime, nodeID string) error {
	auditor, ok := runtime.(importedRuntimeBoundaryAuditor)
	if !ok {
		return nil
	}
	err := auditor.BoundaryFailure()
	if err == nil {
		return nil
	}

	return withNodeID(err, nodeID)
}

func importedPostDeployTarget(
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
			fmt.Errorf("post-deploy target container is absent from the plan")
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
	if !foundNode || !nodeOwnsContainer(node, containerID) {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("post-deploy target is not a container of a planned logical Node")
	}
	actionCount := 0
	for _, action := range plan.Actions {
		if action.Phase == PhasePostStart && action.Kind == ActionImportedPostDeploy &&
			action.Target.ContainerID == containerID {
			if action.Target.NodeID != node.ID || action.ImportedPostDeploy == nil {
				return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
					fmt.Errorf("imported post-deploy action crosses logical Node ownership")
			}
			actionCount++
		}
	}
	if actionCount != 1 {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("post-deploy target requires exactly one imported post-deploy action")
	}
	for index, candidate := range input.Nodes {
		if candidate.ID == node.ID {
			return container, node, candidate, index, nil
		}
	}

	return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
		fmt.Errorf("post-deploy target Node is absent from the normalized input")
}

// nodeOwnsContainer reports whether the planned logical Node owns the given container. Imported
// lifecycle targets are whichever member container the package declared as its exec identity, so
// ownership — not position — is the plan invariant.
func nodeOwnsContainer(node NodePlan, containerID string) bool {
	for _, candidate := range node.ContainerIDs {
		if candidate == containerID {
			return true
		}
	}

	return false
}

func importedDeployEndpointsTarget(
	input Input,
	plan Plan,
	containerID string,
) (ContainerPlan, NodePlan, NodeInput, int, error) {
	container, node, nodeInput, nodeIndex, err := importedPostDeployTarget(input, plan, containerID)
	if err != nil {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0, err
	}
	actionCount := 0
	for _, action := range plan.Actions {
		if action.Phase != PhaseInterfaceFixup ||
			action.Kind != ActionImportedDeployEndpoints ||
			action.Target.ContainerID != containerID {
			continue
		}
		if action.Target.NodeID != node.ID || action.Target.NamespaceOwnerID == "" ||
			action.ImportedDeployEndpoints == nil {
			return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
				fmt.Errorf("imported endpoint action crosses logical Node ownership")
		}
		actionCount++
	}
	if actionCount != 1 {
		return ContainerPlan{}, NodePlan{}, NodeInput{}, 0,
			fmt.Errorf("endpoint target requires exactly one imported endpoint action")
	}

	return container, node, nodeInput, nodeIndex, nil
}

func scopedDirectory(value, description string) (string, error) {
	root := filepath.Clean(value)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return "", fmt.Errorf("%s must be a scoped absolute path", description)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", description, err)
	}
	if err := requireRealDirectory(root, description); err != nil {
		return "", err
	}

	return root, nil
}

func requireRealDirectory(value, description string) error {
	info, err := os.Lstat(value)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a real directory", description)
	}

	return nil
}

func runtimeManagement(input *ManagementInput) *clabtypes.MgmtNet {
	if input == nil {
		return &clabtypes.MgmtNet{}
	}

	return &clabtypes.MgmtNet{
		IPv4Gw: input.IPv4Gateway,
		IPv6Gw: input.IPv6Gateway,
	}
}

func mountedTargetCertificateInfrastructure(
	inputs []CertificateInput,
	nodeID,
	root string,
) (*clabcert.Cert, error) {
	nodeInputs := make([]CertificateInput, 0, len(inputs))
	for _, input := range inputs {
		if input.NodeID == nodeID {
			nodeInputs = append(nodeInputs, input)
		}
	}
	if len(nodeInputs) == 0 {
		return nil, nil
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return nil, fmt.Errorf("post-deploy certificates require a scoped Secret projection")
	}
	caCertificate, err := readCertificateFile(filepath.Join(root, CertificateCACertKey))
	if err != nil {
		return nil, err
	}
	storage := &mountedCertificateStorage{
		ca:    &clabcert.Certificate{Cert: slices.Clone(caCertificate)},
		nodes: make(map[string]*clabcert.Certificate, len(nodeInputs)),
	}
	for _, input := range nodeInputs {
		if Digest(caCertificate) != input.CACertificateDigest {
			return nil, fmt.Errorf(
				"post-deploy certificate authority differs from accepted metadata",
			)
		}
		certificateKey, privateKeyKey := CertificateMaterialKeys(input.NodeID, input.StorageName)
		certificate, readErr := readCertificateFile(filepath.Join(root, certificateKey))
		if readErr != nil {
			return nil, withNodeID(readErr, input.NodeID)
		}
		privateKey, readErr := readCertificateFile(filepath.Join(root, privateKeyKey))
		if readErr != nil {
			return nil, withNodeID(readErr, input.NodeID)
		}
		if Digest(certificate) != input.CertificateDigest ||
			Digest(privateKey) != input.PrivateKeyDigest {
			return nil, fmt.Errorf("post-deploy node certificate differs from accepted metadata")
		}
		if storage.nodes[input.StorageName] != nil {
			return nil, fmt.Errorf("post-deploy certificate storage identity is duplicated")
		}
		storage.nodes[input.StorageName] = &clabcert.Certificate{
			Cert: slices.Clone(certificate), Key: slices.Clone(privateKey),
		}
	}

	// Node material is issued by the controller before the application starts. Omitting the CA
	// signer here prevents its private key from entering an application container; standard
	// package LoadOrGenerateCertificate calls resolve the already-issued Node material first.
	return &clabcert.Cert{CertStorage: storage}, nil
}
