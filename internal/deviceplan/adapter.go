//nolint:nlreturn,wsl_v5 // Adapter guards are clearer as compact fail-closed checks.
package deviceplan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	clabcert "github.com/srl-labs/containerlab/cert"
	clabconstants "github.com/srl-labs/containerlab/constants"
	clablinks "github.com/srl-labs/containerlab/links"
	clabnodes "github.com/srl-labs/containerlab/nodes"
	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
	"gopkg.in/yaml.v2"
)

const maxGeneratedSymlinkTargetBytes = 4096

// Adapter evaluates explicit inputs against the pinned containerlab registry through generic
// containerlab interfaces. It contains no kind-name dispatch.
type Adapter struct {
	Registry *clabnodes.NodeRegistry
	Revision string
	// EntropyRoot is the read-only Secret projection holding the private seed used to replay
	// package-owned randomness without exposing it in the normalized input or plan.
	EntropyRoot string
	// PayloadRoot is the read-only root populated by the worker Pod with explicitly declared
	// ConfigMap and Secret payloads. It is consulted only when an imported generic path field
	// references the corresponding payload destination.
	PayloadRoot string
	// CertificateRoot is the read-only Secret projection holding certificate material whose
	// digests are explicit normalized inputs. Kind-specific issuance requests still originate
	// solely from imported package hooks.
	CertificateRoot string
	// CheckDeploymentConditions runs the imported package's generic preflight hook. It is enabled
	// only by the preparation worker after Kubernetes has scheduled the direct Pod onto its target
	// node, so host CPU, kernel, memory, and device observations describe the actual worker.
	CheckDeploymentConditions bool
}

// Evaluation is the initialized imported intent consumed by generic c9s plan mappings.
type Evaluation struct {
	Input       Input
	InputDigest string
	Nodes       []EvaluatedNode
}

// EvaluatedNode is an in-memory, non-serialized snapshot of one imported kind initialization.
// Config may contain upstream defaults and must be mapped into c9s plan types before persistence.
type EvaluatedNode struct {
	Input                NodeInput
	Config               *clabtypes.NodeConfig
	Containers           []RecordedContainer
	GeneratedArtifacts   []GeneratedArtifact
	Images               []ImageReference
	MissingImages        []string
	Interfaces           []EvaluatedInterface
	ReadinessRuntimeIDs  []string
	LinkApplyMode        LinkApplyMode
	PrivilegedByDefault  bool
	CertificateBacked    bool
	RuntimeCalls         []string
	preparationArtifacts map[string]GeneratedArtifact
	implementation       clabnodes.Node
	definition           *clabtypes.NodeDefinition
	recorder             *recordingRuntime
	scratchLabDir        string
	stableLabDir         string
}

// EvaluatedInterface is one explicit endpoint after imported interface-name normalization.
type EvaluatedInterface struct {
	Input InterfaceInput
	Name  string
	Alias string
}

// GeneratedArtifact is metadata for one filesystem entry emitted by imported initialization or
// preparation inside the controlled workspace. Bytes never enter the serialized plan.
type GeneratedArtifact struct {
	Path               string
	Digest             string
	Mode               uint32
	Kind               ArtifactKind
	LinkTarget         string
	UID                *int64
	GID                *int64
	ExtendedAttributes []generatedExtendedAttribute
	SourceReference    string
}

type generatedExtendedAttribute struct {
	Name   string
	Digest string
	value  []byte
}

// ImageReference is one deterministic image role returned by an imported kind.
type ImageReference struct {
	Role      string
	Reference string
}

type imageMetadataRequiredError struct {
	nodeID     string
	references []string
}

func (e *imageMetadataRequiredError) Error() string {
	return "imported initialization requires additional explicit OCI metadata"
}

// Evaluate initializes every imported registry kind through the same recording runtime. A
// disposable workspace confines initializers that generate files while preserving one generic path.
func (a Adapter) Evaluate(ctx context.Context, input Input) (*Evaluation, error) {
	if ctx == nil {
		return nil, planningError(ErrorInvalidInput, "context", "context is nil", nil)
	}
	if strings.TrimSpace(a.Revision) == "" {
		return nil, planningError(
			ErrorMissingInput,
			"adapter.revision",
			"revision is required",
			nil,
		)
	}

	normalized, err := NormalizeInput(input)
	if err != nil {
		return nil, err
	}
	inputDigest, err := normalized.Digest()
	if err != nil {
		return nil, err
	}
	finishEntropy, err := beginImportedEntropy(
		inputDigest,
		normalized.EntropyDigest,
		a.EntropyRoot,
	)
	if err != nil {
		return nil, err
	}
	defer finishEntropy()
	registry := a.Registry
	if registry == nil {
		registry = NewContainerlabRegistry()
		if err = validateLiveCompatibility(registry, normalized.Compatibility); err != nil {
			return nil, err
		}
	}
	scratchRoot, err := os.MkdirTemp("", "clabernetes-device-plan-")
	if err != nil {
		return nil, planningError(
			ErrorSideEffect,
			"workspace",
			"cannot create controlled planning workspace",
			err,
		)
	}
	defer os.RemoveAll(scratchRoot)

	return evaluateInWorkspace(
		ctx,
		normalized,
		inputDigest,
		registry,
		scratchRoot,
		a.PayloadRoot,
		a.CertificateRoot,
		a.CheckDeploymentConditions,
	)
}

