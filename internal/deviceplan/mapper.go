//nolint:err113,funlen,gocognit,gocyclo,mnd,nlreturn,wsl_v5 // Runtime-neutral mapping uses compact fail-closed guards.
package deviceplan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/google/shlex"
	clabtypes "github.com/srl-labs/containerlab/types"
)

// Plan evaluates imported node behavior and maps generic results into the c9s-owned schema.
func (a Adapter) Plan(ctx context.Context, input Input) (*Plan, error) {
	evaluation, err := a.Evaluate(ctx, input)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		SchemaVersion: SchemaVersion,
		Compatibility: evaluation.Input.Compatibility,
		InputDigest:   evaluation.InputDigest,
		Planner: PlannerIdentity{
			Name:     "clabernetes",
			Revision: a.Revision,
		},
	}
	containerIDs, runtimeContainerIDs, err := indexRecordedContainers(evaluation.Nodes)
	if err != nil {
		return nil, err
	}
	for index := range evaluation.Nodes {
		if err = appendEvaluatedNode(
			plan,
			&evaluation.Nodes[index],
			evaluation.Input,
			containerIDs,
			runtimeContainerIDs,
		); err != nil {
			return nil, err
		}
	}

	normalized, err := NormalizePlan(*plan)
	if err != nil {
		return nil, err
	}

	return &normalized, nil
}

func indexRecordedContainers(
	nodes []EvaluatedNode,
) (map[string][]string, map[string]string, error) {
	byNode := make(map[string][]string, len(nodes))
	byRuntimeID := map[string]string{}
	for nodeIndex := range nodes {
		node := &nodes[nodeIndex]
		if err := validateRecordedContainers(node); err != nil {
			return nil, nil, err
		}
		for containerIndex, recorded := range node.Containers {
			containerID := node.Input.ID + "/primary"
			if len(node.Containers) > 1 {
				containerID = node.Input.ID + "/container/" + shortDigest(recorded.RuntimeID)
			}
			if prior, exists := byRuntimeID[recorded.RuntimeID]; exists {
				return nil, nil, &Error{
					Code: ErrorInvariant, NodeID: node.Input.ID,
					Field: "deployment.containers[" + strconv.Itoa(
						containerIndex,
					) + "].runtimeID",
					Behavior: "components",
					Message:  "imported runtime container identity conflicts with " + prior,
				}
			}
			byNode[node.Input.ID] = append(byNode[node.Input.ID], containerID)
			byRuntimeID[recorded.RuntimeID] = containerID
		}
	}

	return byNode, byRuntimeID, nil
}

func appendEvaluatedNode(
	plan *Plan,
	node *EvaluatedNode,
	input Input,
	containerIDs map[string][]string,
	runtimeContainerIDs map[string]string,
) error {
	if err := validateMappedDefinition(node, input); err != nil {
		return err
	}
	nodeContainerIDs := containerIDs[node.Input.ID]
	readinessContainerIDs, err := mapReadinessContainerIDs(
		node,
		nodeContainerIDs,
		runtimeContainerIDs,
	)
	if err != nil {
		return err
	}
	plan.Nodes = append(plan.Nodes, NodePlan{
		ID:                    node.Input.ID,
		Name:                  node.Input.Name,
		Kind:                  node.Input.Kind,
		Group:                 node.Config.Group,
		Position:              node.Config.Position,
		Aliases:               slices.Clone(node.Config.Aliases),
		ContainerIDs:          slices.Clone(nodeContainerIDs),
		ReadinessContainerIDs: readinessContainerIDs,
	})
	for index, recorded := range node.Containers {
		containerID := nodeContainerIDs[index]
		namespaceOwnerID, err := namespaceOwnerForContainer(
			node,
			recorded,
			containerID,
			containerIDs,
			runtimeContainerIDs,
		)
		if err != nil {
			return err
		}
		image, err := imageInputForContainer(input.Images, node.Input.ID, recorded.Config.Image)
		if err != nil {
			return err
		}
		componentID := image.ComponentID
		if componentID == "" && len(node.Containers) > 1 {
			componentID = recorded.RuntimeID
		}
		container, err := mapContainer(
			node,
			recorded.Config,
			image,
			containerID,
			recorded.RuntimeID,
			componentID,
			namespaceOwnerID,
		)
		if err != nil {
			return err
		}
		plan.Containers = append(plan.Containers, container)
		if err = appendStoragePlans(plan, node, recorded.Config,
			containerID, index == 0, input.Payloads); err != nil {
			return err
		}
	}
	// Imported lifecycle hooks execute against the runtime identity the package itself declares
	// through GetContainerName (a component kind routes execs to a specific member container),
	// so their actions must run inside the container carrying that identity: an
	// application-local exec from the wrong sibling cannot reach the declared target's
	// processes.
	primaryContainerID := nodeContainerIDs[0]
	if named, ok := node.implementation.(interface{ GetContainerName() string }); ok {
		declaredRuntimeID := named.GetContainerName()
		for index, recorded := range node.Containers {
			if declaredRuntimeID != "" && recorded.RuntimeID == declaredRuntimeID {
				primaryContainerID = nodeContainerIDs[index]

				break
			}
		}
	}
	// Every imported lifecycle is rehydrated with a package LabDir, even when the package emitted
	// no preparation artifact. Keep that runtime-owned directory generic and plan-scoped rather
	// than making its existence depend on kind-specific file generation.
	ensureArtifactsVolume(plan, node.Input.ID)
	artifactFileIDs := appendGeneratedArtifactPlans(plan, node)
	appendPayloadPlans(plan, node, input.Payloads, primaryContainerID)
	recordedActionCount, err := appendRecordedDeploymentActions(
		plan,
		node,
		runtimeContainerIDs,
		artifactFileIDs,
		primaryContainerID,
	)
	if err != nil {
		return err
	}
	plan.Actions = append(plan.Actions, Action{
		ID: "imported-post-deploy/" + node.Input.ID, Phase: PhasePostStart,
		Order: recordedActionCount,
		Target: ActionTarget{
			NodeID: node.Input.ID, ContainerID: primaryContainerID,
			NamespaceOwnerID: containerNamespaceOwner(plan, primaryContainerID),
		},
		Kind:               ActionImportedPostDeploy,
		ImportedPostDeploy: &ImportedPostDeployAction{},
	})
	if err := appendExecActions(
		plan,
		node,
		primaryContainerID,
		recordedActionCount+1,
	); err != nil {
		return err
	}
	appendManagementPlan(plan, node, input.Management)
	appendInterfacePlans(
		plan,
		node,
		primaryContainerID,
		containerNamespaceOwner(plan, primaryContainerID),
	)
	//nolint:gocritic // one append per planned element reads clearest.
	plan.Actions = append(
		plan.Actions,
		Action{ //nolint:gocritic // one append per planned action stage reads clearest.
			ID:    "imported-deploy-endpoints/" + node.Input.ID,
			Phase: PhaseInterfaceFixup,
			Target: ActionTarget{
				NodeID: node.Input.ID, ContainerID: primaryContainerID,
				NamespaceOwnerID: containerNamespaceOwner(plan, primaryContainerID),
			},
			Kind:                    ActionImportedDeployEndpoints,
			ImportedDeployEndpoints: &ImportedDeployEndpointsAction{},
		},
	)
	plan.Actions = append(plan.Actions, Action{
		ID:    "imported-readiness/" + node.Input.ID,
		Phase: PhaseReadiness,
		Target: ActionTarget{
			NodeID: node.Input.ID, ContainerID: primaryContainerID,
			NamespaceOwnerID: containerNamespaceOwner(plan, primaryContainerID),
		},
		Kind:              ActionImportedReadiness,
		ImportedReadiness: &ImportedReadinessAction{},
	})
	plan.Actions = append(plan.Actions, Action{
		ID:    "imported-save/" + node.Input.ID,
		Phase: PhaseSave,
		Target: ActionTarget{
			NodeID: node.Input.ID, ContainerID: primaryContainerID,
			NamespaceOwnerID: containerNamespaceOwner(plan, primaryContainerID),
		},
		Kind: ActionSave,
		Save: &SaveAction{Method: SaveMethodImported},
	})

	return nil
}

