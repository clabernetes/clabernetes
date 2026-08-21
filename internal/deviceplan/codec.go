//nolint:cyclop,err113,funlen,gocognit,gocyclo,maintidx // Schema validation is one fail-closed boundary.
package deviceplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"path"
	"slices"
	"strings"
)

const sha256DigestLength = len("sha256:") + sha256.Size*2

// CanonicalJSON validates, normalizes, and deterministically serializes planning input.
func (i Input) CanonicalJSON() ([]byte, error) {
	normalized, err := NormalizeInput(i)
	if err != nil {
		return nil, err
	}

	return marshalCanonical(normalized, "input")
}

// Digest returns the digest of canonical normalized planning input.
func (i Input) Digest() (string, error) {
	raw, err := i.CanonicalJSON()
	if err != nil {
		return "", err
	}

	return Digest(raw), nil
}

// CanonicalJSON validates, normalizes, and deterministically serializes a device plan.
func (p Plan) CanonicalJSON() ([]byte, error) {
	normalized, err := NormalizePlan(p)
	if err != nil {
		return nil, err
	}

	return marshalCanonical(normalized, "plan")
}

// Digest returns the digest of a canonical normalized device plan.
func (p Plan) Digest() (string, error) {
	raw, err := p.CanonicalJSON()
	if err != nil {
		return "", err
	}

	return Digest(raw), nil
}

// Digest returns the SHA-256 identity of already-canonical bytes.
func Digest(raw []byte) string {
	digest := sha256.Sum256(raw)

	return "sha256:" + hex.EncodeToString(digest[:])
}

// DecodeInput rejects unknown fields and trailing JSON, then returns normalized input.
func DecodeInput(raw []byte) (Input, error) {
	decoded, err := decodeStrict[Input](raw, "input")
	if err != nil {
		return Input{}, err
	}

	return NormalizeInput(decoded)
}

// DecodePlan rejects unknown fields and trailing JSON, then returns a normalized plan.
func DecodePlan(raw []byte) (Plan, error) {
	decoded, err := decodeStrict[Plan](raw, "plan")
	if err != nil {
		return Plan{}, err
	}

	return NormalizePlan(decoded)
}