func evaluateInWorkspace(
	ctx context.Context,
	normalized Input,
	inputDigest string,
	registry *clabnodes.NodeRegistry,
	scratchRoot,
	payloadRoot,
	certificateRoot string,
	checkDeploymentConditions bool,
) (*Evaluation, error) {
	result := &Evaluation{
		Input: normalized, InputDigest: inputDigest,
		Nodes: make([]EvaluatedNode, 0, len(normalized.Nodes)),
	}
	for index, nodeInput := range normalized.Nodes {
		evaluated, evaluateErr := evaluateNode(
			ctx,
			registry,
			normalized,
			nodeInput,
			index,
			scratchRoot,
			payloadRoot,
			true,
		)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		result.Nodes = append(result.Nodes, *evaluated)
	}
	certificateInfrastructure, err := mountedCertificateInfrastructure(
		normalized.Certificates,
		certificateRoot,
	)
	if err != nil {
		return nil, err
	}
	if checkDeploymentConditions {
		if err := recordDeploymentConditions(ctx, result.Nodes); err != nil {
			return nil, err
		}
	}
	if err := recordPreparations(
		ctx,
		result.Nodes,
		normalized.TopologyName,
		certificateInfrastructure,
	); err != nil {
		return nil, err
	}
	if err := capturePreparationArtifacts(result.Nodes); err != nil {
		return nil, err
	}
	if err := recordDeployments(ctx, result.Nodes, certificateInfrastructure); err != nil {
		return nil, err
	}
	if err := finalizeEvaluations(result.Nodes); err != nil {
		return nil, err
	}

	return result, nil
}

func recordDeploymentConditions(ctx context.Context, nodes []EvaluatedNode) error {
	for index := range nodes {
		node := &nodes[index]
		hookErr := invokeImported(
			node.Input.ID,
			"deployment.conditions",
			"imported-deployment-conditions",
			"containerlab deployment-condition hook panicked",
			func() error { return node.implementation.CheckDeploymentConditions(ctx) },
		)
		if recorderErr := node.recorder.Failure(); recorderErr != nil {
			return withNodeID(recorderErr, node.Input.ID)
		}
		if hookErr != nil {
			var planningErr *Error
			if errors.As(hookErr, &planningErr) {
				return planningErr
			}
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: "deployment.conditions",
				Behavior: "imported-deployment-conditions",
				Message:  "containerlab deployment conditions are not satisfied on the target worker",
				cause:    hookErr,
			}
		}
	}

	return nil
}

func recordPreparations(
	ctx context.Context,
	nodes []EvaluatedNode,
	topologyName string,
	certificateInfrastructure *clabcert.Cert,
) error {
	params := &clabnodes.PreDeployParams{
		Cert: certificateInfrastructure, TopologyName: topologyName,
	}
	for index := range nodes {
		node := &nodes[index]
		activateCertificateStorage(certificateInfrastructure, node.Input.ID)
		verifyErr := invokeImported(
			node.Input.ID,
			"definition.startup-config",
			"imported-startup-config",
			"containerlab startup-configuration verification panicked",
			func() error { return node.implementation.VerifyStartupConfig(node.scratchLabDir) },
		)
		if verifyErr != nil {
			var planningErr *Error
			if errors.As(verifyErr, &planningErr) {
				return planningErr
			}
			return &Error{
				Code: ErrorMissingInput, NodeID: node.Input.ID,
				Field: "definition.startup-config", Behavior: "imported-startup-config",
				Message: "containerlab startup-configuration input could not be verified",
				cause:   verifyErr,
			}
		}
		hookErr := invokeImported(
			node.Input.ID,
			"preparation",
			"imported-pre-deploy",
			"containerlab preparation hook panicked",
			func() error { return node.implementation.PreDeploy(ctx, params) },
		)
		if recorderErr := node.recorder.Failure(); recorderErr != nil {
			return withNodeID(recorderErr, node.Input.ID)
		}
		if hookErr != nil {
			var planningErr *Error
			if errors.As(hookErr, &planningErr) {
				return planningErr
			}
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: "preparation",
				Behavior: "imported-pre-deploy",
				Message:  "containerlab preparation hook could not be evaluated",
				cause:    hookErr,
			}
		}
	}

	return nil
}