func mapReadinessContainerIDs(
	node *EvaluatedNode,
	nodeContainerIDs []string,
	runtimeContainerIDs map[string]string,
) ([]string, error) {
	result := make([]string, 0, len(node.ReadinessRuntimeIDs))
	seen := make(map[string]bool, len(node.ReadinessRuntimeIDs))
	for _, runtimeID := range node.ReadinessRuntimeIDs {
		containerID, exists := runtimeContainerIDs[runtimeID]
		if !exists || !slices.Contains(nodeContainerIDs, containerID) || seen[containerID] {
			return nil, &Error{
				Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.components",
				Behavior: "imported-components",
				Message:  "imported component inventory does not match recorded containers",
			}
		}
		seen[containerID] = true
		result = append(result, containerID)
	}
	if len(result) == 0 {
		return nil, &Error{
			Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.components",
			Behavior: "imported-components",
			Message:  "imported component inventory has no readiness-owning container",
		}
	}

	return result, nil
}

func appendGeneratedArtifactPlans(plan *Plan, node *EvaluatedNode) map[string]string {
	fileIDs := make(map[string]string, len(node.GeneratedArtifacts))
	if len(node.GeneratedArtifacts) == 0 {
		return fileIDs
	}
	ensureArtifactsVolume(plan, node.Input.ID)
	for index, artifact := range node.GeneratedArtifacts {
		fileID := fmt.Sprintf("generated/%s/%d", node.Input.ID, index)
		fileIDs[artifact.Path] = fileID
		sourceKind := FileSourceGenerator
		if node.CertificateBacked {
			sourceKind = FileSourceCertificate
		}
		plan.Files = append(plan.Files, FilePlan{
			ID:              fileID,
			NodeID:          node.Input.ID,
			SourceKind:      sourceKind,
			ArtifactKind:    artifact.Kind,
			SourceReference: artifact.SourceReference,
			Digest:          artifact.Digest,
			ArtifactPath:    artifact.Path,
			LinkTarget:      artifact.LinkTarget,
			Mode:            artifact.Mode,
			UID:             artifact.UID,
			GID:             artifact.GID,
			ExtendedAttributes: mapGeneratedExtendedAttributes(
				artifact.ExtendedAttributes,
			),
			Sensitive: true,
		})
		plan.Actions = append(plan.Actions, Action{
			ID:     "prepare/" + fileID,
			Phase:  PhasePrepare,
			Order:  index,
			Target: ActionTarget{NodeID: node.Input.ID},
			Kind:   ActionFile,
			File:   &FileAction{FileID: fileID},
		})
	}

	return fileIDs
}

func mapGeneratedExtendedAttributes(
	attributes []generatedExtendedAttribute,
) []ExtendedAttribute {
	if len(attributes) == 0 {
		return nil
	}
	result := make([]ExtendedAttribute, 0, len(attributes))
	for _, attribute := range attributes {
		result = append(result, ExtendedAttribute{
			Name: attribute.Name, Digest: attribute.Digest,
		})
	}

	return result
}

