package deviceplan

import "encoding/json"

// SchemaVersion identifies both normalized planning inputs and device plans.
const SchemaVersion = "v1alpha1"

// ErrorCode is a stable machine-readable planning failure class.
type ErrorCode string

// Planning failure codes.
const (
	ErrorInvalidInput  ErrorCode = "InvalidInput"
	ErrorMissingInput  ErrorCode = "MissingInput"
	ErrorUnsupported   ErrorCode = "Unsupported"
	ErrorInvariant     ErrorCode = "Invariant"
	ErrorSideEffect    ErrorCode = "SideEffect"
	ErrorSerialization ErrorCode = "Serialization"
)

// Compatibility identifies the imported containerlab behavior and c9s plan schema.
type Compatibility struct {
	ContainerlabModule  string `json:"containerlabModule"`
	ContainerlabVersion string `json:"containerlabVersion"`
	RegistryDigest      string `json:"registryDigest"`
	PlanSchemaVersion   string `json:"planSchemaVersion"`
}

// Input is the complete explicit, side-effect-free input to device planning.
type Input struct {
	SchemaVersion string             `json:"schemaVersion"`
	TopologyName  string             `json:"topologyName"`
	Compatibility Compatibility      `json:"compatibility"`
	EntropyDigest string             `json:"entropyDigest,omitempty"`
	Nodes         []NodeInput        `json:"nodes"`
	Images        []ImageInput       `json:"images,omitempty"`
	Payloads      []PayloadInput     `json:"payloads,omitempty"`
	Certificates  []CertificateInput `json:"certificates,omitempty"`
	Management    []ManagementInput  `json:"management,omitempty"`
	Interfaces    []InterfaceInput   `json:"interfaces,omitempty"`
}

// NodeInput is one fully resolved logical Node. Definition is canonical JSON carrying the exact
// supported containerlab vocabulary; it contains identities and references, never secret bytes.
type NodeInput struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Type       string          `json:"type,omitempty"`
	GroupOwner string          `json:"groupOwner,omitempty"`
	Definition json.RawMessage `json:"definition"`
}

// ImageInput is explicit OCI metadata for one logical Node or component image.
type ImageInput struct {
	NodeID          string      `json:"nodeID"`
	Role            string      `json:"role,omitempty"`
	ComponentID     string      `json:"componentID,omitempty"`
	SourceReference string      `json:"sourceReference"`
	DigestReference string      `json:"digestReference"`
	Platform        Platform    `json:"platform"`
	Config          ImageConfig `json:"config"`
}

// Platform identifies one OCI image variant.
type Platform struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant,omitempty"`
	OSVersion    string   `json:"osVersion,omitempty"`
	OSFeatures   []string `json:"osFeatures,omitempty"`
}

// ImageConfig contains execution-relevant OCI configuration supplied to planning.
type ImageConfig struct {
	Entrypoint   []string     `json:"entrypoint,omitempty"`
	Command      []string     `json:"command,omitempty"`
	Environment  []KeyValue   `json:"environment,omitempty"`
	User         string       `json:"user,omitempty"`
	WorkingDir   string       `json:"workingDir,omitempty"`
	Ports        []Port       `json:"ports,omitempty"`
	StopSignal   string       `json:"stopSignal,omitempty"`
	Healthcheck  *Healthcheck `json:"healthcheck,omitempty"`
	Labels       []KeyValue   `json:"labels,omitempty"`
	DeclaredDirs []string     `json:"declaredDirectories,omitempty"`
}

// PayloadKind identifies an explicit payload source without embedding sensitive bytes.
type PayloadKind string

// Supported payload source kinds.
const (
	PayloadConfigMap PayloadKind = "ConfigMap"
	PayloadSecret    PayloadKind = "Secret"
	PayloadURL       PayloadKind = "URL"
	PayloadInline    PayloadKind = "Inline"
)