// NormalizeInput validates and orders every set-like input field without mutating the caller.
func NormalizeInput(input Input) (Input, error) {
	normalized, err := cloneJSON(input)
	if err != nil {
		return Input{}, planningError(ErrorSerialization, "input", "cannot clone input", err)
	}

	if err = validateInput(normalized); err != nil {
		return Input{}, err
	}

	slices.SortFunc(normalized.Nodes, func(left, right NodeInput) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(normalized.Images, compareImageInput)

	for index := range normalized.Images {
		normalizeImageConfig(&normalized.Images[index].Config)
		slices.Sort(normalized.Images[index].Platform.OSFeatures)
	}

	slices.SortFunc(normalized.Payloads, func(left, right PayloadInput) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(normalized.Certificates, func(left, right CertificateInput) int {
		if compared := strings.Compare(left.NodeID, right.NodeID); compared != 0 {
			return compared
		}

		return strings.Compare(left.StorageName, right.StorageName)
	})
	slices.SortFunc(normalized.Management, func(left, right ManagementInput) int {
		return strings.Compare(left.NodeID, right.NodeID)
	})

	for index := range normalized.Management {
		slices.SortFunc(normalized.Management[index].InboundPorts, func(left, right Port) int {
			if compared := strings.Compare(left.Protocol, right.Protocol); compared != 0 {
				return compared
			}

			return left.Number - right.Number
		})
	}

	slices.SortFunc(normalized.Interfaces, func(left, right InterfaceInput) int {
		return strings.Compare(left.ID, right.ID)
	})

	return normalized, nil
}

// NormalizePlan validates and orders every set-like plan field without mutating the caller.
func NormalizePlan(plan Plan) (Plan, error) {
	normalized, err := cloneJSON(plan)
	if err != nil {
		return Plan{}, planningError(ErrorSerialization, "plan", "cannot clone plan", err)
	}

	for index := range normalized.Containers {
		if normalized.Containers[index].RuntimeID == "" {
			normalized.Containers[index].RuntimeID = normalized.Containers[index].ID
		}
	}

	for index := range normalized.Files {
		if normalized.Files[index].ArtifactKind == "" {
			normalized.Files[index].ArtifactKind = ArtifactRegular
		}
	}

	if err = validatePlan(normalized); err != nil {
		return Plan{}, err
	}

	slices.SortFunc(normalized.Nodes, func(left, right NodePlan) int {
		return strings.Compare(left.ID, right.ID)
	})

	for index := range normalized.Nodes {
		slices.Sort(normalized.Nodes[index].ReadinessContainerIDs)
		slices.Sort(normalized.Nodes[index].Aliases)
	}

	slices.SortFunc(normalized.Containers, func(left, right ContainerPlan) int {
		return strings.Compare(left.ID, right.ID)
	})

	for index := range normalized.Containers {
		container := &normalized.Containers[index]
		slices.Sort(container.MountIDs)
		slices.Sort(container.Security.CapabilitiesAdd)
		slices.Sort(container.Security.CapabilitiesDrop)
		slices.SortFunc(container.Security.Devices, compareDevice)
		slices.SortFunc(container.Security.Sysctls, compareKeyValue)
		slices.SortFunc(container.Environment, compareKeyValue)
		slices.SortFunc(container.Labels, compareKeyValue)
		slices.SortFunc(container.Ports, comparePort)
	}

	slices.SortFunc(normalized.Files, func(left, right FilePlan) int {
		return strings.Compare(left.ID, right.ID)
	})

	for index := range normalized.Files {
		slices.SortFunc(normalized.Files[index].Variables, compareKeyValue)
		slices.SortFunc(
			normalized.Files[index].ExtendedAttributes,
			func(left, right ExtendedAttribute) int {
				return strings.Compare(left.Name, right.Name)
			},
		)
	}

	slices.SortFunc(normalized.Volumes, func(left, right VolumePlan) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(normalized.Mounts, func(left, right MountPlan) int {
		return strings.Compare(left.ID, right.ID)
	})
	slices.SortFunc(normalized.Actions, compareAction)
	slices.SortFunc(normalized.Management, func(left, right ManagementPlan) int {
		return strings.Compare(left.ID, right.ID)
	})

	for index := range normalized.Management {
		slices.SortFunc(normalized.Management[index].Routes, compareRoute)

		if contract := normalized.Management[index].Interposition; contract != nil {
			slices.Sort(contract.TransportCIDRs)
			slices.SortFunc(contract.InboundPorts, func(left, right ManagementPortMap) int {
				if compared := strings.Compare(left.Protocol, right.Protocol); compared != 0 {
					return compared
				}

				return int(left.PodPort) - int(right.PodPort)
			})
		}
	}

	slices.SortFunc(normalized.Interfaces, func(left, right InterfacePlan) int {
		return strings.Compare(left.ID, right.ID)
	})

	return normalized, nil
}

func validateInput(input Input) error {
	if err := validateHeader(input.SchemaVersion, input.Compatibility); err != nil {
		return err
	}

	if strings.TrimSpace(input.TopologyName) == "" {
		return planningError(ErrorMissingInput, "topologyName", "topology name is required", nil)
	}

	if input.EntropyDigest != "" && !validDigest(input.EntropyDigest) {
		return planningError(ErrorInvalidInput, "entropyDigest", "must be a sha256 digest", nil)
	}

	if len(input.Nodes) == 0 {
		return planningError(ErrorMissingInput, "nodes", "at least one Node is required", nil)
	}

	nodes := map[string]bool{}
	names := map[string]bool{}

	for index, node := range input.Nodes {
		field := fmt.Sprintf("nodes[%d]", index)
		if node.ID == "" || node.Name == "" || node.Kind == "" {
			return planningError(
				ErrorInvalidInput,
				field,
				"id, name, and kind are required",
				nil,
			)
		}

		if nodes[node.ID] || names[node.Name] {
			return planningError(ErrorInvalidInput, field, "Node identity is duplicated", nil)
		}

		if !json.Valid(node.Definition) {
			return planningError(ErrorInvalidInput, field+".definition", "must be valid JSON", nil)
		}

		nodes[node.ID] = true
		names[node.Name] = true
	}

	for index, node := range input.Nodes {
		if node.GroupOwner != "" && !nodes[node.GroupOwner] {
			return planningError(
				ErrorMissingInput,
				fmt.Sprintf("nodes[%d].groupOwner", index),
				"references an unknown Node",
				nil,
			)
		}
	}

	images := map[string]bool{}

	for index, image := range input.Images {
		field := fmt.Sprintf("images[%d]", index)
		if !nodes[image.NodeID] {
			return planningError(
				ErrorMissingInput,
				field+".nodeID",
				"references an unknown Node",
				nil,
			)
		}

		identity := image.Role
		if identity == "" {
			identity = image.SourceReference
		}

		key := image.NodeID + "\x00" + identity
		if images[key] {
			return planningError(ErrorInvalidInput, field, "image identity is duplicated", nil)
		}

		if image.SourceReference == "" || image.DigestReference == "" ||
			image.Platform.OS == "" || image.Platform.Architecture == "" {
			return planningError(
				ErrorMissingInput,
				field,
				"source, digest, operating system, and architecture are required",
				nil,
			)
		}

		if err := validateUniqueKeyValues(image.Config.Environment,
			field+".config.environment"); err != nil {
			return err
		}

		if err := validateUniqueKeyValues(image.Config.Labels, field+".config.labels"); err != nil {
			return err
		}

		images[key] = true
	}

	payloads := map[string]bool{}

	for index, payload := range input.Payloads {
		field := fmt.Sprintf("payloads[%d]", index)
		if payload.ID == "" || payload.Destination == "" || !nodes[payload.NodeID] {
			return planningError(
				ErrorInvalidInput,
				field,
				"id, known Node, and destination are required",
				nil,
			)
		}

		if payloads[payload.ID] {
			return planningError(ErrorInvalidInput, field+".id", "payload ID is duplicated", nil)
		}

		if !validPayloadKind(payload.Kind) {
			return planningError(
				ErrorUnsupported,
				field+".kind",
				"payload kind is unsupported",
				nil,
			)
		}

		if payload.Reference == "" || payload.Kind == PayloadInline && payload.Digest == "" {
			return planningError(ErrorMissingInput, field, "payload identity is incomplete", nil)
		}

		payloads[payload.ID] = true
	}

	certificates := map[string]bool{}

	for index, certificate := range input.Certificates {
		field := fmt.Sprintf("certificates[%d]", index)
		if !nodes[certificate.NodeID] {
			return planningError(
				ErrorMissingInput,
				field+".nodeID",
				"references an unknown Node",
				nil,
			)
		}

		if strings.TrimSpace(certificate.StorageName) == "" ||
			!validDigest(certificate.CertificateDigest) ||
			!validDigest(certificate.PrivateKeyDigest) ||
			!validDigest(certificate.CACertificateDigest) ||
			!validDigest(certificate.CAPrivateKeyDigest) {
			return planningError(
				ErrorInvalidInput,
				field,
				"certificate storage identity and material digests are required",
				nil,
			)
		}

		identity := certificate.NodeID + "\x00" + certificate.StorageName
		if certificates[identity] {
			return planningError(
				ErrorInvalidInput,
				field,
				"certificate storage identity is duplicated",
				nil,
			)
		}

		certificates[identity] = true
	}

	managementNodes := map[string]bool{}

	for index, management := range input.Management {
		field := fmt.Sprintf("management[%d]", index)
		if !nodes[management.NodeID] {
			return planningError(ErrorInvalidInput, field, "known Node is required", nil)
		}

		if management.InterfaceName == "" && management.IPv4 == "" &&
			management.IPv4Gateway == "" && management.IPv6 == "" &&
			management.IPv6Gateway == "" && len(management.DNS.Servers) == 0 &&
			len(management.DNS.Search) == 0 && len(management.DNS.Options) == 0 {
			return planningError(ErrorInvalidInput, field, "management intent is empty", nil)
		}

		if managementNodes[management.NodeID] {
			return planningError(
				ErrorInvalidInput,
				field,
				"Node management input is duplicated",
				nil,
			)
		}

		managementNodes[management.NodeID] = true

		for portIndex, port := range management.InboundPorts {
			portField := fmt.Sprintf("%s.inboundPorts[%d]", field, portIndex)
			if port.Number <= 0 || port.Number > 65535 {
				return planningError(ErrorInvalidInput, portField, "port number is invalid", nil)
			}

			switch strings.ToLower(port.Protocol) {
			case "", protocolTCP, protocolUDP:
			default:
				return planningError(ErrorInvalidInput, portField, "port protocol is invalid", nil)
			}
		}

	}

	interfaces := map[string]bool{}

	for index, intf := range input.Interfaces {
		field := fmt.Sprintf("interfaces[%d]", index)
		if intf.ID == "" || !nodes[intf.NodeID] || intf.Name == "" || intf.LinkID == "" ||
			intf.Connectivity == "" || intf.TunnelID < 0 || intf.MTU < 0 {
			return planningError(ErrorInvalidInput, field, "interface input is incomplete", nil)
		}

		if interfaces[intf.ID] {
			return planningError(ErrorInvalidInput, field+".id", "interface ID is duplicated", nil)
		}

		interfaces[intf.ID] = true
	}

	return nil
}

func validatePlan(plan Plan) error {
	if err := validateHeader(plan.SchemaVersion, plan.Compatibility); err != nil {
		return err
	}

	if !validDigest(plan.InputDigest) {
		return planningError(ErrorInvalidInput, "inputDigest", "must be a sha256 digest", nil)
	}

	if plan.Planner.Name == "" || plan.Planner.Revision == "" {
		return planningError(ErrorMissingInput, "planner", "name and revision are required", nil)
	}

	if len(plan.Nodes) == 0 || len(plan.Containers) == 0 {
		return planningError(ErrorMissingInput, "plan", "Nodes and containers are required", nil)
	}

	nodes := map[string]NodePlan{}

	for index, node := range plan.Nodes {
		field := fmt.Sprintf("nodes[%d]", index)
		if node.ID == "" || node.Name == "" || node.Kind == "" || len(node.ContainerIDs) == 0 ||
			len(node.ReadinessContainerIDs) == 0 {
			return planningError(ErrorInvalidInput, field, "logical Node plan is incomplete", nil)
		}

		if _, exists := nodes[node.ID]; exists {
			return planningError(ErrorInvariant, field+".id", "Node ID is duplicated", nil)
		}

		if hasDuplicates(node.ContainerIDs) || hasDuplicates(node.ReadinessContainerIDs) {
			return planningError(ErrorInvariant, field, "container references are duplicated", nil)
		}

		nodes[node.ID] = node
	}

	containers := map[string]ContainerPlan{}
	runtimeIDs := map[string]bool{}

	for index, container := range plan.Containers {
		field := fmt.Sprintf("containers[%d]", index)
		if container.ID == "" || container.RuntimeID == "" || container.Image == "" ||
			container.NamespaceOwnerID == "" {
			return planningError(
				ErrorInvalidInput,
				field,
				"container identity and image are required",
				nil,
			)
		}

		if container.ImageDigest != "" && !validDigest(container.ImageDigest) {
			return planningError(
				ErrorInvalidInput,
				field+".imageDigest",
				"image digest must be a sha256 digest",
				nil,
			)
		}

		if _, exists := nodes[container.NodeID]; !exists {
			return planningError(ErrorInvariant, field+".nodeID", "references an unknown Node", nil)
		}

		if _, exists := containers[container.ID]; exists {
			return planningError(ErrorInvariant, field+".id", "container ID is duplicated", nil)
		}

		if runtimeIDs[container.RuntimeID] {
			return planningError(
				ErrorInvariant,
				field+".runtimeID",
				"runtime ID is duplicated",
				nil,
			)
		}

		runtimeIDs[container.RuntimeID] = true
		if err := validateUniqueKeyValues(container.Environment, field+".environment"); err != nil {
			return err
		}

		if err := validateUniqueKeyValues(container.Labels, field+".labels"); err != nil {
			return err
		}

		if err := validateUniqueKeyValues(container.Security.Sysctls,
			field+".security.sysctls"); err != nil {
			return err
		}

		ports := map[string]bool{}

		for portIndex, port := range container.Ports {
			portField := fmt.Sprintf("%s.ports[%d]", field, portIndex)
			if port.Number < 1 || port.Number > 65535 ||
				(port.Protocol != "TCP" && port.Protocol != "UDP") {
				return planningError(
					ErrorInvalidInput,
					portField,
					"port number or protocol is invalid",
					nil,
				)
			}

			key := fmt.Sprintf("%d/%s", port.Number, port.Protocol)
			if ports[key] {
				return planningError(ErrorInvariant, portField, "container port is duplicated", nil)
			}

			ports[key] = true
		}

		containers[container.ID] = container
	}

	for index, container := range plan.Containers {
		if _, exists := containers[container.NamespaceOwnerID]; !exists {
			return planningError(
				ErrorInvariant,
				fmt.Sprintf("containers[%d].namespaceOwnerID", index),
				"references an unknown container",
				nil,
			)
		}
	}

	for index, node := range plan.Nodes {
		containerIDs := make(map[string]bool, len(node.ContainerIDs))
		for _, containerID := range node.ContainerIDs {
			containerIDs[containerID] = true
		}

		for _, containerID := range append(slices.Clone(node.ContainerIDs),
			node.ReadinessContainerIDs...) {
			container, exists := containers[containerID]
			if !exists || container.NodeID != node.ID {
				return planningError(
					ErrorInvariant,
					fmt.Sprintf("nodes[%d].containerIDs", index),
					"references a missing or foreign container",
					nil,
				)
			}
		}

		for _, containerID := range node.ReadinessContainerIDs {
			if !containerIDs[containerID] {
				return planningError(
					ErrorInvariant,
					fmt.Sprintf("nodes[%d].readinessContainerIDs", index),
					"references a container outside the Node component inventory",
					nil,
				)
			}
		}
	}

	files := map[string]FilePlan{}

	for index, file := range plan.Files {
		field := fmt.Sprintf("files[%d]", index)

		artifactKind := file.ArtifactKind
		if artifactKind == "" {
			artifactKind = ArtifactRegular
		}

		artifactPath := path.Clean(file.ArtifactPath)
		if file.ID == "" || !validFileSourceKind(file.SourceKind) ||
			(artifactKind != ArtifactRegular && artifactKind != ArtifactSymlink &&
				artifactKind != ArtifactDirectory) ||
			file.ArtifactPath == "" || path.IsAbs(file.ArtifactPath) ||
			(artifactPath == "." && artifactKind != ArtifactDirectory) ||
			strings.HasPrefix(path.Clean(file.ArtifactPath), "../") ||
			(file.Destination != "" && !path.IsAbs(file.Destination)) ||
			file.Mode > 0o777 || !validPortableOwnership(file.UID) ||
			!validPortableOwnership(file.GID) {
			return planningError(ErrorInvalidInput, field, "file plan is incomplete", nil)
		}

		if artifactKind == ArtifactSymlink { //nolint:gocritic // each guard names one divergence class.
			if (file.SourceKind != FileSourceGenerator &&
				file.SourceKind != FileSourceCertificate) || file.LinkTarget == "" ||
				len(file.LinkTarget) > maxGeneratedSymlinkTargetBytes ||
				strings.ContainsRune(file.LinkTarget, 0) || file.Mode != 0 {
				return planningError(
					ErrorInvalidInput,
					field,
					"symbolic-link plan is incomplete",
					nil,
				)
			}
		} else if artifactKind == ArtifactDirectory {
			if (file.SourceKind != FileSourceGenerator &&
				file.SourceKind != FileSourceCertificate) || file.LinkTarget != "" ||
				file.Digest != "" || file.Destination != "" {
				return planningError(ErrorInvalidInput, field, "directory plan is incomplete", nil)
			}
		} else if file.LinkTarget != "" {
			return planningError(ErrorInvalidInput, field,
				"regular artifact has a link target", nil)
		}

		if len(file.ExtendedAttributes) > 128 ||
			(len(file.ExtendedAttributes) != 0 && file.SourceKind != FileSourceGenerator &&
				file.SourceKind != FileSourceCertificate) {
			return planningError(ErrorInvalidInput, field, "artifact metadata is invalid", nil)
		}

		attributeNames := map[string]bool{}

		for attributeIndex, attribute := range file.ExtendedAttributes {
			attributeField := fmt.Sprintf("%s.extendedAttributes[%d]", field, attributeIndex)
			if attribute.Name == "" || len(attribute.Name) > 255 ||
				strings.ContainsRune(attribute.Name, 0) || !validDigest(attribute.Digest) ||
				attributeNames[attribute.Name] {
				return planningError(
					ErrorInvalidInput,
					attributeField,
					"extended attribute metadata is invalid",
					nil,
				)
			}

			attributeNames[attribute.Name] = true
		}

		if file.SourceKind == FileSourcePayload && file.Destination == "" {
			return planningError(ErrorInvalidInput, field, "payload destination is required", nil)
		}

		if file.Digest != "" && !validDigest(file.Digest) {
			return planningError(ErrorInvalidInput, field+".digest", "file digest is invalid", nil)
		}

		if (file.SourceKind == FileSourceEmpty) != (file.SourceReference == "") {
			return planningError(ErrorInvalidInput, field, "file source identity is invalid", nil)
		}

		if err := validateUniqueKeyValues(file.Variables, field+".variables"); err != nil {
			return err
		}

		if _, exists := nodes[file.NodeID]; !exists {
			return planningError(ErrorInvariant, field+".nodeID", "references an unknown Node", nil)
		}

		if _, exists := files[file.ID]; exists {
			return planningError(ErrorInvariant, field+".id", "file ID is duplicated", nil)
		}

		files[file.ID] = file
	}

	volumes := map[string]VolumePlan{}

	for index, volume := range plan.Volumes {
		field := fmt.Sprintf("volumes[%d]", index)
		if volume.ID == "" || !validVolumeKind(volume.Kind) {
			return planningError(ErrorInvalidInput, field, "volume ID or kind is invalid", nil)
		}

		if _, exists := nodes[volume.NodeID]; !exists {
			return planningError(ErrorInvariant, field+".nodeID", "references an unknown Node", nil)
		}

		if _, exists := volumes[volume.ID]; exists {
			return planningError(ErrorInvariant, field+".id", "volume ID is duplicated", nil)
		}

		volumes[volume.ID] = volume
	}

	mounts := map[string]MountPlan{}

	for index, mount := range plan.Mounts {
		field := fmt.Sprintf("mounts[%d]", index)
		if mount.ID == "" || mount.Destination == "" || !path.IsAbs(mount.Destination) ||
			path.Clean(mount.Destination) != mount.Destination {
			return planningError(
				ErrorInvalidInput,
				field,
				"mount ID and destination are required",
				nil,
			)
		}

		if _, exists := containers[mount.ContainerID]; !exists {
			return planningError(
				ErrorInvariant,
				field+".containerID",
				"references an unknown container",
				nil,
			)
		}

		if _, exists := volumes[mount.VolumeID]; !exists {
			return planningError(
				ErrorInvariant,
				field+".volumeID",
				"references an unknown volume",
				nil,
			)
		}

		if _, exists := mounts[mount.ID]; exists {
			return planningError(ErrorInvariant, field+".id", "mount ID is duplicated", nil)
		}

		mounts[mount.ID] = mount
	}

	for index, container := range plan.Containers {
		for _, mountID := range container.MountIDs {
			mount, exists := mounts[mountID]
			if !exists || mount.ContainerID != container.ID {
				return planningError(
					ErrorInvariant,
					fmt.Sprintf("containers[%d].mountIDs", index),
					"references a missing or foreign mount",
					nil,
				)
			}
		}
	}

	management := map[string]ManagementPlan{}
	managementNodes := map[string]bool{}

	for index, item := range plan.Management {
		field := fmt.Sprintf("management[%d]", index)
		if item.ID == "" ||
			(item.InterfaceName == "" && item.InterfaceSelector == "") ||
			(item.InterfaceName != "" && item.InterfaceSelector != "") ||
			(item.InterfaceSelector != "" &&
				item.InterfaceSelector != ManagementInterfacePodTransport &&
				item.InterfaceSelector != ManagementInterfaceInterposed) {
			return planningError(ErrorInvalidInput, field, "management plan is incomplete", nil)
		}

		if err := validateManagementInterposition(field, item); err != nil {
			return err
		}

		if _, exists := nodes[item.NodeID]; !exists || managementNodes[item.NodeID] {
			return planningError(
				ErrorInvariant,
				field+".nodeID",
				"Node is unknown or duplicated",
				nil,
			)
		}

		if _, exists := management[item.ID]; exists {
			return planningError(ErrorInvariant, field+".id", "management ID is duplicated", nil)
		}

		management[item.ID] = item
		managementNodes[item.NodeID] = true
	}

	interfaces := map[string]InterfacePlan{}

	for index, intf := range plan.Interfaces {
		field := fmt.Sprintf("interfaces[%d]", index)
		if intf.ID == "" || intf.Name == "" || intf.NamespaceOwnerID == "" ||
			intf.LinkID == "" || intf.Connectivity == "" || intf.TunnelID < 0 || intf.MTU < 0 ||
			!validLinkApplyMode(intf.LinkApplyMode) {
			return planningError(ErrorInvalidInput, field, "interface plan is incomplete", nil)
		}

		if _, exists := nodes[intf.NodeID]; !exists {
			return planningError(ErrorInvariant, field+".nodeID", "references an unknown Node", nil)
		}

		if _, exists := containers[intf.NamespaceOwnerID]; !exists {
			return planningError(
				ErrorInvariant,
				field+".namespaceOwnerID",
				"references an unknown container",
				nil,
			)
		}

		if _, exists := interfaces[intf.ID]; exists {
			return planningError(ErrorInvariant, field+".id", "interface ID is duplicated", nil)
		}

		interfaces[intf.ID] = intf
	}

	actions := map[string]bool{}

	for index, action := range plan.Actions {
		field := fmt.Sprintf("actions[%d]", index)
		if action.ID == "" || !validActionPhase(action.Phase) {
			return planningError(ErrorInvalidInput, field, "action ID or phase is invalid", nil)
		}

		if actions[action.ID] {
			return planningError(ErrorInvariant, field+".id", "action ID is duplicated", nil)
		}

		if _, exists := nodes[action.Target.NodeID]; !exists {
			return planningError(
				ErrorInvariant,
				field+".target.nodeID",
				"references an unknown Node",
				nil,
			)
		}

		if action.Target.ContainerID != "" {
			if _, exists := containers[action.Target.ContainerID]; !exists {
				return planningError(
					ErrorInvariant,
					field+".target.containerID",
					"references an unknown container",
					nil,
				)
			}
		}

		if action.Target.NamespaceOwnerID != "" {
			if _, exists := containers[action.Target.NamespaceOwnerID]; !exists {
				return planningError(
					ErrorInvariant,
					field+".target.namespaceOwnerID",
					"references an unknown container",
					nil,
				)
			}
		}

		if err := validateActionPayload(
			action,
			files,
			volumes,
			mounts,
			management,
			interfaces,
			field,
		); err != nil {
			return err
		}

		actions[action.ID] = true
	}

	return nil
}

func validPortableOwnership(value *int64) bool {
	return value == nil || *value >= 0 && *value < 1<<31
}

func validateHeader(schema string, compatibility Compatibility) error {
	if schema != SchemaVersion {
		return planningError(ErrorInvalidInput, "schemaVersion", "unsupported schema version", nil)
	}

	if compatibility.ContainerlabModule == "" || compatibility.ContainerlabVersion == "" ||
		!validDigest(compatibility.RegistryDigest) ||
		compatibility.PlanSchemaVersion != SchemaVersion {
		return planningError(
			ErrorInvalidInput,
			"compatibility",
			"compatibility identity is incomplete",
			nil,
		)
	}

	return nil
}

func validateActionPayload(
	action Action,
	files map[string]FilePlan,
	volumes map[string]VolumePlan,
	mounts map[string]MountPlan,
	management map[string]ManagementPlan,
	interfaces map[string]InterfacePlan,
	field string,
) error {
	payloads := 0

	for _, present := range []bool{
		action.Exec != nil,
		action.File != nil,
		action.WriteStdin != nil,
		action.Mount != nil,
		action.Sysctl != nil,
		action.WaitInterface != nil,
		action.RenameInterface != nil,
		action.ManagementForwarding != nil,
		action.ImportedDeployEndpoints != nil,
		action.ImportedPostDeploy != nil,
		action.ImportedReadiness != nil,
		action.Save != nil,
	} {
		if present {
			payloads++
		}
	}

	if payloads != 1 {
		return planningError(
			ErrorInvariant,
			field,
			"action must contain exactly one typed payload",
			nil,
		)
	}

	var valid bool

	switch action.Kind {
	case ActionExec:
		valid = action.Exec != nil && len(action.Exec.Command) > 0
	case ActionFile:
		file, exists := files[valueOrEmpty(
			action.File,
			func(value *FileAction) string { return value.FileID },
		)]

		valid = exists
		if valid && action.File.WriteMode != "" &&
			action.File.WriteMode != FileWriteReplace && action.File.WriteMode != FileWriteAppend {
			valid = false
		}

		if valid && action.Phase == PhasePostStart {
			valid = action.File.Destination != "" && file.ArtifactKind == ArtifactRegular
		}
	case ActionWriteStdin:
		file, exists := files[valueOrEmpty(
			action.WriteStdin,
			func(value *WriteStdinAction) string { return value.FileID },
		)]
		valid = exists && file.ArtifactKind == ArtifactRegular
	case ActionMount:
		mount, exists := mounts[valueOrEmpty(
			action.Mount,
			func(value *MountAction) string { return value.MountID },
		)]
		volume, volumeExists := volumes[mount.VolumeID]

		valid = exists && volumeExists && volume.Kind == VolumeEmptyDir &&
			strings.EqualFold(volume.Medium, "Memory") &&
			action.Mount.Filesystem == "tmpfs" && action.Mount.Source == "tmpfs" &&
			action.Phase == PhasePreStart && action.Target.ContainerID == mount.ContainerID
		if valid {
			for _, option := range action.Mount.Options {
				if option == "" || strings.ContainsAny(option, "\x00\n\r") {
					valid = false

					break
				}
			}
		}
	case ActionSysctl:
		valid = action.Sysctl != nil && action.Sysctl.Name != ""
	case ActionWaitInterface:
		waitInterfaceID := valueOrEmpty(
			action.WaitInterface,
			func(value *WaitInterfaceAction) string { return value.InterfaceID },
		)
		_, valid = interfaces[waitInterfaceID]
		valid = valid && action.WaitInterface.TimeoutSeconds > 0
	case ActionRenameInterface:
		renameInterfaceID := valueOrEmpty(
			action.RenameInterface,
			func(value *RenameInterfaceAction) string { return value.InterfaceID },
		)
		_, valid = interfaces[renameInterfaceID]
		valid = valid && action.RenameInterface.From != "" && action.RenameInterface.To != ""
	case ActionManagementForwarding:
		_, valid = management[valueOrEmpty(
			action.ManagementForwarding,
			func(value *ManagementForwardingAction) string { return value.ManagementID },
		)]
	case ActionImportedDeployEndpoints:
		valid = action.ImportedDeployEndpoints != nil && action.Phase == PhaseInterfaceFixup &&
			action.Target.ContainerID != "" && action.Target.NamespaceOwnerID != ""
	case ActionImportedPostDeploy:
		valid = action.ImportedPostDeploy != nil && action.Phase == PhasePostStart &&
			action.Target.ContainerID != ""
	case ActionImportedReadiness:
		valid = action.ImportedReadiness != nil && action.Phase == PhaseReadiness &&
			action.Target.ContainerID != ""
	case ActionSave:
		valid = action.Save != nil && action.Save.Method == SaveMethodImported &&
			action.Phase == PhaseSave && action.Target.ContainerID != ""
		if valid && action.Save.FileID != "" {
			file, exists := files[action.Save.FileID]
			valid = exists && file.ArtifactKind == ArtifactRegular
		}
	default:
		return planningError(ErrorUnsupported, field+".kind", "action kind is unsupported", nil)
	}

	if !valid {
		return planningError(
			ErrorInvariant,
			field,
			"typed action payload is incomplete or mismatched",
			nil,
		)
	}

	return nil
}

func validPayloadKind(kind PayloadKind) bool {
	return slices.Contains(
		[]PayloadKind{PayloadConfigMap, PayloadSecret, PayloadURL, PayloadInline},
		kind,
	)
}

func validFileSourceKind(kind FileSourceKind) bool {
	return slices.Contains(
		[]FileSourceKind{
			FileSourcePayload,
			FileSourceGenerator,
			FileSourceCertificate,
			FileSourceEmpty,
		},
		kind,
	)
}

func validVolumeKind(kind VolumeKind) bool {
	return slices.Contains(
		[]VolumeKind{
			VolumeArtifacts,
			VolumeEmptyDir,
			VolumeConfigMap,
			VolumeSecret,
			VolumePersistent,
			VolumeDevice,
		},
		kind,
	)
}

func validActionPhase(phase ActionPhase) bool {
	return slices.Contains(
		[]ActionPhase{
			PhasePrepare,
			PhasePreStart,
			PhaseInterfaceFixup,
			PhasePostStart,
			PhaseReadiness,
			PhaseSave,
			PhasePostStop,
		},
		phase,
	)
}

func validLinkApplyMode(mode LinkApplyMode) bool {
	return slices.Contains(
		[]LinkApplyMode{LinkApplyLive, LinkApplyRestart, LinkApplyRecreate},
		mode,
	)
}

func validDigest(value string) bool {
	if len(value) != sha256DigestLength || !strings.HasPrefix(value, "sha256:") {
		return false
	}

	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))

	return err == nil
}

func validateUniqueKeyValues(values []KeyValue, field string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if value.Name == "" || seen[value.Name] {
			return planningError(
				ErrorInvalidInput,
				field,
				"key names must be non-empty and unique",
				nil,
			)
		}

		seen[value.Name] = true
	}

	return nil
}