func appendRecordedDeploymentActions(
	plan *Plan,
	node *EvaluatedNode,
	runtimeContainerIDs map[string]string,
	artifactFileIDs map[string]string,
	lifecycleContainerID string,
) (int, error) {
	if node.recorder == nil {
		return 0, &Error{
			Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.operations",
			Behavior: "imported-deploy", Message: "imported deployment recorder is unavailable",
		}
	}
	execs := node.recorder.Execs()
	copies := node.recorder.Copies()
	stdins := node.recorder.Stdins()
	operationCount := len(execs) + len(copies) + len(stdins)
	orders := make(map[int]bool, operationCount)
	appendOrder := func(order int, behavior string) error {
		if order < 0 || order >= operationCount || orders[order] {
			return &Error{
				Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.operations.order",
				Behavior: behavior, Message: "imported deployment operation order is invalid",
			}
		}
		orders[order] = true

		return nil
	}
	target := func(runtimeID, behavior string) (ActionTarget, error) {
		containerID, exists := runtimeContainerIDs[runtimeID]
		if !exists {
			return ActionTarget{}, &Error{
				Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.operations.target",
				Behavior: behavior,
				Message:  "imported deployment operation targets an unknown container",
			}
		}
		for _, container := range plan.Containers {
			if container.ID != containerID {
				continue
			}
			if container.NodeID != node.Input.ID {
				return ActionTarget{}, &Error{
					Code: ErrorInvariant, NodeID: node.Input.ID,
					Field: "deployment.operations.target", Behavior: behavior,
					Message: "imported deployment operation crosses logical Node ownership",
				}
			}
			if containerID != lifecycleContainerID {
				return ActionTarget{}, &Error{
					Code: ErrorUnsupported, NodeID: node.Input.ID,
					Field: "deployment.operations.target", Behavior: "cross-container-lifecycle",
					Message: "ordered imported deployment operations span application containers",
				}
			}

			return ActionTarget{
				NodeID: node.Input.ID, ContainerID: containerID,
				NamespaceOwnerID: container.NamespaceOwnerID,
			}, nil
		}

		return ActionTarget{}, &Error{
			Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.operations.target",
			Behavior: behavior, Message: "imported deployment target has no planned container",
		}
	}
	for _, recorded := range execs {
		behavior := "runtime.ExecNotWait"
		if recorded.Wait {
			behavior = "runtime.Exec"
		}
		if err := appendOrder(recorded.Order, behavior); err != nil {
			return 0, err
		}
		actionTarget, err := target(recorded.RuntimeID, behavior)
		if err != nil {
			return 0, err
		}
		plan.Actions = append(plan.Actions, Action{
			ID:    fmt.Sprintf("imported-deploy-exec/%s/%06d", node.Input.ID, recorded.Order),
			Phase: PhasePostStart, Order: recorded.Order, Target: actionTarget,
			Kind: ActionExec,
			Exec: &ExecAction{Command: slices.Clone(recorded.Command), Wait: recorded.Wait},
		})
	}
	for _, recorded := range copies {
		const behavior = "runtime.CopyToContainer"
		if err := appendOrder(recorded.Order, behavior); err != nil {
			return 0, err
		}
		actionTarget, err := target(recorded.RuntimeID, behavior)
		if err != nil {
			return 0, err
		}
		fileID := artifactFileIDs[recorded.ArtifactPath]
		if fileID == "" {
			return 0, &Error{
				Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.operations.copy",
				Behavior: behavior, Message: "recorded copy has no generated artifact",
			}
		}
		plan.Actions = append(plan.Actions, Action{
			ID:    fmt.Sprintf("imported-deploy-copy/%s/%06d", node.Input.ID, recorded.Order),
			Phase: PhasePostStart, Order: recorded.Order, Target: actionTarget,
			Kind: ActionFile,
			File: &FileAction{
				FileID: fileID, Destination: recorded.Destination, WriteMode: recorded.WriteMode,
			},
		})
	}
	for _, recorded := range stdins {
		const behavior = "runtime.WriteToStdinNoWait"
		if err := appendOrder(recorded.Order, behavior); err != nil {
			return 0, err
		}
		actionTarget, err := target(recorded.RuntimeID, behavior)
		if err != nil {
			return 0, err
		}
		fileID := artifactFileIDs[recorded.ArtifactPath]
		if fileID == "" {
			return 0, &Error{
				Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.operations.stdin",
				Behavior: behavior, Message: "recorded stdin has no generated artifact",
			}
		}
		plan.Actions = append(plan.Actions, Action{
			ID:    fmt.Sprintf("imported-deploy-stdin/%s/%06d", node.Input.ID, recorded.Order),
			Phase: PhasePostStart, Order: recorded.Order, Target: actionTarget,
			Kind:       ActionWriteStdin,
			WriteStdin: &WriteStdinAction{FileID: fileID},
		})
	}
	if len(orders) != operationCount {
		return 0, &Error{
			Code: ErrorInvariant, NodeID: node.Input.ID, Field: "deployment.operations.order",
			Behavior: "imported-deploy",
			Message:  "imported deployment operation order is incomplete",
		}
	}

	return operationCount, nil
}

func containerNamespaceOwner(plan *Plan, containerID string) string {
	for _, container := range plan.Containers {
		if container.ID == containerID {
			return container.NamespaceOwnerID
		}
	}

	return containerID
}

func validateRecordedContainers(node *EvaluatedNode) error {
	if len(node.Containers) == 0 {
		return &Error{
			Code: ErrorUnsupported, NodeID: node.Input.ID, Field: "deployment.containers",
			Behavior: "containerless-node",
			Message:  "imported deployment recorded no application container",
		}
	}
	for index, container := range node.Containers {
		if container.Config == nil || !container.Started {
			return &Error{
				Code: ErrorInvariant, NodeID: node.Input.ID,
				Field:    "deployment.containers[" + strconv.Itoa(index) + "]",
				Behavior: "container-lifecycle",
				Message:  "imported deployment did not create and start a complete container",
			}
		}
	}

	return nil
}

func namespaceOwnerForContainer(
	node *EvaluatedNode,
	recorded RecordedContainer,
	containerID string,
	containerIDs map[string][]string,
	runtimeContainerIDs map[string]string,
) (string, error) {
	if target, shared := strings.CutPrefix(recorded.Config.NetworkMode, "container:"); shared {
		ownerID, exists := runtimeContainerIDs[target]
		if !exists {
			return "", &Error{
				Code: ErrorMissingInput, NodeID: node.Input.ID, Field: "deployment.network-mode",
				Behavior: "network-namespace",
				Message:  "imported container references an unknown namespace owner",
			}
		}

		return ownerID, nil
	}
	if node.Input.GroupOwner != "" {
		owners := containerIDs[node.Input.GroupOwner]
		if len(owners) == 0 {
			return "", &Error{
				Code: ErrorMissingInput, NodeID: node.Input.ID, Field: "groupOwner",
				Behavior: "network-namespace", Message: "group owner has no application container",
			}
		}

		return owners[0], nil
	}

	return containerID, nil
}