// PayloadInput identifies one input artifact. Reference is an object/key, URL, or content digest;
// inline content is supplied out of band and represented here only by Digest.
type PayloadInput struct {
	ID          string      `json:"id"`
	NodeID      string      `json:"nodeID"`
	Kind        PayloadKind `json:"kind"`
	Reference   string      `json:"reference"`
	Digest      string      `json:"digest,omitempty"`
	Destination string      `json:"destination"`
	Mode        uint32      `json:"mode,omitempty"`
	Sensitive   bool        `json:"sensitive,omitempty"`
}

// CertificateInput identifies package-requested certificate material projected into a worker.
// Only digests and the opaque package storage key enter the normalized input; certificate and
// private-key bytes remain in an independently mounted Kubernetes Secret.
type CertificateInput struct {
	NodeID              string `json:"nodeID"`
	StorageName         string `json:"storageName"`
	CertificateDigest   string `json:"certificateDigest"`
	PrivateKeyDigest    string `json:"privateKeyDigest"`
	CACertificateDigest string `json:"caCertificateDigest"`
	CAPrivateKeyDigest  string `json:"caPrivateKeyDigest"`
}

// ManagementInput is the controller-allocated management intent for one logical Node.
type ManagementInput struct {
	NodeID        string `json:"nodeID"`
	InterfaceName string `json:"interfaceName"`
	IPv4          string `json:"ipv4,omitempty"`
	IPv4Gateway   string `json:"ipv4Gateway,omitempty"`
	IPv6          string `json:"ipv6,omitempty"`
	IPv6Gateway   string `json:"ipv6Gateway,omitempty"`
	//nolint:modernize // the accepted schema freezes this tag and its serialization.
	DNS DNSConfig `json:"dns,omitempty"`
	// InboundPorts are additional controller-declared inbound destination ports (i.e. the
	// auto-expose default set) translated to the Node's interposed management address when the
	// port is not already claimed by a planned container port in the Pod.
	InboundPorts []Port `json:"inboundPorts,omitempty"`
	// Mesh is the controller-allocated management L2 mesh membership carried into the
	// interposition contract.
	Mesh *ManagementMesh `json:"mesh,omitempty"`
}

// Connectivity vocabulary for accepted Link endpoints, carried verbatim through the plan:
// c9s realization flavors resolved from endpoint shape alone.
const (
	ConnectivityWire     = "wire"
	ConnectivityHost     = "host"
	ConnectivitySamePod  = "same-pod"
	ConnectivityLoopback = "loopback"
)

// InterfaceInput is one accepted Link endpoint supplied before planning.
type InterfaceInput struct {
	ID            string `json:"id"`
	NodeID        string `json:"nodeID"`
	Name          string `json:"name"`
	LinkID        string `json:"linkID"`
	LinkName      string `json:"linkName,omitempty"`
	PeerNodeID    string `json:"peerNodeID,omitempty"`
	PeerInterface string `json:"peerInterface,omitempty"`
	PeerTransport string `json:"peerTransport,omitempty"`
	Connectivity  string `json:"connectivity"`
	WireID        int    `json:"wireID,omitempty"`
	MTU           int    `json:"mtu,omitempty"`
}

// Plan is a normalized runtime-neutral description rendered by c9s into Kubernetes resources.
type Plan struct {
	SchemaVersion string           `json:"schemaVersion"`
	Compatibility Compatibility    `json:"compatibility"`
	InputDigest   string           `json:"inputDigest"`
	Planner       PlannerIdentity  `json:"planner"`
	Nodes         []NodePlan       `json:"nodes"`
	Containers    []ContainerPlan  `json:"containers"`
	Files         []FilePlan       `json:"files,omitempty"`
	Volumes       []VolumePlan     `json:"volumes,omitempty"`
	Mounts        []MountPlan      `json:"mounts,omitempty"`
	Actions       []Action         `json:"actions,omitempty"`
	Management    []ManagementPlan `json:"management,omitempty"`
	Interfaces    []InterfacePlan  `json:"interfaces,omitempty"`
}

// PlannerIdentity identifies the c9s implementation that produced a plan.
type PlannerIdentity struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