func evaluateNode(
	ctx context.Context,
	registry *clabnodes.NodeRegistry,
	input Input,
	nodeInput NodeInput,
	index int,
	scratchRoot,
	payloadRoot string,
	requireImageMetadata bool,
) (*EvaluatedNode, error) {
	entry := registry.Kind(nodeInput.Kind)
	if entry == nil {
		return nil, &Error{
			Code:    ErrorInvalidInput,
			NodeID:  nodeInput.ID,
			Field:   "kind",
			Message: "kind is absent from the imported containerlab registry",
		}
	}
	definition, err := decodeNodeDefinition(nodeInput)
	if err != nil {
		return nil, err
	}
	if err = rewriteExplicitPayloadPaths(
		nodeInput.ID,
		definition,
		input.Payloads,
		payloadRoot,
	); err != nil {
		return nil, err
	}
	config, err := nodeConfigFromDefinition(nodeInput, definition, entry)
	if err != nil {
		return nil, err
	}
	config.Index = index
	stableLabDir := config.LabDir
	scratchLabDir := filepath.Join(
		scratchRoot,
		strings.TrimPrefix(Digest([]byte(nodeInput.ID)), "sha256:"),
	)
	config.LabDir = scratchLabDir
	management := managementForNode(input.Management, nodeInput.ID)
	applyManagementInput(config, management)

	recorder := newRecordingRuntime(input.Images, management, scratchLabDir)
	if !requireImageMetadata {
		recorder.AllowMissingImageMetadata()
	}
	implementation, err := registry.NewNodeOfKind(nodeInput.Kind)
	if err != nil {
		return nil, &Error{
			Code: ErrorInvalidInput, NodeID: nodeInput.ID, Field: "kind",
			Message: "cannot construct imported kind", cause: err,
		}
	}
	err = invokeImported(
		nodeInput.ID,
		"definition",
		"imported-init",
		"containerlab kind initialization panicked",
		func() error {
			return implementation.Init(
				config,
				clabnodes.WithRuntime(recorder),
				clabnodes.WithMgmtNet(recorder.Mgmt()),
			)
		},
	)
	if err != nil {
		if !requireImageMetadata {
			missingImages := recorder.MissingImages()
			if len(missingImages) != 0 {
				return nil, &imageMetadataRequiredError{
					nodeID: nodeInput.ID, references: missingImages,
				}
			}
		}
		var planningErr *Error
		if errors.As(err, &planningErr) {
			return nil, planningErr
		}
		return nil, &Error{
			Code: ErrorInvalidInput, NodeID: nodeInput.ID, Field: "definition",
			Behavior: "imported-init", Message: "containerlab kind initialization failed", cause: err,
		}
	}
	if err = recorder.Failure(); err != nil {
		return nil, withNodeID(err, nodeInput.ID)
	}
	interfaces, err := evaluateInterfaces(
		implementation,
		interfacesForNode(input.Interfaces, nodeInput.ID),
		nodeInput.ID,
	)
	if err != nil {
		return nil, err
	}

	importedImages, err := evaluateImported(
		nodeInput.ID,
		"images",
		"imported-images",
		"containerlab image discovery panicked",
		func() map[string]string { return implementation.GetImages(ctx) },
	)
	if err != nil {
		return nil, err
	}
	images := mapImageReferences(importedImages)
	if requireImageMetadata {
		err = validateImageInputs(nodeInput.ID, images, input.Images)
	}
	if err != nil {
		return nil, err
	}
	importedLinkMode, err := evaluateImported(
		nodeInput.ID,
		"interfaces",
		"imported-link-apply-mode",
		"containerlab link lifecycle discovery panicked",
		func() clabnodes.LinkApplyMode {
			return clabnodes.LinkApplyModeForNode(ctx, implementation)
		},
	)
	if err != nil {
		return nil, err
	}
	linkMode := mapLinkApplyMode(importedLinkMode)
	if err = recorder.Failure(); err != nil {
		return nil, withNodeID(err, nodeInput.ID)
	}
	config = implementation.Config()
	if config == nil {
		return nil, &Error{
			Code: ErrorInvariant, NodeID: nodeInput.ID, Field: "kind",
			Behavior: "initializer", Message: "kind initializer returned no configuration",
		}
	}

	return &EvaluatedNode{
		Input:               nodeInput,
		Config:              config,
		Images:              images,
		MissingImages:       recorder.MissingImages(),
		Interfaces:          interfaces,
		LinkApplyMode:       linkMode,
		PrivilegedByDefault: entry.PrivilegedByDefault(),
		CertificateBacked:   nodeHasCertificateInput(input.Certificates, nodeInput.ID),
		RuntimeCalls:        recorder.Calls(),
		implementation:      implementation,
		definition:          definition,
		recorder:            recorder,
		scratchLabDir:       scratchLabDir,
		stableLabDir:        stableLabDir,
	}, nil
}

func evaluateInterfaces(
	implementation clabnodes.Node,
	inputs []InterfaceInput,
	nodeID string,
) ([]EvaluatedInterface, error) {
	return evaluateInterfacesWithRuntimeState(implementation, inputs, nodeID, false)
}

// preRealizedEndpoint retains the imported topology metadata while telling the imported default
// endpoint hook that c9s already realized this interface in the direct Pod namespace.
type preRealizedEndpoint struct {
	clablinks.Endpoint
}

func (*preRealizedEndpoint) IsRuntimeDiscovered() bool {
	return true
}

func evaluatePreRealizedInterfaces(
	implementation clabnodes.Node,
	inputs []InterfaceInput,
	nodeID string,
) ([]EvaluatedInterface, error) {
	return evaluateInterfacesWithRuntimeState(implementation, inputs, nodeID, true)
}