func validateMappedDefinition(node *EvaluatedNode, input Input) error {
	definition := node.definition
	management := managementForNode(input.Management, node.Input.ID)
	if err := validateDefinitionManagement(node, management); err != nil {
		return err
	}
	if err := validateDefinitionNetworkMode(node, input.Nodes); err != nil {
		return err
	}
	unsupported := []struct {
		present  bool
		field    string
		behavior string
	}{
		{definition.Config != nil, "definition.config", "config-engine"},
		{len(definition.EnvFiles) != 0, "definition.env-files", "environment-file"},
		{definition.Runtime != "", "definition.runtime", "container-runtime"},
		{definition.Stages != nil, "definition.stages", "deployment-stages"},
		{
			definition.Credentials.Username != "" || definition.Credentials.IdentityFile != "",
			"definition.credentials", "credentials",
		},
	}
	for _, item := range unsupported {
		if item.present {
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: item.field,
				Behavior: item.behavior,
				Message:  "input has no direct-Pod plan mapping",
			}
		}
	}

	return nil
}

func validateDefinitionManagement(node *EvaluatedNode, management *ManagementInput) error {
	definition := node.definition
	if definition.MgmtIPv4 == "" && definition.MgmtIPv6 == "" {
		return nil
	}
	if management == nil ||
		!definitionManagementAddressMatches(definition.MgmtIPv4, management.IPv4) ||
		!definitionManagementAddressMatches(definition.MgmtIPv6, management.IPv6) {
		return &Error{
			Code: ErrorMissingInput, NodeID: node.Input.ID, Field: "definition.management",
			Behavior: "management-allocation",
			Message:  "definition management addresses require matching controller allocation",
		}
	}

	return nil
}

// definitionManagementAddressMatches accepts a controller allocation for one definition
// management address: containerlab definitions declare bare addresses while allocations carry
// the policy prefix, so a bare definition matches on the address part alone and a prefixed
// definition must match the allocation exactly.
func definitionManagementAddressMatches(definitionValue, allocated string) bool {
	if definitionValue == "" {
		return true
	}

	if strings.Contains(definitionValue, "/") {
		return definitionValue == allocated
	}

	allocatedAddress, _ := splitManagementAddress(allocated)

	return definitionValue == allocatedAddress
}

func validateDefinitionNetworkMode(node *EvaluatedNode, nodes []NodeInput) error {
	if node.definition.NetworkMode == "" {
		return nil
	}
	if node.Input.GroupOwner == "" {
		return &Error{
			Code: ErrorMissingInput, NodeID: node.Input.ID, Field: "definition.network-mode",
			Behavior: "network-namespace",
			Message:  "shared network namespace has no resolved group owner",
		}
	}
	for _, candidate := range nodes {
		if candidate.ID == node.Input.GroupOwner &&
			node.definition.NetworkMode == "container:"+candidate.Name {
			return nil
		}
	}

	return &Error{
		Code: ErrorInvariant, NodeID: node.Input.ID, Field: "definition.network-mode",
		Behavior: "network-namespace",
		Message:  "shared network namespace differs from the resolved group owner",
	}
}

func mapContainer(
	node *EvaluatedNode,
	config *clabtypes.NodeConfig,
	image ImageInput,
	containerID,
	runtimeID,
	componentID,
	namespaceOwnerID string,
) (ContainerPlan, error) {
	if config.AutoRemove {
		return ContainerPlan{}, nodeMappingError(
			node.Input.ID,
			"definition.auto-remove",
			"automatic removal has no direct Pod semantic",
		)
	}
	if config.PidMode != "" || config.CgroupnsMode != "" {
		return ContainerPlan{}, nodeMappingError(
			node.Input.ID,
			"definition",
			"PID or cgroup namespace mode requires an explicit direct-Pod mapping",
		)
	}
	entrypoint, err := splitOverride(config.Entrypoint, "definition.entrypoint", node.Input.ID)
	if err != nil {
		return ContainerPlan{}, err
	}
	command, err := splitOverride(config.Cmd, "definition.cmd", node.Input.ID)
	if err != nil {
		return ContainerPlan{}, err
	}
	security, err := mapSecurity(node.Input.ID, config)
	if err != nil {
		return ContainerPlan{}, err
	}
	ports, err := mapPorts(node.definition.Ports, image.Config.Ports, node.Input.ID)
	if err != nil {
		return ContainerPlan{}, err
	}

	// The recorded configuration already carries every package-owned variable: the imported
	// Deploy hook sets its own interface count per container, including per-component values
	// this mapper must not clobber.
	environment := maps.Clone(config.Env)

	memoryLimit, err := formatMemory(config.Memory)
	if err != nil {
		return ContainerPlan{}, nodeMappingError(
			node.Input.ID,
			"definition.memory",
			"memory limit is not a parseable size",
		)
	}

	return ContainerPlan{
		ID:               containerID,
		NodeID:           node.Input.ID,
		RuntimeID:        runtimeID,
		ComponentID:      componentID,
		NamespaceOwnerID: namespaceOwnerID,
		Image:            config.Image,
		ImageDigest:      digestFromReference(image.DigestReference),
		ImagePullPolicy:  string(config.ImagePullPolicy),
		ImagePullPolicyExplicit: strings.TrimSpace(
			node.definition.ImagePullPolicy,
		) != "",
		ImageEntrypoint: slices.Clone(image.Config.Entrypoint),
		ImageCommand:    slices.Clone(image.Config.Command),
		Entrypoint:      entrypoint,
		Command:         command,
		Environment:     mapKeyValues(environment),
		Labels:          mapKeyValues(config.Labels),
		User:            config.User,
		WorkingDir:      image.Config.WorkingDir,
		Ports:           ports,
		StopSignal:      image.Config.StopSignal,
		RestartPolicy:   config.RestartPolicy,
		StartupDelay:    config.StartupDelay,
		TTY:             true,
		Stdin:           true,
		Security:        security,
		Resources: ResourcePlan{
			CPULimit:    formatCPU(config.CPU),
			MemoryLimit: memoryLimit,
			CPUSet:      config.CPUSet,
		},
		DNS:         mapDNS(config.DNS),
		Healthcheck: mapHealthcheck(config.Healthcheck, image.Config.Healthcheck),
		Required:    true,
	}, nil
}