// NodePlan maps one logical Node to its directly visible application containers. ContainerIDs is
// ordered: the first entry is the logical Node's primary application container and any remaining
// entries are imported components.
type NodePlan struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Kind                  string   `json:"kind"`
	Group                 string   `json:"group,omitempty"`
	Position              string   `json:"position,omitempty"`
	Aliases               []string `json:"aliases,omitempty"`
	ContainerIDs          []string `json:"containerIDs"`
	ReadinessContainerIDs []string `json:"readinessContainerIDs"`
}

// ContainerPlan is one kubelet-managed device or component application container.
type ContainerPlan struct {
	ID               string `json:"id"`
	NodeID           string `json:"nodeID"`
	RuntimeID        string `json:"runtimeID"`
	ComponentID      string `json:"componentID,omitempty"`
	NamespaceOwnerID string `json:"namespaceOwnerID"`
	Image            string `json:"image"`
	ImageDigest      string `json:"imageDigest,omitempty"`
	ImagePullPolicy  string `json:"imagePullPolicy,omitempty"`
	// ImagePullPolicyExplicit distinguishes imported Node intent from the package default so a
	// NodeProfile default never overwrites an explicitly declared policy.
	ImagePullPolicyExplicit bool       `json:"imagePullPolicyExplicit,omitempty"`
	ImageEntrypoint         []string   `json:"imageEntrypoint,omitempty"`
	ImageCommand            []string   `json:"imageCommand,omitempty"`
	Entrypoint              []string   `json:"entrypoint,omitempty"`
	Command                 []string   `json:"command,omitempty"`
	Environment             []KeyValue `json:"environment,omitempty"`
	Labels                  []KeyValue `json:"labels,omitempty"`
	User                    string     `json:"user,omitempty"`
	WorkingDir              string     `json:"workingDir,omitempty"`
	Ports                   []Port     `json:"ports,omitempty"`
	StopSignal              string     `json:"stopSignal,omitempty"`
	RestartPolicy           string     `json:"restartPolicy,omitempty"`
	StartupDelay            uint       `json:"startupDelaySeconds,omitempty"`
	TTY                     bool       `json:"tty,omitempty"`
	Stdin                   bool       `json:"stdin,omitempty"`
	//nolint:modernize // the accepted schema freezes this tag and its serialization.
	Security SecurityPlan `json:"security,omitempty"`
	//nolint:modernize // the accepted schema freezes this tag and its serialization.
	Resources ResourcePlan `json:"resources,omitempty"`
	//nolint:modernize // the accepted schema freezes this tag and its serialization.
	DNS         DNSConfig    `json:"dns,omitempty"`
	Healthcheck *Healthcheck `json:"healthcheck,omitempty"`
	Required    bool         `json:"required"`
	MountIDs    []string     `json:"mountIDs,omitempty"`
}

// KeyValue is a deterministically sortable string map entry.
type KeyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Port is one application destination port.
type Port struct {
	Number   int    `json:"number"`
	Protocol string `json:"protocol"`
}

// Healthcheck is an OCI-compatible process health contract.
type Healthcheck struct {
	Test        []string `json:"test,omitempty"`
	Interval    int64    `json:"intervalNanoseconds,omitempty"`
	Timeout     int64    `json:"timeoutNanoseconds,omitempty"`
	StartPeriod int64    `json:"startPeriodNanoseconds,omitempty"`
	Retries     int      `json:"retries,omitempty"`
}

// DNSConfig preserves ordered resolver behavior.
type DNSConfig struct {
	Servers []string `json:"servers,omitempty"`
	Search  []string `json:"search,omitempty"`
	Options []string `json:"options,omitempty"`
}

// SecurityPlan is portable container and Pod security intent.
type SecurityPlan struct {
	Privileged       bool       `json:"privileged,omitempty"`
	CapabilitiesAdd  []string   `json:"capabilitiesAdd,omitempty"`
	CapabilitiesDrop []string   `json:"capabilitiesDrop,omitempty"`
	Devices          []Device   `json:"devices,omitempty"`
	Sysctls          []KeyValue `json:"sysctls,omitempty"`
	SeccompProfile   string     `json:"seccompProfile,omitempty"`
	AppArmorProfile  string     `json:"appArmorProfile,omitempty"`
	ReadOnlyRootFS   bool       `json:"readOnlyRootFilesystem,omitempty"`
}