func evaluateInterfacesWithRuntimeState(
	implementation clabnodes.Node,
	inputs []InterfaceInput,
	nodeID string,
	preRealized bool,
) ([]EvaluatedInterface, error) {
	result := make([]EvaluatedInterface, 0, len(inputs))
	for _, input := range inputs {
		linkMTU := input.MTU
		if linkMTU == 0 {
			linkMTU = clabconstants.DefaultLinkMTU
		}
		link := clablinks.NewLinkVEth()
		link.LinkCommonParams = clablinks.LinkCommonParams{
			MTU:  linkMTU,
			Vars: map[string]any{},
		}
		var endpoint clablinks.Endpoint = clablinks.NewEndpointVeth(
			clablinks.NewEndpointGeneric(implementation, input.Name, link),
		)
		if preRealized {
			endpoint = &preRealizedEndpoint{Endpoint: endpoint}
		}
		link.Endpoints = append(link.Endpoints, endpoint)
		err := invokeImported(
			nodeID,
			"interfaces",
			"imported-interface",
			"containerlab endpoint attachment panicked",
			func() error { return implementation.AddEndpoint(endpoint) },
		)
		if err != nil {
			var planningErr *Error
			if errors.As(err, &planningErr) {
				return nil, planningErr
			}
			return nil, &Error{
				Code: ErrorInvalidInput, NodeID: nodeID, Field: "interfaces",
				Behavior: "imported-interface", Message: "containerlab rejected an endpoint",
				cause: err,
			}
		}
		result = append(result, EvaluatedInterface{
			Input: input,
			Name:  endpoint.GetIfaceName(),
			Alias: endpoint.GetIfaceAlias(),
		})
	}
	err := invokeImported(
		nodeID,
		"interfaces",
		"imported-interface",
		"containerlab interface validation panicked",
		implementation.CheckInterfaceName,
	)
	if err != nil {
		var planningErr *Error
		if errors.As(err, &planningErr) {
			return nil, planningErr
		}
		return nil, &Error{
			Code: ErrorInvalidInput, NodeID: nodeID, Field: "interfaces",
			Behavior: "imported-interface", Message: "containerlab interface validation failed",
			cause: err,
		}
	}

	return result, nil
}

func recordDeployments(
	ctx context.Context,
	nodes []EvaluatedNode,
	certificateInfrastructure *clabcert.Cert,
) error {
	params := &clabnodes.DeployParams{Nodes: importedNodeMap(nodes)}
	for index := range nodes {
		node := &nodes[index]
		activateCertificateStorage(certificateInfrastructure, node.Input.ID)
		node.recorder.BeginMutationRecording()
		pullErr := invokeImported(
			node.Input.ID,
			"images",
			"imported-image-pull",
			"containerlab image-pull hook panicked",
			func() error { return node.implementation.PullImage(ctx) },
		)
		if pullErr != nil {
			var planningErr *Error
			if errors.As(pullErr, &planningErr) {
				return planningErr
			}
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: "images",
				Behavior: "imported-image-pull",
				Message:  "containerlab image-pull hook could not be recorded",
				cause:    pullErr,
			}
		}
		if recorderErr := node.recorder.Failure(); recorderErr != nil {
			return withNodeID(recorderErr, node.Input.ID)
		}
		err := invokeImported(
			node.Input.ID,
			"deployment",
			"imported-deploy",
			"containerlab deployment hook panicked",
			func() error { return node.implementation.Deploy(ctx, params) },
		)
		if err != nil {
			var planningErr *Error
			if errors.As(err, &planningErr) {
				return planningErr
			}
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: "deployment",
				Behavior: "imported-deploy", Message: "containerlab deployment hook could not be recorded",
				cause: err,
			}
		}
		if err := node.recorder.Failure(); err != nil {
			return withNodeID(err, node.Input.ID)
		}
		updateErr := invokeImported(
			node.Input.ID,
			"deployment.runtimeInfo",
			"imported-runtime-info",
			"containerlab runtime-information hook panicked",
			func() error { return node.implementation.UpdateConfigWithRuntimeInfo(ctx) },
		)
		if updateErr != nil {
			var planningErr *Error
			if errors.As(updateErr, &planningErr) {
				return planningErr
			}
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: "deployment.runtimeInfo",
				Behavior: "imported-runtime-info",
				Message:  "containerlab runtime-information hook could not be recorded",
				cause:    updateErr,
			}
		}
		if err := node.recorder.Failure(); err != nil {
			return withNodeID(err, node.Input.ID)
		}
		node.Containers = node.recorder.Containers()
		if len(node.Containers) == 0 {
			continue
		}
		readinessRuntimeIDs, inventoryErr := importedContainerInventory(
			ctx,
			node.Input.ID,
			node.implementation,
			node.recorder,
		)
		if inventoryErr != nil {
			return inventoryErr
		}

		node.ReadinessRuntimeIDs = readinessRuntimeIDs
	}

	return nil
}

func importedContainerInventory(
	ctx context.Context,
	nodeID string,
	implementation clabnodes.Node,
	recorder *recordingRuntime,
) ([]string, error) {
	var containers []clabruntime.GenericContainer
	hookErr := invokeImported(
		nodeID,
		"deployment.components",
		"imported-components",
		"containerlab component inventory hook panicked",
		func() error {
			var err error
			containers, err = implementation.GetContainers(ctx)

			return err
		},
	)
	if recorderErr := recorder.Failure(); recorderErr != nil {
		return nil, withNodeID(recorderErr, nodeID)
	}
	if hookErr != nil {
		var planningErr *Error
		if errors.As(hookErr, &planningErr) {
			return nil, planningErr
		}

		return nil, &Error{
			Code: ErrorUnsupported, NodeID: nodeID, Field: "deployment.components",
			Behavior: "imported-components",
			Message:  "containerlab component inventory could not be recorded",
			cause:    hookErr,
		}
	}
	if len(containers) == 0 {
		return nil, &Error{
			Code: ErrorInvariant, NodeID: nodeID, Field: "deployment.components",
			Behavior: "imported-components",
			Message:  "containerlab component inventory is empty",
		}
	}
	recorded := recorder.Containers()
	known := make(map[string]bool, len(recorded))
	for _, container := range recorded {
		known[container.RuntimeID] = true
	}
	result := make([]string, 0, len(containers))
	seen := make(map[string]bool, len(containers))
	for _, container := range containers {
		if container.ID == "" || !known[container.ID] || seen[container.ID] {
			return nil, &Error{
				Code: ErrorInvariant, NodeID: nodeID, Field: "deployment.components",
				Behavior: "imported-components",
				Message:  "containerlab component inventory is absent, foreign, or duplicated",
			}
		}
		seen[container.ID] = true
		result = append(result, container.ID)
	}

	return result, nil
}