func mapSecurity(nodeID string, config *clabtypes.NodeConfig) (SecurityPlan, error) {
	security := SecurityPlan{
		Privileged:      config.Privileged,
		CapabilitiesAdd: slices.Clone(config.CapAdd),
		Sysctls:         mapKeyValues(config.Sysctls),
	}
	for _, raw := range config.Devices {
		parts := strings.Split(raw, ":")
		device := Device{HostPath: parts[0], ContainerPath: parts[0], Permissions: "rwm"}
		switch len(parts) {
		case 1:
		case 2:
			device.ContainerPath = parts[1]
		case 3:
			device.ContainerPath = parts[1]
			device.Permissions = parts[2]
		default:
			return SecurityPlan{}, nodeMappingError(
				nodeID,
				"definition.devices",
				"device mapping is not representable",
			)
		}
		if device.HostPath == "" || device.ContainerPath == "" {
			return SecurityPlan{}, nodeMappingError(
				nodeID,
				"definition.devices",
				"device mapping is incomplete",
			)
		}
		security.Devices = append(security.Devices, device)
	}
	for _, option := range config.SecurityOpts {
		key, value, found := strings.Cut(option, "=")
		if !found || value == "" {
			return SecurityPlan{}, nodeMappingError(
				nodeID,
				"definition.security-opts",
				"security option is not portable",
			)
		}
		switch strings.ToLower(key) {
		case "seccomp":
			security.SeccompProfile = value
		case "apparmor":
			security.AppArmorProfile = value
		default:
			return SecurityPlan{}, nodeMappingError(
				nodeID,
				"definition.security-opts",
				"security option is not portable",
			)
		}
	}

	return security, nil
}

func appendPayloadPlans(
	plan *Plan,
	node *EvaluatedNode,
	payloads []PayloadInput,
	containerID string,
) {
	for _, payload := range payloads {
		if payload.NodeID != node.Input.ID {
			continue
		}
		volumeID := ensureArtifactsVolume(plan, node.Input.ID)
		fileID := "file/" + payload.ID
		mountID := "mount/" + payload.ID
		plan.Files = append(plan.Files, FilePlan{
			ID: fileID, NodeID: node.Input.ID,
			SourceKind: FileSourcePayload, ArtifactKind: ArtifactRegular,
			SourceReference: payload.ID,
			Digest:          payload.Digest, ArtifactPath: "payloads/" + payload.ID,
			Destination: payload.Destination,
			Mode:        payload.Mode, Sensitive: payload.Sensitive,
		})
		plan.Mounts = append(plan.Mounts, MountPlan{
			ID:          mountID,
			ContainerID: containerID,
			VolumeID:    volumeID,
			SourcePath:  "payloads/" + payload.ID,
			Destination: payload.Destination,
			ReadOnly:    true,
		})
		appendContainerMountID(plan, containerID, mountID)
		plan.Actions = append(plan.Actions, Action{
			ID: "prepare/" + payload.ID, Phase: PhasePrepare,
			Target: ActionTarget{NodeID: node.Input.ID},
			Kind:   ActionFile,
			File:   &FileAction{FileID: fileID},
		})
	}
}

func appendStoragePlans(
	plan *Plan,
	node *EvaluatedNode,
	config *clabtypes.NodeConfig,
	containerID string,
	includeDefinitionBinds bool,
	payloads []PayloadInput,
) error {
	definitionBinds := []string(nil)
	if includeDefinitionBinds {
		definitionBinds = node.definition.Binds
	}
	if len(config.Binds) < len(definitionBinds) ||
		!slices.Equal(config.Binds[:len(definitionBinds)], definitionBinds) {
		return &Error{
			Code: ErrorInvariant, NodeID: node.Input.ID, Field: "definition.binds",
			Behavior: "imported-init", Message: "kind initializer rewrote user bind intent",
		}
	}
	for index, raw := range definitionBinds {
		if err := appendUserBindPlan(plan, node, payloads, containerID, index, raw); err != nil {
			return err
		}
	}
	for index, raw := range config.Binds[len(definitionBinds):] {
		if err := appendImportedBindPlan(plan, node, config, containerID, index, raw); err != nil {
			return err
		}
	}
	for destination, options := range config.Tmpfs {
		size, mountOptions, readOnly, requiresRuntimeMount, err := normalizeTmpfsOptions(options)
		if err != nil {
			return nodeMappingError(
				node.Input.ID,
				"kind.tmpfs",
				fmt.Sprintf("tmpfs %q has invalid options: %v", destination, err),
			)
		}
		volumeID := containerID + "/tmpfs/" + shortDigest(destination)
		mountID := volumeID + "/mount"
		plan.Volumes = append(plan.Volumes, VolumePlan{
			ID: volumeID, NodeID: node.Input.ID, Kind: VolumeEmptyDir,
			Medium: "Memory", Size: size,
		})
		plan.Mounts = append(plan.Mounts, MountPlan{
			ID: mountID, ContainerID: containerID, VolumeID: volumeID,
			Destination: destination, ReadOnly: readOnly,
		})
		appendContainerMountID(plan, containerID, mountID)
		if requiresRuntimeMount {
			plan.Actions = append(plan.Actions, Action{
				ID: "pre-start-mount/" + mountID, Phase: PhasePreStart,
				Target: ActionTarget{NodeID: node.Input.ID, ContainerID: containerID},
				Kind:   ActionMount,
				Mount: &MountAction{
					MountID: mountID, Filesystem: "tmpfs", Source: "tmpfs", Options: mountOptions,
				},
			})
		}
	}
	if config.ShmSize != "" {
		shmBytes, err := humanize.ParseBytes(config.ShmSize)
		if err != nil || shmBytes == 0 {
			return nodeMappingError(
				node.Input.ID,
				"kind.shm-size",
				"shared-memory size is not a positive byte quantity",
			)
		}
		size := strconv.FormatUint(shmBytes, 10)
		volumeID := containerID + "/shm"
		mountID := volumeID + "/mount"
		plan.Volumes = append(plan.Volumes, VolumePlan{
			ID: volumeID, NodeID: node.Input.ID, Kind: VolumeEmptyDir,
			Medium: "Memory", Size: size,
		})
		plan.Mounts = append(plan.Mounts, MountPlan{
			ID: mountID, ContainerID: containerID, VolumeID: volumeID, Destination: "/dev/shm",
		})
		appendContainerMountID(plan, containerID, mountID)
	}

	return nil
}