// Device is one explicit host device mapping.
type Device struct {
	HostPath      string `json:"hostPath"`
	ContainerPath string `json:"containerPath"`
	Permissions   string `json:"permissions,omitempty"`
}

// ResourcePlan uses runtime-neutral quantities interpreted by the Kubernetes renderer.
type ResourcePlan struct {
	CPURequest    string `json:"cpuRequest,omitempty"`
	CPULimit      string `json:"cpuLimit,omitempty"`
	MemoryRequest string `json:"memoryRequest,omitempty"`
	MemoryLimit   string `json:"memoryLimit,omitempty"`
	HugePages     string `json:"hugePages,omitempty"`
	CPUSet        string `json:"cpuSet,omitempty"`
}

// FileSourceKind identifies how preparation obtains a planned artifact without embedding bytes.
type FileSourceKind string

// Supported artifact sources.
const (
	FileSourcePayload     FileSourceKind = "Payload"
	FileSourceGenerator   FileSourceKind = "Generator"
	FileSourceCertificate FileSourceKind = "Certificate"
	FileSourceEmpty       FileSourceKind = "Empty"
)

// ArtifactKind distinguishes generic filesystem entries emitted by imported preparation.
type ArtifactKind string

// Supported prepared artifact kinds.
const (
	ArtifactRegular   ArtifactKind = "Regular"
	ArtifactSymlink   ArtifactKind = "Symlink"
	ArtifactDirectory ArtifactKind = "Directory"
)

// ExtendedAttribute identifies non-byte filesystem metadata without serializing its value.
// Preparation reruns the imported hook, verifies the digest, and applies the regenerated value.
type ExtendedAttribute struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// FilePlan stages one payload or generated artifact into the plan-owned artifact tree.
type FilePlan struct {
	ID                 string              `json:"id"`
	NodeID             string              `json:"nodeID"`
	SourceKind         FileSourceKind      `json:"sourceKind"`
	ArtifactKind       ArtifactKind        `json:"artifactKind,omitempty"`
	SourceReference    string              `json:"sourceReference,omitempty"`
	Digest             string              `json:"digest,omitempty"`
	ArtifactPath       string              `json:"artifactPath"`
	LinkTarget         string              `json:"linkTarget,omitempty"`
	Destination        string              `json:"destination"`
	Mode               uint32              `json:"mode,omitempty"`
	UID                *int64              `json:"uid,omitempty"`
	GID                *int64              `json:"gid,omitempty"`
	ExtendedAttributes []ExtendedAttribute `json:"extendedAttributes,omitempty"`
	Sensitive          bool                `json:"sensitive,omitempty"`
	PreserveExisting   bool                `json:"preserveExisting,omitempty"`
	Variables          []KeyValue          `json:"variables,omitempty"`
}

// VolumeKind identifies a portable volume realization.
type VolumeKind string

// Supported plan volume kinds.
const (
	VolumeArtifacts  VolumeKind = "Artifacts"
	VolumeEmptyDir   VolumeKind = "EmptyDir"
	VolumeConfigMap  VolumeKind = "ConfigMap"
	VolumeSecret     VolumeKind = "Secret"
	VolumePersistent VolumeKind = "Persistent"
	VolumeDevice     VolumeKind = "Device"
)

// VolumePlan describes runtime-neutral storage intent.
type VolumePlan struct {
	ID        string     `json:"id"`
	NodeID    string     `json:"nodeID"`
	Kind      VolumeKind `json:"kind"`
	Reference string     `json:"reference,omitempty"`
	Size      string     `json:"size,omitempty"`
	Medium    string     `json:"medium,omitempty"`
}