func invokeImported(
	nodeID,
	field,
	behavior,
	panicMessage string,
	operation func() error,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			cause := importedPanicCause(recovered)
			err = &Error{
				Code: ErrorUnsupported, NodeID: nodeID, Field: field,
				Behavior: behavior, Message: panicMessage + ": " + cause.Error(),
				cause: cause,
			}
		}
	}()

	return withImportedEntropy(nodeID, behavior, operation)
}

func importedPanicCause(recovered any) error {
	location := importedPanicLocation()
	if runtimeErr, ok := recovered.(runtime.Error); ok {
		return fmt.Errorf("imported hook runtime panic at %s: %w", location, runtimeErr)
	}

	// Imported hooks may panic with values containing payload or credential material. Preserve the
	// generic value type for diagnosis without copying the value into conditions, events, or logs.
	return fmt.Errorf("imported hook panic value type %T at %s", recovered, location)
}

func importedPanicLocation() string {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	for {
		frame, more := frames.Next()
		if !strings.HasPrefix(frame.Function, "runtime.") &&
			!strings.Contains(frame.Function, "internal/deviceplan.importedPanic") &&
			!strings.Contains(frame.Function, "internal/deviceplan.invokeImported") {
			return fmt.Sprintf("%s (%s:%d)", frame.Function, filepath.Base(frame.File), frame.Line)
		}
		if !more {
			break
		}
	}

	return "unknown imported frame"
}

func evaluateImported[T any](
	nodeID,
	field,
	behavior,
	panicMessage string,
	operation func() T,
) (value T, err error) {
	err = invokeImported(nodeID, field, behavior, panicMessage, func() error {
		value = operation()

		return nil
	})

	return value, err
}

func finalizeEvaluations(nodes []EvaluatedNode) error {
	for index := range nodes {
		node := &nodes[index]
		for containerIndex := range node.Containers {
			rewriteWorkspacePaths(
				node.Containers[containerIndex].Config,
				node.scratchLabDir,
				node.stableLabDir,
			)
		}
		artifacts, err := scanGeneratedArtifacts(node.scratchLabDir)
		if err != nil {
			return withNodeID(err, node.Input.ID)
		}
		for artifactIndex := range artifacts {
			artifact := &artifacts[artifactIndex]
			artifact.SourceReference = "containerlab/imported-lifecycle"
			if prepared, exists := node.preparationArtifacts[artifact.Path]; exists &&
				prepared.Digest == artifact.Digest && prepared.Mode == artifact.Mode &&
				prepared.Kind == artifact.Kind && prepared.LinkTarget == artifact.LinkTarget &&
				reflect.DeepEqual(prepared.UID, artifact.UID) &&
				reflect.DeepEqual(prepared.GID, artifact.GID) &&
				reflect.DeepEqual(prepared.ExtendedAttributes, artifact.ExtendedAttributes) {
				artifact.SourceReference = "containerlab/imported-prepare"
			}
		}
		node.GeneratedArtifacts = artifacts
		node.Config = node.implementation.Config()
		rewriteWorkspacePaths(node.Config, node.scratchLabDir, node.stableLabDir)
		node.RuntimeCalls = node.recorder.Calls()
	}

	return nil
}

func capturePreparationArtifacts(nodes []EvaluatedNode) error {
	for index := range nodes {
		node := &nodes[index]
		artifacts, err := scanGeneratedArtifacts(node.scratchLabDir)
		if err != nil {
			return withNodeID(err, node.Input.ID)
		}
		node.preparationArtifacts = make(map[string]GeneratedArtifact, len(artifacts))
		for _, artifact := range artifacts {
			artifact.SourceReference = "containerlab/imported-prepare"
			node.preparationArtifacts[artifact.Path] = artifact
		}
	}

	return nil
}

func importedNodeMap(nodes []EvaluatedNode) map[string]clabnodes.Node {
	result := make(map[string]clabnodes.Node, len(nodes))
	for index := range nodes {
		node := &nodes[index]
		result[node.Input.Name] = node.implementation
		if shortName := node.implementation.GetShortName(); shortName != "" {
			result[shortName] = node.implementation
		}
	}

	return result
}