// appendUserBindPlan realizes one user-declared definition bind from its explicit payload
// backing: the bind source must be covered by declared payload files (exactly, or as a
// directory prefix), and each backing file is mounted read-only at the bind target. This keeps
// containerlab bind semantics portable — file content comes from explicit c9s inputs, never
// from a host filesystem.
func appendUserBindPlan(
	plan *Plan,
	node *EvaluatedNode,
	payloads []PayloadInput,
	containerID string,
	index int,
	raw string,
) error {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" ||
		!path.IsAbs(parts[1]) {
		return nodeMappingError(
			node.Input.ID,
			"definition.binds",
			"user bind is not representable",
		)
	}
	if len(parts) == 3 {
		for option := range strings.SplitSeq(parts[2], ",") {
			switch option {
			case "", "ro", "rw":
			default:
				return nodeMappingError(
					node.Input.ID,
					"definition.binds",
					"user bind option is not representable",
				)
			}
		}
	}
	source := path.Clean(parts[0])
	if !path.IsAbs(source) {
		// Containerlab resolves relative bind sources against the topology directory; payload
		// destinations carry the same topology-relative identity rooted at "/".
		source = "/" + source
	}
	target := path.Clean(parts[1])
	volumeID := ensureArtifactsVolume(plan, node.Input.ID)
	matched := false
	for _, payload := range payloads {
		if payload.NodeID != node.Input.ID {
			continue
		}
		var destination string

		switch {
		case payload.Destination == source:
			destination = target
		case strings.HasPrefix(payload.Destination, source+"/"):
			destination = target + strings.TrimPrefix(payload.Destination, source)
		default:
			continue
		}
		matched = true
		mountID := fmt.Sprintf("mount/bind/%d/%s", index, payload.ID)
		plan.Mounts = append(plan.Mounts, MountPlan{
			ID:          mountID,
			ContainerID: containerID,
			VolumeID:    volumeID,
			SourcePath:  "payloads/" + payload.ID,
			Destination: destination,
			ReadOnly:    true,
		})
		appendContainerMountID(plan, containerID, mountID)
	}
	if !matched {
		return nodeMappingError(
			node.Input.ID,
			"definition.binds",
			"user bind mapping requires an explicit c9s volume or payload",
		)
	}

	return nil
}

func appendImportedBindPlan(
	plan *Plan,
	node *EvaluatedNode,
	config *clabtypes.NodeConfig,
	containerID string,
	index int,
	raw string,
) error {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || !path.IsAbs(parts[0]) ||
		parts[1] == "" || !path.IsAbs(parts[1]) {
		return nodeMappingError(node.Input.ID, "kind.binds", "imported bind is not representable")
	}

	source, destination := path.Clean(parts[0]), path.Clean(parts[1])
	readOnly := false
	propagation := ""
	if len(parts) == 3 {
		for option := range strings.SplitSeq(parts[2], ",") {
			switch option {
			case "", "rw":
			case "ro":
				readOnly = true
			case "private", "rprivate", "shared", "rshared", "slave", "rslave":
				propagation = option
			default:
				return nodeMappingError(
					node.Input.ID,
					"kind.binds",
					"imported bind option is not representable",
				)
			}
		}
	}

	var volumeID string

	sourcePath := ""
	labDir := path.Clean(config.LabDir)
	if source == labDir || strings.HasPrefix(source, labDir+"/") {
		volumeID = ensureArtifactsVolume(plan, node.Input.ID)
		if source != labDir {
			sourcePath = strings.TrimPrefix(source, labDir+"/")
		}
	} else {
		volumeID = node.Input.ID + "/host/" + shortDigest(source)
		ensureVolume(plan, VolumePlan{
			ID: volumeID, NodeID: node.Input.ID, Kind: VolumeDevice, Reference: source,
		})
	}

	mountID := fmt.Sprintf("%s/imported-bind/%d", containerID, index)
	plan.Mounts = append(plan.Mounts, MountPlan{
		ID: mountID, ContainerID: containerID, VolumeID: volumeID,
		SourcePath: sourcePath, Destination: destination, ReadOnly: readOnly,
		Propagation: propagation,
	})
	appendContainerMountID(plan, containerID, mountID)

	return nil
}

func appendContainerMountID(plan *Plan, containerID, mountID string) {
	for index := range plan.Containers {
		if plan.Containers[index].ID == containerID {
			plan.Containers[index].MountIDs = append(plan.Containers[index].MountIDs, mountID)

			return
		}
	}
}

func ensureArtifactsVolume(plan *Plan, nodeID string) string {
	volumeID := nodeID + "/artifacts"
	ensureVolume(plan, VolumePlan{ID: volumeID, NodeID: nodeID, Kind: VolumeArtifacts})

	return volumeID
}

func ensureVolume(plan *Plan, candidate VolumePlan) {
	for _, volume := range plan.Volumes {
		if volume.ID == candidate.ID {
			return
		}
	}
	plan.Volumes = append(plan.Volumes, candidate)
}