// MountPlan maps one planned volume into one application container.
type MountPlan struct {
	ID          string `json:"id"`
	ContainerID string `json:"containerID"`
	VolumeID    string `json:"volumeID"`
	SourcePath  string `json:"sourcePath,omitempty"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	Propagation string `json:"propagation,omitempty"`
}

// ActionPhase orders typed lifecycle work around application-container execution.
type ActionPhase string

// Supported lifecycle phases.
const (
	PhasePrepare        ActionPhase = "Prepare"
	PhasePreStart       ActionPhase = "PreStart"
	PhasePostStart      ActionPhase = "PostStart"
	PhaseReadiness      ActionPhase = "Readiness"
	PhaseInterfaceFixup ActionPhase = "InterfaceFixup"
	PhaseSave           ActionPhase = "Save"
	PhasePostStop       ActionPhase = "PostStop"
)

// ActionKind discriminates the single typed payload carried by an Action.
type ActionKind string

// Supported typed action kinds.
const (
	ActionExec                    ActionKind = "Exec"
	ActionFile                    ActionKind = "File"
	ActionWriteStdin              ActionKind = "WriteStdin"
	ActionMount                   ActionKind = "Mount"
	ActionSysctl                  ActionKind = "Sysctl"
	ActionWaitInterface           ActionKind = "WaitInterface"
	ActionRenameInterface         ActionKind = "RenameInterface"
	ActionManagementForwarding    ActionKind = "ManagementForwarding"
	ActionImportedDeployEndpoints ActionKind = "ImportedDeployEndpoints"
	ActionImportedPostDeploy      ActionKind = "ImportedPostDeploy"
	ActionImportedReadiness       ActionKind = "ImportedReadiness"
	ActionSave                    ActionKind = "Save"
	// SaveMethodImported delegates save behavior to the Node implementation from the pinned
	// containerlab package. It is a generic lifecycle boundary, not a kind identifier.
	SaveMethodImported = "ImportedPackage"
)

// Action is one ordered lifecycle action. Exactly one payload matching Kind is required.
type Action struct {
	ID                      string                         `json:"id"`
	Phase                   ActionPhase                    `json:"phase"`
	Order                   int                            `json:"order"`
	Target                  ActionTarget                   `json:"target"`
	Kind                    ActionKind                     `json:"kind"`
	Exec                    *ExecAction                    `json:"exec,omitempty"`
	File                    *FileAction                    `json:"file,omitempty"`
	WriteStdin              *WriteStdinAction              `json:"writeStdin,omitempty"`
	Mount                   *MountAction                   `json:"mount,omitempty"`
	Sysctl                  *SysctlAction                  `json:"sysctl,omitempty"`
	WaitInterface           *WaitInterfaceAction           `json:"waitInterface,omitempty"`
	RenameInterface         *RenameInterfaceAction         `json:"renameInterface,omitempty"`
	ManagementForwarding    *ManagementForwardingAction    `json:"managementForwarding,omitempty"`
	ImportedDeployEndpoints *ImportedDeployEndpointsAction `json:"importedDeployEndpoints,omitempty"`
	ImportedPostDeploy      *ImportedPostDeployAction      `json:"importedPostDeploy,omitempty"`
	ImportedReadiness       *ImportedReadinessAction       `json:"importedReadiness,omitempty"`
	Save                    *SaveAction                    `json:"save,omitempty"`
}

// ImportedDeployEndpointsAction delegates endpoint deployment and post-deployment fixups to the
// pinned containerlab package after c9s has realized the declared interfaces. The empty payload
// keeps all kind behavior behind the imported generic Node hooks.
type ImportedDeployEndpointsAction struct{}

// ImportedPostDeployAction delegates the opaque post-deployment lifecycle to the pinned
// containerlab package inside the already-running direct application container. The empty
// payload is intentional: all kind behavior remains in the imported registry implementation.
type ImportedPostDeployAction struct{}

// ActionTarget identifies a logical Node, application container, and optional namespace owner.
type ActionTarget struct {
	NodeID           string `json:"nodeID"`
	ContainerID      string `json:"containerID,omitempty"`
	NamespaceOwnerID string `json:"namespaceOwnerID,omitempty"`
}

// ExecAction executes an existing user- or kind-declared command without a shell by default.
type ExecAction struct {
	Command []string `json:"command"`
	Shell   bool     `json:"shell,omitempty"`
	Wait    bool     `json:"wait"`
	// ContinueOnError keeps the lifecycle phase running when the command fails. The topology's
	// own exec list is best effort in containerlab -- a failing command is reported and the node
	// keeps running -- and a PostStart hook that fails would instead take the whole container
	// down. Commands a kind's own deployment recorded stay fail-closed.
	ContinueOnError bool `json:"continueOnError,omitempty"`
	TimeoutSeconds  int  `json:"timeoutSeconds,omitempty"`
}

// FileWriteMode describes how one staged artifact is applied to an existing container path.
type FileWriteMode string

// Supported lifecycle file-write modes.
const (
	FileWriteReplace FileWriteMode = "Replace"
	FileWriteAppend  FileWriteMode = "Append"
)

// FileAction copies one staged FilePlan to its declared target or an operation-specific override.
type FileAction struct {
	FileID      string        `json:"fileID"`
	Destination string        `json:"destination,omitempty"`
	WriteMode   FileWriteMode `json:"writeMode,omitempty"`
}

// WriteStdinAction streams one staged FilePlan to a running container's standard input.
type WriteStdinAction struct {
	FileID string `json:"fileID"`
}

// MountAction realizes one planned filesystem mount synchronously before the application
// process starts. Options are opaque filesystem options emitted through generic containerlab
// configuration; the runtime helper, rather than any kind-specific c9s code, interprets them.
type MountAction struct {
	MountID    string   `json:"mountID"`
	Filesystem string   `json:"filesystem"`
	Source     string   `json:"source"`
	Options    []string `json:"options,omitempty"`
}

// SysctlAction applies one namespaced sysctl.
type SysctlAction struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// WaitInterfaceAction waits for one named interface before the bounded timeout.
type WaitInterfaceAction struct {
	InterfaceID    string `json:"interfaceID"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// RenameInterfaceAction renames one planned interface inside its target namespace.
type RenameInterfaceAction struct {
	InterfaceID string `json:"interfaceID"`
	From        string `json:"from"`
	To          string `json:"to"`
}

// ManagementForwardingAction configures management namespace routing explicitly.
type ManagementForwardingAction struct {
	ManagementID string `json:"managementID"`
	Namespace    string `json:"namespace,omitempty"`
	IPv4Forward  bool   `json:"ipv4Forward,omitempty"`
}

// ImportedReadinessAction invokes the initialized Node's package-owned IsHealthy hook from
// inside its directly running application container. The empty payload is intentional: all
// behavior and kind-specific inputs remain in the imported package and normalized Input.
type ImportedReadinessAction struct{}

// SaveAction records a supported configuration save method and destination FilePlan.
type SaveAction struct {
	Method string `json:"method"`
	FileID string `json:"fileID,omitempty"`
}

// LinkApplyMode declares the lifecycle needed for a Link change.
type LinkApplyMode string

// Supported Link-change modes, from least to most disruptive.
const (
	LinkApplyLive     LinkApplyMode = "Live"
	LinkApplyRestart  LinkApplyMode = "Restart"
	LinkApplyRecreate LinkApplyMode = "Recreate"
)

// ManagementPlan is the realized management intent for one logical Node.
type ManagementPlan struct {
	ID                string                      `json:"id"`
	NodeID            string                      `json:"nodeID"`
	InterfaceName     string                      `json:"interfaceName,omitempty"`
	InterfaceSelector ManagementInterfaceSelector `json:"interfaceSelector,omitempty"`
	IPv4              string                      `json:"ipv4,omitempty"`
	IPv4Gateway       string                      `json:"ipv4Gateway,omitempty"`
	IPv6              string                      `json:"ipv6,omitempty"`
	IPv6Gateway       string                      `json:"ipv6Gateway,omitempty"`
	Routes            []Route                     `json:"routes,omitempty"`
	//nolint:modernize // the accepted schema freezes this tag and its serialization.
	DNS DNSConfig `json:"dns,omitempty"`
	// Interposition is the sidecar interposition contract; it is present exactly when
	// InterfaceSelector is Interposed.
	Interposition *ManagementInterposition `json:"interposition,omitempty"`
}

// ManagementInterposition is the vendor-neutral sidecar interposition contract for one
// management identity: the synthetic device-leg interface presented to the device and the
// translation surface at the Pod boundary. Its values are derived from the pinned containerlab
// dependency, never from kind-conditional code.
type ManagementInterposition struct {
	// DeviceInterface is the interface name the device expects for its management port.
	DeviceInterface string `json:"deviceInterface"`
	// DeviceMAC optionally pins the device-leg MAC address.
	DeviceMAC string `json:"deviceMAC,omitempty"`
	// TransportCIDRs are cluster destinations whose routing the sidecar keeps on the preserved
	// Kubernetes underlay.
	TransportCIDRs []string `json:"transportCIDRs,omitempty"`
	// InboundPorts are declared management ports translated from the Pod address to the device
	// management address.
	InboundPorts []ManagementPortMap `json:"inboundPorts,omitempty"`
	// Mesh is the Pod's membership in the topology's management L2 domain; when present the
	// sidecar bridges the device leg, the gateway leg, and a management VTEP so peer management
	// addresses are reachable device-to-device.
	Mesh *ManagementMesh `json:"mesh,omitempty"`
}

// ManagementPortMap is one declared inbound management port translation.
type ManagementPortMap struct {
	// Protocol is "tcp" or "udp".
	Protocol string `json:"protocol"`
	// PodPort is the port clients dial on the Pod address.
	PodPort uint16 `json:"podPort"`
	// DevicePort is the port the device binds on its management address.
	DevicePort uint16 `json:"devicePort"`
}

// ManagementMesh is controller-allocated membership in the management L2 domain shared by a
// namespace's interposed Pods.
type ManagementMesh struct {
	// TunnelID is the VNI of the management mesh, derived deterministically from the
	// namespace; Link wire ids travel a different port and plane and cannot reach the mesh.
	TunnelID int `json:"tunnelID"`
	// GatewayMAC is the deterministic gateway link-layer identity shared by every Pod of the
	// namespace.
	GatewayMAC string `json:"gatewayMAC"`
	// PeerService is the stable transport (Service DNS) name resolving to every mesh member
	// Pod; the sidecar discovers the current peer set through it on the revision tick.
	PeerService string `json:"peerService"`
}

// ManagementInterfaceSelector identifies a generic runtime-owned interface when the imported
// package intentionally leaves its concrete management interface unset.
type ManagementInterfaceSelector string

// Supported management interface selectors.
const (
	ManagementInterfacePodTransport ManagementInterfaceSelector = "PodTransport"
	// ManagementInterfaceInterposed selects the sidecar-interposed synthetic management
	// interface described by the entry's Interposition contract.
	ManagementInterfaceInterposed ManagementInterfaceSelector = "Interposed"
)

// Route is one explicit management route.
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Metric      int    `json:"metric,omitempty"`
}

// InterfacePlan describes one direct Link endpoint and its change lifecycle.
type InterfacePlan struct {
	ID               string        `json:"id"`
	NodeID           string        `json:"nodeID"`
	NamespaceOwnerID string        `json:"namespaceOwnerID"`
	Name             string        `json:"name"`
	Alias            string        `json:"alias,omitempty"`
	LinkID           string        `json:"linkID"`
	LinkName         string        `json:"linkName,omitempty"`
	PeerNodeID       string        `json:"peerNodeID,omitempty"`
	PeerInterface    string        `json:"peerInterface,omitempty"`
	PeerTransport    string        `json:"peerTransport,omitempty"`
	Connectivity     string        `json:"connectivity"`
	WireID           int           `json:"wireID,omitempty"`
	MTU              int           `json:"mtu,omitempty"`
	LinkApplyMode    LinkApplyMode `json:"linkApplyMode"`
	RequiredAtStart  bool          `json:"requiredAtStart,omitempty"`
}