func scanGeneratedArtifacts(root string) ([]GeneratedArtifact, error) {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, planningError(
			ErrorSideEffect,
			"workspace",
			"cannot inspect imported generated-artifact root",
			err,
		)
	}
	artifacts := []GeneratedArtifact{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := "."
		if path != root {
			var err error
			relative, err = filepath.Rel(root, path)
			if err != nil {
				return err
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		uid, gid, extendedAttributes, err := readGeneratedArtifactMetadata(
			path,
			info.Mode()&os.ModeSymlink != 0,
			os.Geteuid(),
			os.Getegid(),
		)
		if err != nil {
			return err
		}
		artifact := GeneratedArtifact{
			Path: filepath.ToSlash(relative), Mode: uint32(info.Mode().Perm()),
			UID: uid, GID: gid, ExtendedAttributes: extendedAttributes,
		}
		if info.IsDir() {
			artifact.Kind = ArtifactDirectory
			artifacts = append(artifacts, artifact)

			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil || target == "" || len(target) > maxGeneratedSymlinkTargetBytes ||
				strings.ContainsRune(target, 0) {
				return &Error{
					Code: ErrorUnsupported, Field: "workspace",
					Behavior: "generated-symlink",
					Message:  "imported preparation generated an invalid symbolic link",
					cause:    err,
				}
			}
			artifact.Digest = Digest([]byte(target))
			artifact.Kind = ArtifactSymlink
			artifact.LinkTarget = target
			artifact.Mode = 0
			artifacts = append(artifacts, artifact)

			return nil
		}
		if !info.Mode().IsRegular() {
			return &Error{
				Code: ErrorUnsupported, Field: "workspace",
				Behavior: "generated-file-type",
				Message:  "imported preparation generated a non-regular artifact",
			}
		}
		content, err := os.ReadFile(path) //nolint:gosec // path is confined under planner scratch.
		if err != nil {
			return err
		}
		artifact.Digest = Digest(content)
		artifact.Kind = ArtifactRegular
		artifacts = append(artifacts, artifact)

		return nil
	})
	if err != nil {
		var planningErr *Error
		if errors.As(err, &planningErr) {
			return nil, planningErr
		}

		return nil, planningError(
			ErrorSideEffect,
			"workspace",
			"cannot inventory imported generated artifacts",
			err,
		)
	}
	slices.SortFunc(artifacts, func(left, right GeneratedArtifact) int {
		return strings.Compare(left.Path, right.Path)
	})

	return artifacts, nil
}

func rewriteWorkspacePaths(config *clabtypes.NodeConfig, scratchRoot, stableRoot string) {
	rewrite := func(value string) string {
		return strings.ReplaceAll(value, scratchRoot, stableRoot)
	}
	config.LabDir = stableRoot
	config.ResStartupConfig = rewrite(config.ResStartupConfig)
	for index := range config.Binds {
		config.Binds[index] = rewrite(config.Binds[index])
	}
	for name, value := range config.Env {
		config.Env[name] = rewrite(value)
	}
	config.Entrypoint = rewrite(config.Entrypoint)
	config.Cmd = rewrite(config.Cmd)
}

func decodeNodeDefinition(input NodeInput) (*clabtypes.NodeDefinition, error) {
	definition := &clabtypes.NodeDefinition{}
	if err := yaml.UnmarshalStrict(input.Definition, definition); err != nil {
		return nil, &Error{
			Code:     ErrorInvalidInput,
			NodeID:   input.ID,
			Field:    "definition",
			Behavior: "containerlab-vocabulary",
			Message:  "definition cannot be decoded by the imported containerlab type",
			cause:    err,
		}
	}
	if definition.Kind != "" && definition.Kind != input.Kind {
		return nil, &Error{
			Code:    ErrorInvalidInput,
			NodeID:  input.ID,
			Field:   "definition.kind",
			Message: "definition kind differs from normalized Node kind",
		}
	}
	if definition.Type != "" && input.Type != "" && definition.Type != input.Type {
		return nil, &Error{
			Code:    ErrorInvalidInput,
			NodeID:  input.ID,
			Field:   "definition.type",
			Message: "definition type differs from normalized Node type",
		}
	}
	if definition.Credentials.Password != "" {
		return nil, &Error{
			Code:     ErrorUnsupported,
			NodeID:   input.ID,
			Field:    "definition.credentials.password",
			Behavior: "secret-input",
			Message:  "password bytes must be supplied through a secret reference",
		}
	}

	definition.Kind = input.Kind
	if input.Type != "" {
		definition.Type = input.Type
	}

	return definition, nil
}