func appendExecActions(
	plan *Plan,
	node *EvaluatedNode,
	containerID string,
	baseOrder int,
) error {
	for index, raw := range node.Config.Exec {
		command, err := shlex.Split(raw)
		if err != nil || len(command) == 0 {
			return nodeMappingError(node.Input.ID, "definition.exec", "exec command is invalid")
		}
		plan.Actions = append(plan.Actions, Action{
			ID:    "post-start/" + node.Input.ID + "/" + strconv.Itoa(index),
			Phase: PhasePostStart,
			Order: baseOrder + index,
			Target: ActionTarget{
				NodeID: node.Input.ID, ContainerID: containerID,
				NamespaceOwnerID: containerNamespaceOwner(plan, containerID),
			},
			Kind: ActionExec,
			Exec: &ExecAction{Command: command, Wait: true},
		})
	}

	return nil
}

// defaultManagementDeviceInterface is containerlab's primary-interface contract: every kind
// consumes this name as its management port unless its evaluated configuration names another.
const defaultManagementDeviceInterface = "eth0"

// appendManagementPlan records one management entry per logical Node even when the controller
// allocated no addresses: the package-declared management interface must ride the plan so
// runtime completion can rehydrate it after planning. A namespace-owning Node with a complete
// allocated identity is realized by sidecar interposition and carries the derived contract.
func appendManagementPlan(plan *Plan, node *EvaluatedNode, values []ManagementInput) {
	interfaceSelector := ManagementInterfaceSelector("")
	if node.Config.MgmtIntf == "" {
		interfaceSelector = ManagementInterfacePodTransport
	}
	entry := ManagementPlan{
		ID:                "management/" + node.Input.ID,
		NodeID:            node.Input.ID,
		InterfaceName:     node.Config.MgmtIntf,
		InterfaceSelector: interfaceSelector,
	}
	if value := managementForNode(values, node.Input.ID); value != nil {
		entry.IPv4 = value.IPv4
		entry.IPv4Gateway = value.IPv4Gateway
		entry.IPv6 = value.IPv6
		entry.IPv6Gateway = value.IPv6Gateway
		entry.DNS = value.DNS

		if entry.IPv4 != "" && entry.IPv4Gateway != "" &&
			!strings.HasPrefix(node.Config.NetworkMode, "container:") {
			entry.InterfaceName = ""
			entry.InterfaceSelector = ManagementInterfaceInterposed
			entry.Interposition = interpositionContract(plan, node, value)
		}
	}
	plan.Management = append(plan.Management, entry)
}

// interpositionContract derives the vendor-neutral interposition data for one Node from its
// evaluated containerlab configuration, its planned container ports, and the controller's
// declared inbound port set. It never branches on kind or vendor identity.
func interpositionContract(
	plan *Plan,
	node *EvaluatedNode,
	management *ManagementInput,
) *ManagementInterposition {
	deviceInterface := node.Config.MgmtIntf
	if deviceInterface == "" {
		deviceInterface = defaultManagementDeviceInterface
	}

	contract := &ManagementInterposition{
		DeviceInterface: deviceInterface,
		DeviceMAC:       node.Config.MacAddress,
	}

	seen := map[string]bool{}

	appendPort := func(port Port, translate bool) {
		if port.Number <= 0 || port.Number > 65535 {
			return
		}

		protocol := strings.ToLower(port.Protocol)
		if protocol == "" {
			protocol = protocolTCP
		}

		if protocol != protocolTCP && protocol != protocolUDP {
			return
		}

		key := protocol + "/" + strconv.Itoa(port.Number)
		if seen[key] {
			return
		}

		seen[key] = true

		if !translate {
			return
		}

		contract.InboundPorts = append(contract.InboundPorts, ManagementPortMap{
			Protocol:   protocol,
			PodPort:    uint16(port.Number),
			DevicePort: uint16(port.Number),
		})
	}

	// Ports planned for the Node's own containers translate to its management address; ports of
	// other containers sharing the Pod namespace only claim the Pod-side port so a declared
	// inbound port never shadows a group member's listener.
	for _, container := range plan.Containers {
		if container.NodeID != node.Input.ID {
			continue
		}

		for _, port := range container.Ports {
			appendPort(port, true)
		}
	}

	for _, container := range plan.Containers {
		if container.NodeID == node.Input.ID {
			continue
		}

		for _, port := range container.Ports {
			appendPort(port, false)
		}
	}

	if management != nil {
		for _, port := range management.InboundPorts {
			appendPort(port, true)
		}
	}

	return contract
}

func appendInterfacePlans(
	plan *Plan,
	node *EvaluatedNode,
	containerID,
	namespaceOwnerID string,
) {
	for _, intf := range node.Interfaces {
		plan.Interfaces = append(plan.Interfaces, InterfacePlan{
			ID:               intf.Input.ID,
			NodeID:           node.Input.ID,
			NamespaceOwnerID: namespaceOwnerID,
			Name:             intf.Name,
			Alias:            intf.Alias,
			LinkID:           intf.Input.LinkID,
			LinkName:         intf.Input.LinkName,
			PeerNodeID:       intf.Input.PeerNodeID,
			PeerInterface:    intf.Input.PeerInterface,
			PeerTransport:    intf.Input.PeerTransport,
			Connectivity:     intf.Input.Connectivity,
			TunnelID:         intf.Input.TunnelID,
			MTU:              intf.Input.MTU,
			LinkApplyMode:    node.LinkApplyMode,
			RequiredAtStart:  true,
		})
		plan.Actions = append(plan.Actions, Action{
			ID: "wait-interface/" + intf.Input.ID, Phase: PhasePreStart,
			Target: ActionTarget{
				NodeID: node.Input.ID, ContainerID: containerID,
				NamespaceOwnerID: namespaceOwnerID,
			},
			Kind: ActionWaitInterface,
			WaitInterface: &WaitInterfaceAction{
				InterfaceID: intf.Input.ID, TimeoutSeconds: 300,
			},
		})
	}
}