func hasDuplicates(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			return true
		}

		seen[value] = true
	}

	return false
}

func normalizeImageConfig(config *ImageConfig) {
	slices.SortFunc(config.Environment, compareKeyValue)
	slices.SortFunc(config.Labels, compareKeyValue)
	slices.SortFunc(config.Ports, comparePort)
	slices.Sort(config.DeclaredDirs)
}

func compareImageInput(left, right ImageInput) int {
	if compared := strings.Compare(left.NodeID, right.NodeID); compared != 0 {
		return compared
	}

	if compared := strings.Compare(left.Role, right.Role); compared != 0 {
		return compared
	}

	if compared := strings.Compare(left.SourceReference, right.SourceReference); compared != 0 {
		return compared
	}

	return strings.Compare(left.ComponentID, right.ComponentID)
}

func compareKeyValue(left, right KeyValue) int {
	return strings.Compare(left.Name, right.Name)
}

func comparePort(left, right Port) int {
	if left.Number < right.Number {
		return -1
	}

	if left.Number > right.Number {
		return 1
	}

	return strings.Compare(left.Protocol, right.Protocol)
}

func compareDevice(left, right Device) int {
	if compared := strings.Compare(left.ContainerPath, right.ContainerPath); compared != 0 {
		return compared
	}

	return strings.Compare(left.HostPath, right.HostPath)
}