// rewriteExplicitPayloadPaths makes only explicitly declared, digest-verified payload mounts
// visible to imported hooks. The traversal is structural rather than field- or kind-specific: a
// future imported definition field that contains the declared path automatically gets the same
// controlled workspace semantics without a c9s source change.
func rewriteExplicitPayloadPaths(
	nodeID string,
	definition *clabtypes.NodeDefinition,
	payloads []PayloadInput,
	payloadRoot string,
) error {
	byDestination := map[string]PayloadInput{}
	for _, payload := range payloads {
		if payload.NodeID != nodeID {
			continue
		}
		destination := normalizedPayloadPath(payload.Destination)
		if destination == "" {
			continue
		}
		if existing, exists := byDestination[destination]; exists && existing.ID != payload.ID {
			return &Error{
				Code: ErrorInvalidInput, NodeID: nodeID, Field: "payloads.destination",
				Behavior: "payload-input",
				Message:  "multiple payload inputs declare the same normalized destination",
			}
		}
		byDestination[destination] = payload
	}
	if len(byDestination) == 0 {
		return nil
	}

	resolved := map[string]string{}
	rewrite := func(value string) (string, error) {
		payload, exists := byDestination[normalizedPayloadPath(value)]
		if !exists {
			return value, nil
		}
		if source := resolved[payload.ID]; source != "" {
			return source, nil
		}
		if payload.Kind != PayloadConfigMap && payload.Kind != PayloadSecret &&
			payload.Kind != PayloadURL {
			return "", &Error{
				Code: ErrorMissingInput, NodeID: nodeID, Field: "payloads",
				Behavior: "payload-workspace",
				Message:  "imported planning requires payload bytes that are not locally projected",
			}
		}
		root := filepath.Clean(payloadRoot)
		if !filepath.IsAbs(root) || root == string(filepath.Separator) {
			return "", &Error{
				Code: ErrorMissingInput, NodeID: nodeID, Field: "payloads",
				Behavior: "payload-workspace",
				Message:  "imported planning requires a scoped payload projection root",
			}
		}
		source := filepath.Join(root, ArtifactNodeDirectory(payload.ID), "source")
		info, err := os.Lstat(source)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", &Error{
				Code: ErrorMissingInput, NodeID: nodeID, Field: "payloads",
				Behavior: "payload-workspace",
				Message:  "explicit payload projection is unavailable or not a regular file",
				cause:    err,
			}
		}
		content, err := os.ReadFile(source) //nolint:gosec // Path is a scoped typed Pod projection.
		if err != nil {
			return "", planningError(
				ErrorSideEffect,
				"payloads",
				"cannot read explicit payload projection",
				err,
			)
		}
		if len(content) > maxPreparedPayloadBytes || Digest(content) != payload.Digest {
			return "", &Error{
				Code: ErrorInvariant, NodeID: nodeID, Field: "payloads",
				Behavior: "payload-workspace",
				Message:  "explicit payload projection differs from its accepted identity",
			}
		}
		resolved[payload.ID] = source

		return source, nil
	}

	return rewriteImportedStringLeaves(reflect.ValueOf(definition), rewrite)
}

func normalizedPayloadPath(value string) string {
	if value == "" || strings.ContainsRune(value, '\x00') {
		return ""
	}
	value = filepath.ToSlash(value)
	if !path.IsAbs(value) {
		value = "/" + value
	}

	return path.Clean(value)
}

func rewriteImportedStringLeaves(
	value reflect.Value,
	rewrite func(string) (string, error),
) error {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			return rewriteImportedStringLeaves(value.Elem(), rewrite)
		}
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		copy := reflect.New(value.Elem().Type()).Elem()
		copy.Set(value.Elem())
		if err := rewriteImportedStringLeaves(copy, rewrite); err != nil {
			return err
		}
		value.Set(copy)
	case reflect.Struct:
		for index := range value.NumField() {
			field := value.Field(index)
			if field.CanSet() {
				if err := rewriteImportedStringLeaves(field, rewrite); err != nil {
					return err
				}
			}
		}
	case reflect.Slice, reflect.Array:
		for index := range value.Len() {
			if err := rewriteImportedStringLeaves(value.Index(index), rewrite); err != nil {
				return err
			}
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			entry := iterator.Value()
			copy := reflect.New(entry.Type()).Elem()
			copy.Set(entry)
			if err := rewriteImportedStringLeaves(copy, rewrite); err != nil {
				return err
			}
			value.SetMapIndex(iterator.Key(), copy)
		}
	case reflect.String:
		if value.CanSet() {
			rewritten, err := rewrite(value.String())
			if err != nil {
				return err
			}
			value.SetString(rewritten)
		}
	}

	return nil
}