func imageInputForContainer(inputs []ImageInput, nodeID, reference string) (ImageInput, error) {
	var match *ImageInput
	for _, image := range inputs {
		if image.NodeID != nodeID ||
			(image.SourceReference != reference && image.DigestReference != reference) {
			continue
		}
		if match != nil && match.DigestReference != image.DigestReference {
			return ImageInput{}, &Error{
				Code: ErrorInvalidInput, NodeID: nodeID, Field: "images",
				Behavior: "container-image",
				Message:  "multiple image metadata entries ambiguously match an imported container",
			}
		}
		duplicate := image
		match = &duplicate
	}
	if match != nil {
		return *match, nil
	}

	return ImageInput{}, &Error{
		Code: ErrorMissingInput, NodeID: nodeID, Field: "images",
		Behavior: "container-image", Message: "container image metadata is missing",
	}
}

func splitOverride(raw, field, nodeID string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	values, err := shlex.Split(raw)
	if err != nil || len(values) == 0 {
		return nil, nodeMappingError(nodeID, field, "command override is invalid")
	}

	return values, nil
}

func mapKeyValues(values map[string]string) []KeyValue {
	result := make([]KeyValue, 0, len(values))
	for name, value := range values {
		result = append(result, KeyValue{Name: name, Value: value})
	}

	return result
}

func mapPorts(nodePorts []string, imagePorts []Port, nodeID string) ([]Port, error) {
	result := slices.Clone(imagePorts)
	seen := map[string]bool{}
	for index := range result {
		result[index].Protocol = strings.ToUpper(defaultString(result[index].Protocol, "TCP"))
		seen[fmt.Sprintf("%d/%s", result[index].Number, result[index].Protocol)] = true
	}
	for _, raw := range nodePorts {
		value, protocol, _ := strings.Cut(raw, "/")
		number, err := strconv.Atoi(value)
		protocol = strings.ToUpper(defaultString(protocol, "TCP"))
		if err != nil || number < 1 || number > 65535 ||
			(protocol != "TCP" && protocol != "UDP") {
			return nil, nodeMappingError(nodeID, "definition.ports", "port is invalid")
		}
		key := fmt.Sprintf("%d/%s", number, protocol)
		if !seen[key] {
			result = append(result, Port{Number: number, Protocol: protocol})
			seen[key] = true
		}
	}

	return result, nil
}

func mapDNS(config *clabtypes.DNSConfig) DNSConfig {
	if config == nil {
		return DNSConfig{}
	}

	return DNSConfig{
		Servers: slices.Clone(config.Servers),
		Search:  slices.Clone(config.Search),
		Options: slices.Clone(config.Options),
	}
}

func mapHealthcheck(config *clabtypes.HealthcheckConfig, image *Healthcheck) *Healthcheck {
	if config == nil {
		if image == nil {
			return nil
		}
		duplicate := *image
		duplicate.Test = slices.Clone(image.Test)
		return &duplicate
	}

	return &Healthcheck{
		Test:        slices.Clone(config.Test),
		Interval:    int64(time.Duration(config.Interval) * time.Second),
		Timeout:     int64(time.Duration(config.Timeout) * time.Second),
		StartPeriod: int64(time.Duration(config.StartPeriod) * time.Second),
		Retries:     config.Retries,
	}
}

func digestFromReference(reference string) string {
	_, digest, found := strings.Cut(reference, "@")
	if !found {
		return reference
	}

	return digest
}

func shortDigest(value string) string {
	return strings.TrimPrefix(Digest([]byte(value)), "sha256:")[:12]
}

func formatCPU(value float64) string {
	if value == 0 {
		return ""
	}

	return strconv.FormatFloat(value, 'f', -1, 64)
}

// formatMemory converts containerlab's human-readable memory limit (parsed by the same
// humanize rules the Docker runtime applies) into an exact byte quantity for the Kubernetes
// renderer -- passing the raw string through would let Docker's "512m" (megabytes) silently
// become the Kubernetes quantity 512 millibytes.
func formatMemory(value string) (string, error) {
	if value == "" {
		return "", nil
	}

	memoryBytes, err := humanize.ParseBytes(value)
	if err != nil {
		return "", err
	}

	return strconv.FormatUint(memoryBytes, 10), nil
}

func normalizeTmpfsOptions(options string) (string, []string, bool, bool, error) {
	if options == "" {
		return "", nil, false, false, nil
	}
	result := make([]string, 0, strings.Count(options, ",")+1)
	size := ""
	readOnly := false
	requiresRuntimeMount := false
	for option := range strings.SplitSeq(options, ",") {
		option = strings.TrimSpace(option)
		if option == "" || strings.ContainsAny(option, "\x00\n\r") {
			return "", nil, false, false, errors.New("option is empty or malformed")
		}
		key, _, _ := strings.Cut(option, "=")
		switch key {
		case "rw":
			readOnly = false
		case "ro":
			readOnly = true
		case "defaults":
		case "size":
			if !strings.Contains(option, "=") {
				return "", nil, false, false, errors.New("size has no value")
			}
		default:
			requiresRuntimeMount = true
		}
		if value, found := strings.CutPrefix(option, "size="); found {
			bytes, err := humanize.ParseBytes(value)
			if value == "" {
				return "", nil, false, false, errors.New("size is empty")
			}
			if err != nil { //nolint:gocritic // the parse outcomes are deliberate explicit guards.
				requiresRuntimeMount = true
			} else if bytes == 0 {
				return "", nil, false, false, errors.New("size is not positive")
			} else {
				size = strconv.FormatUint(bytes, 10)
				option = "size=" + size
			}
		}
		result = append(result, option)
	}

	return size, result, readOnly, requiresRuntimeMount, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

func nodeMappingError(nodeID, field, message string) error {
	return &Error{
		Code: ErrorUnsupported, NodeID: nodeID, Field: field,
		Behavior: "kubernetes-mapping", Message: message,
	}
}