func compareRoute(left, right Route) int {
	if compared := strings.Compare(left.Destination, right.Destination); compared != 0 {
		return compared
	}

	if compared := strings.Compare(left.Gateway, right.Gateway); compared != 0 {
		return compared
	}

	if left.Metric < right.Metric {
		return -1
	}

	if left.Metric > right.Metric {
		return 1
	}

	return 0
}

func compareAction(left, right Action) int {
	leftRank := actionPhaseRank(left.Phase)

	rightRank := actionPhaseRank(right.Phase)
	if leftRank < rightRank {
		return -1
	}

	if leftRank > rightRank {
		return 1
	}

	if left.Order < right.Order {
		return -1
	}

	if left.Order > right.Order {
		return 1
	}

	return strings.Compare(left.ID, right.ID)
}

func actionPhaseRank(phase ActionPhase) int {
	for index, candidate := range []ActionPhase{
		PhasePrepare,
		PhasePreStart,
		PhaseInterfaceFixup,
		PhasePostStart,
		PhaseReadiness,
		PhaseSave,
		PhasePostStop,
	} {
		if phase == candidate {
			return index
		}
	}

	return -1
}

func valueOrEmpty[T any](value *T, get func(*T) string) string {
	if value == nil {
		return ""
	}

	return get(value)
}