//nolint:funlen // This is the explicit, side-effect-free NodeDefinition-to-NodeConfig boundary.
func nodeConfigFromDefinition(
	input NodeInput,
	definition *clabtypes.NodeDefinition,
	entry *clabnodes.NodeRegistryEntry,
) (*clabtypes.NodeConfig, error) {
	privileged := entry.PrivilegedByDefault()
	if definition.Privileged != nil {
		privileged = *definition.Privileged
	}
	if definition.LinkApplyMode != "" && !definition.LinkApplyMode.IsValid() {
		return nil, &Error{
			Code:    ErrorInvalidInput,
			NodeID:  input.ID,
			Field:   "definition.link-apply-mode",
			Message: "link apply mode is invalid",
		}
	}

	config := &clabtypes.NodeConfig{
		ShortName:       input.Name,
		LongName:        input.Name,
		Fqdn:            input.Name,
		LabDir:          "/clabernetes/plan/" + input.ID,
		Kind:            input.Kind,
		NodeType:        definition.Type,
		Group:           definition.Group,
		StartupConfig:   definition.StartupConfig,
		StartupDelay:    definition.StartupDelay,
		RestartPolicy:   definition.RestartPolicy,
		LinkApplyMode:   definition.LinkApplyMode,
		Config:          definition.Config,
		Image:           definition.Image,
		ImagePullPolicy: clabtypes.ParsePullPolicyValue(definition.ImagePullPolicy),
		License:         definition.License,
		Position:        definition.Position,
		Entrypoint:      definition.Entrypoint,
		Cmd:             definition.Cmd,
		Exec:            slices.Clone(definition.Exec),
		Binds:           slices.Clone(definition.Binds),
		Devices:         slices.Clone(definition.Devices),
		CapAdd:          slices.Clone(definition.CapAdd),
		Privileged:      privileged,
		CgroupnsMode:    definition.CgroupnsMode,
		PidMode:         definition.PidMode,
		Tmpfs:           maps.Clone(definition.Tmpfs),
		SecurityOpts:    slices.Clone(definition.SecurityOpts),
		ShmSize:         definition.ShmSize,
		NetworkMode:     definition.NetworkMode,
		MgmtIPv4Address: definition.MgmtIPv4,
		MgmtIPv6Address: definition.MgmtIPv6,
		Env:             maps.Clone(definition.Env),
		User:            definition.User,
		Labels:          maps.Clone(definition.Labels),
		Runtime:         definition.Runtime,
		CPU:             definition.CPU,
		CPUSet:          definition.CPUSet,
		Memory:          definition.Memory,
		Sysctls:         maps.Clone(definition.Sysctls),
		Extras:          definition.Extras,
		Stages:          definition.Stages,
		DNS:             definition.DNS,
		Certificate:     definition.Certificate,
		Healthcheck:     definition.HealthCheck,
		Credentials:     definition.Credentials,
		Aliases:         slices.Clone(definition.Aliases),
		Components:      slices.Clone(definition.Components),
	}
	if definition.EnforceStartupConfig != nil {
		config.EnforceStartupConfig = *definition.EnforceStartupConfig
	}
	if definition.SuppressStartupConfig != nil {
		config.SuppressStartupConfig = *definition.SuppressStartupConfig
	}
	if definition.AutoRemove != nil {
		config.AutoRemove = *definition.AutoRemove
	}
	if config.Env == nil {
		config.Env = map[string]string{}
	}
	if config.Sysctls == nil {
		config.Sysctls = map[string]string{}
	}
	if config.Certificate == nil {
		config.Certificate = &clabtypes.CertificateConfig{}
	}
	if config.Certificate.Issue == nil {
		issue := false
		config.Certificate.Issue = &issue
	}
	if credentials := entry.GetCredentials(); credentials != nil {
		if config.Credentials.Username == "" {
			config.Credentials.Username = credentials.GetUsername()
		}
		if config.Credentials.Password == "" {
			config.Credentials.Password = credentials.GetPassword()
		}
	}

	return config, nil
}

func managementForNode(values []ManagementInput, nodeID string) *ManagementInput {
	for index := range values {
		if values[index].NodeID == nodeID {
			return &values[index]
		}
	}

	return nil
}

func applyManagementInput(config *clabtypes.NodeConfig, input *ManagementInput) {
	if input == nil {
		return
	}
	// The imported kind owns its management-interface default. An explicit controller allocation
	// may override it, but an omitted interface must not erase package-owned kind behavior.
	if input.InterfaceName != "" {
		config.MgmtIntf = input.InterfaceName
	}
	config.MgmtIPv4Address = input.IPv4
	config.MgmtIPv4Gateway = input.IPv4Gateway
	config.MgmtIPv6Address = input.IPv6
	config.MgmtIPv6Gateway = input.IPv6Gateway
}

func mapImageReferences(values map[string]string) []ImageReference {
	result := make([]ImageReference, 0, len(values))
	for role, reference := range values {
		result = append(result, ImageReference{Role: role, Reference: reference})
	}
	slices.SortFunc(result, func(left, right ImageReference) int {
		return strings.Compare(left.Role, right.Role)
	})

	return result
}

func validateImageInputs(nodeID string, images []ImageReference, inputs []ImageInput) error {
	provided := map[string]bool{}
	for _, image := range inputs {
		if image.NodeID == nodeID {
			provided[image.SourceReference] = true
			provided[image.DigestReference] = true
		}
	}
	for _, image := range images {
		if image.Reference != "" && !provided[image.Reference] {
			return &Error{
				Code:     ErrorMissingInput,
				NodeID:   nodeID,
				Field:    "images",
				Behavior: "image-metadata",
				Message: fmt.Sprintf(
					"explicit OCI metadata is missing for image role %q",
					image.Role,
				),
			}
		}
	}

	return nil
}

func interfacesForNode(values []InterfaceInput, nodeID string) []InterfaceInput {
	result := []InterfaceInput{}
	for _, value := range values {
		if value.NodeID == nodeID {
			result = append(result, value)
		}
	}

	return result
}

func mapLinkApplyMode(mode clabnodes.LinkApplyMode) LinkApplyMode {
	// This translates the package's generic lowercase lifecycle vocabulary into the versioned
	// plan schema. The default deliberately remains invalid so a future operation fails plan
	// validation instead of being silently weakened or reinterpreted as Recreate.
	switch mode {
	case clabnodes.LinkApplyModeLive:
		return LinkApplyLive
	case clabnodes.LinkApplyModeRestart:
		return LinkApplyRestart
	case clabnodes.LinkApplyModeRecreate:
		return LinkApplyRecreate
	default:
		return LinkApplyMode(mode)
	}
}

func withNodeID(err error, nodeID string) error {
	var planningErr *Error
	if !errors.As(err, &planningErr) {
		return err
	}
	copy := *planningErr
	copy.NodeID = nodeID

	return &copy
}