func marshalCanonical(value any, field string) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, planningError(ErrorSerialization, field, "cannot encode canonical JSON", err)
	}

	return raw, nil
}

func cloneJSON[T any](value T) (T, error) {
	var cloned T

	raw, err := json.Marshal(value)
	if err != nil {
		return cloned, err
	}

	if err = json.Unmarshal(raw, &cloned); err != nil {
		return cloned, err
	}

	return cloned, nil
}

func decodeStrict[T any](raw []byte, field string) (T, error) {
	var decoded T

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&decoded); err != nil {
		return decoded, planningError(ErrorSerialization, field, "cannot decode JSON", err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}

		return decoded, planningError(ErrorSerialization, field, "cannot decode JSON", err)
	}

	return decoded, nil
}

// Inbound management port protocols accepted by the interposition contract.
const (
	protocolTCP = "tcp"
	protocolUDP = "udp"
)

// validateManagementInterposition enforces the selector/contract pairing: an Interposed entry
// carries a complete translation contract, and no other entry carries one at all.
func validateManagementInterposition(field string, item ManagementPlan) error {
	if item.InterfaceSelector != ManagementInterfaceInterposed {
		if item.Interposition != nil {
			return planningError(
				ErrorInvalidInput,
				field+".interposition",
				"interposition contract requires the Interposed selector",
				nil,
			)
		}

		return nil
	}

	contract := item.Interposition
	if contract == nil || contract.DeviceInterface == "" ||
		len(contract.DeviceInterface) >= 16 {
		return planningError(
			ErrorInvalidInput,
			field+".interposition",
			"interposed management requires a valid device interface",
			nil,
		)
	}

	if item.IPv4 == "" || item.IPv4Gateway == "" {
		return planningError(
			ErrorInvalidInput,
			field+".interposition",
			"interposed management requires an allocated IPv4 identity and gateway",
			nil,
		)
	}

	if contract.DeviceMAC != "" {
		if _, err := net.ParseMAC(contract.DeviceMAC); err != nil {
			return planningError(
				ErrorInvalidInput,
				field+".interposition.deviceMAC",
				"device MAC is invalid",
				nil,
			)
		}
	}

	for _, cidr := range contract.TransportCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return planningError(
				ErrorInvalidInput,
				field+".interposition.transportCIDRs",
				"transport CIDR is invalid",
				nil,
			)
		}
	}

	for _, port := range contract.InboundPorts {
		if (port.Protocol != protocolTCP && port.Protocol != protocolUDP) ||
			port.PodPort == 0 || port.DevicePort == 0 {
			return planningError(
				ErrorInvalidInput,
				field+".interposition.inboundPorts",
				"inbound port map is invalid",
				nil,
			)
		}
	}

	return nil
}
