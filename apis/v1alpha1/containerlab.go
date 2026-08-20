package v1alpha1

import (
	"encoding/json"
	"fmt"

	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// This file holds the containerlab *vocabulary* -- the node definition (and its sub objects) as
// a user would write it in a containerlab topology file. These types carry both json and yaml
// tags with identical (containerlab native) names: the json side is what the Node CRD schema is
// built from and what planning serializes for the imported containerlab module, the yaml side is
// what the topology compiler parses. util/containerlab aliases these types so there is exactly
// one definition of the vocabulary in the codebase.

// NodeDefinition represents a configuration a given node can have in a (containerlab) lab
// definition file. It is a *curated subset* of the containerlab node vocabulary: a field lives
// here only if the direct runtime can realize its semantics in a Kubernetes Pod. Deliberately
// absent are Docker-runtime selection and lifecycle vocabulary (`runtime`, `auto-remove`,
// `pid-mode`, `cgroupns-mode`, `cpu-set`), multi-node boot orchestration (`stages`), and
// credential bytes (`credentials`) -- the topology compiler rejects each of those with a
// structured diagnostic naming the field. This is also, verbatim, the containerlab portion of
// the clabernetes Node custom resource spec.
type NodeDefinition struct {
	// Kind is the containerlab kind of the node -- i.e. nokia_srlinux.
	// +optional
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
	// Type is the type of the node -- i.e. ixrd2.
	// +optional
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	// Image is the container image for the node.
	// +optional
	Image string `json:"image,omitempty" yaml:"image,omitempty"`
	// ImagePullPolicy is the containerlab pull policy for the node's image; it maps onto the
	// equivalent Kubernetes image pull policy and takes precedence over profile and global
	// defaults.
	// +kubebuilder:validation:Enum=always;Always;never;Never;ifnotpresent;IfNotPresent
	// +optional
	ImagePullPolicy string `json:"image-pull-policy,omitempty" yaml:"image-pull-policy,omitempty"`
	// License is the path to the license file for the node.
	// +optional
	License string `json:"license,omitempty" yaml:"license,omitempty"`
	// StartupConfig is the startup configuration for the node -- either a path or an inline
	// (multiline string) config.
	// +optional
	StartupConfig string `json:"startup-config,omitempty" yaml:"startup-config,omitempty"`
	// EnforceStartupConfig makes the node boot from StartupConfig even when it already has a
	// saved (persisted) configuration.
	// +optional
	EnforceStartupConfig *bool `json:"enforce-startup-config,omitempty" yaml:"enforce-startup-config,omitempty"`
	// SuppressStartupConfig boots the node with its factory configuration -- containerlab
	// generates and mounts no startup config at all.
	// +optional
	SuppressStartupConfig *bool `json:"suppress-startup-config,omitempty" yaml:"suppress-startup-config,omitempty"`
	// StartupDelay is an optional delay in seconds applied before the node's application
	// container starts.
	// +optional
	StartupDelay uint `json:"startup-delay,omitempty" yaml:"startup-delay,omitempty"`
	// RestartPolicy is the container restart policy for the node. Only the values with a
	// shared-Pod mapping are accepted -- a device container in a direct Pod always restarts
	// with its Pod, so Docker's "no" and "on-failure" policies cannot be represented.
	// +kubebuilder:validation:Enum=always;Always;unless-stopped;Unless-stopped
	// +optional
	RestartPolicy string `json:"restart-policy,omitempty" yaml:"restart-policy,omitempty"`
	// Config holds containerlab config engine settings for the node.
	// +optional
	Config *ConfigDispatcher `json:"config,omitempty" yaml:"config,omitempty"`
	// Entrypoint overrides the container entrypoint.
	// +optional
	Entrypoint string `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
	// Cmd overrides the container command.
	// +optional
	Cmd string `json:"cmd,omitempty" yaml:"cmd,omitempty"`
	// Exec is a list of commands to run in the node's container once it is started.
	// +listType=atomic
	// +optional
	Exec []string `json:"exec,omitempty" yaml:"exec,omitempty"`
	// User is the linux user to use in the node's container.
	// +optional
	User string `json:"user,omitempty" yaml:"user,omitempty"`
	// Binds is a list of bind (mount) compatible strings.
	// +listType=atomic
	// +optional
	Binds []string `json:"binds,omitempty" yaml:"binds,omitempty"`
	// Devices is a list of host devices to map into the node's container.
	// +listType=atomic
	// +optional
	Devices []string `json:"devices,omitempty" yaml:"devices,omitempty"`
	// CapAdd is a list of linux capabilities to add to the node's container.
	// +listType=atomic
	// +optional
	CapAdd []string `json:"cap-add,omitempty" yaml:"cap-add,omitempty"`
	// Privileged runs the node's container in privileged mode.
	// +optional
	Privileged *bool `json:"privileged,omitempty" yaml:"privileged,omitempty"`
	// SecurityOpts is a list of security options (i.e. seccomp or apparmor settings) to apply to
	// the node's container.
	// +listType=atomic
	// +optional
	SecurityOpts []string `json:"security-opts,omitempty" yaml:"security-opts,omitempty"`
	// Tmpfs holds tmpfs mounts for the node's container, keyed by destination path.
	// +optional
	Tmpfs map[string]string `json:"tmpfs,omitempty" yaml:"tmpfs,omitempty"`
	// ShmSize is the shared memory size allocated to the node's container -- i.e. 256m.
	// +optional
	ShmSize string `json:"shm-size,omitempty" yaml:"shm-size,omitempty"`
	// CPU is the number of vcpus to allocate for the node's container -- it becomes the
	// container's Kubernetes CPU limit.
	// +kubebuilder:validation:Minimum=0
	// +optional
	CPU float64 `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	// Memory is the memory limit for the node's container in containerlab's human-readable
	// form -- i.e. 1Gb -- and becomes the container's Kubernetes memory limit.
	// +optional
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`
	// the ports pattern spells out 1-65535 as an alternation so the range is enforced by the
	// pattern alone: a CEL rule over an unbounded list of unbounded strings blows the
	// apiserver's estimated cost budget and the whole CRD is then rejected at install time

	// Ports lists additional ports to expose on this node. Each entry is a destination port
	// between 1 and 65535 with an optional protocol -- "8080" or "5201/udp" -- meaning the port
	// the node itself listens on. clabernetes allocates the pod side port that carries it and
	// records both in the Node status, so docker style "host:container" bindings are rejected
	// here. Unless auto expose is disabled, the default set of management ports is exposed in
	// addition to these.
	// +kubebuilder:validation:items:Pattern=`^([1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])(/(tcp|udp|TCP|UDP))?$`
	// +listType=atomic
	// +optional
	Ports []string `json:"ports,omitempty" yaml:"ports"`
	// MgmtIPv4 is the user-defined IPv4 address of the node in the management network.
	// +optional
	MgmtIPv4 string `json:"mgmt-ipv4,omitempty" yaml:"mgmt-ipv4,omitempty"`
	// MgmtIPv6 is the user-defined IPv6 address of the node in the management network.
	// +optional
	MgmtIPv6 string `json:"mgmt-ipv6,omitempty" yaml:"mgmt-ipv6,omitempty"`
	// the network-mode length bound is what keeps its CEL rule inside the apiserver's estimated
	// cost budget: "container:" plus a node name, which is an RFC1123 label, so 10 + 63

	// NetworkMode declares that this node shares the network namespace -- and therefore the
	// device pod -- of the named primary node. `container:<primary node name>` is the only
	// accepted value: clabernetes derives node "grouping" (nodes co-located in one device pod,
	// i.e. the cards of a distributed chassis) from exactly this containerlab-native field, and
	// the other containerlab network modes have no meaning inside a device pod.
	// +kubebuilder:validation:MaxLength=73
	// +kubebuilder:validation:XValidation:rule="self.matches('^container:[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="network-mode must be container:<primary node name> -- it declares that this node shares its device pod with the named primary node"
	// +optional
	NetworkMode string `json:"network-mode,omitempty" yaml:"network-mode,omitempty"`
	// Env holds the environment variables for the node's container.
	// +optional
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	// EnvFiles is a list of external files containing environment variables for the node.
	// +listType=atomic
	// +optional
	EnvFiles []string `json:"env-files,omitempty" yaml:"env-files,omitempty"`
	// Sysctls holds sysctl settings for the node.
	// +optional
	Sysctls map[string]string `json:"sysctls,omitempty" yaml:"sysctls,omitempty"`
	// Group names the containerlab group a node belongs to. Group-scoped configuration from
	// the topology's groups section participates in the imported inheritance rules, and the
	// name itself is carried onto the compiled Node as a label.
	// +optional
	Group string `json:"group,omitempty" yaml:"group,omitempty"`
	// Labels are containerlab node labels, and exist only so a Topology definition can carry
	// them: the compiler copies them onto the emitted Node's metadata.labels, from where they
	// reach the device deployment and its pods. There is deliberately no spec.labels on a Node
	// (hence json:"-") -- in Kubernetes, metadata.labels is where labels belong, and unlike
	// containerlab's Docker labels these are selectable with kubectl. Invalid labels and keys
	// reserved by c9s make Topology compilation fail before any primitive is emitted.
	// +optional
	Labels map[string]string `json:"-" yaml:"labels,omitempty"`
	// DNS holds the DNS configuration for the node.
	// +optional
	DNS *DNSConfig `json:"dns,omitempty" yaml:"dns,omitempty"`
	// Certificate holds the TLS certificate configuration for the node.
	// +optional
	Certificate *CertificateConfig `json:"certificate,omitempty" yaml:"certificate,omitempty"`
	// Healthcheck is the containerlab process health contract for the node; it merges over the
	// image-defined OCI healthcheck and is realized as container startup/readiness behavior.
	// +optional
	Healthcheck *HealthcheckConfig `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
	// Aliases lists additional network names for the node. Each alias is realized as an extra
	// same-namespace headless Service selecting the node's Pod, so lab members resolve the
	// alias exactly like the node's own name. Aliases do not inherit from defaults or kinds.
	// +kubebuilder:validation:items:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=atomic
	// +optional
	Aliases []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	// LinkApplyMode declares the lifecycle action used when the node's links change: live (no
	// lifecycle action), restart, or recreate. It overrides the kind's imported default.
	// +kubebuilder:validation:Enum=live;restart;recreate
	// +optional
	LinkApplyMode string `json:"link-apply-mode,omitempty" yaml:"link-apply-mode,omitempty"`
	// Extras holds extra, possibly kind specific, node parameters.
	// +optional
	Extras *Extras `json:"extras,omitempty" yaml:"extras,omitempty"`
	// Components holds the hardware component (i.e. SR-OS card/mda) configuration for the node.
	// +listType=atomic
	// +optional
	Components []*Component `json:"components,omitempty" yaml:"components,omitempty"`
}

// Vars is a mapping of containerlab config engine variable name to (arbitrary, so json.RawMessage
// style) value.
type Vars map[string]apiextensionsv1.JSON //nolint: recvcheck

// MarshalYAML implements yaml marshalling for Vars -- the raw json values are unpacked so the
// rendered (containerlab) yaml holds the plain values rather than the k8s JSON wrapper type.
func (v Vars) MarshalYAML() (any, error) {
	out := make(map[string]any, len(v))

	for key := range v {
		var value any

		err := json.Unmarshal(v[key].Raw, &value)
		if err != nil {
			return nil, err
		}

		out[key] = value
	}

	return out, nil
}

// UnmarshalYAML implements yaml unmarshalling for Vars -- see also MarshalYAML.
func (v *Vars) UnmarshalYAML(unmarshal func(any) error) error {
	values := map[string]any{}

	err := unmarshal(&values)
	if err != nil {
		return err
	}

	out := make(map[string]apiextensionsv1.JSON, len(values))

	for key := range values {
		raw, marshalErr := json.Marshal(values[key])
		if marshalErr != nil {
			return marshalErr
		}

		out[key] = apiextensionsv1.JSON{Raw: raw}
	}

	*v = out

	return nil
}

// ConfigDispatcher represents the config of a configuration machine that is responsible to
// execute configuration commands on the nodes after they started.
type ConfigDispatcher struct {
	// Vars holds the variables for the config engine.
	// +optional
	Vars Vars `json:"vars,omitempty" yaml:"vars,omitempty"`
}

// Extras contains extra node parameters which are not entitled to be part of a generic node
// config.
type Extras struct {
	// SRLAgents is a list of Nokia SR Linux agents (spec files) to install on the node.
	// +listType=atomic
	// +optional
	SRLAgents []string `json:"srl-agents,omitempty" yaml:"srl-agents,omitempty"`
	// CeosCopyToFlash is a list of paths to files which are to be copied to the ceos flash dir.
	// +listType=atomic
	// +optional
	CeosCopyToFlash []string `json:"ceos-copy-to-flash,omitempty" yaml:"ceos-copy-to-flash,omitempty"`
}

// DNSConfig represents DNS configuration options a node has.
type DNSConfig struct {
	// Servers is a list of DNS servers.
	// +listType=atomic
	// +optional
	Servers []string `json:"servers,omitempty" yaml:"servers,omitempty"`
	// Options is a list of DNS options.
	// +listType=atomic
	// +optional
	Options []string `json:"options,omitempty" yaml:"options,omitempty"`
	// Search is a list of DNS search domains.
	// +listType=atomic
	// +optional
	Search []string `json:"search,omitempty" yaml:"search,omitempty"`
}

// HealthcheckConfig represents a containerlab process health contract for a node.
type HealthcheckConfig struct {
	// Test is the command to run to check the health of the container -- the first element
	// selects the containerlab test form (i.e. CMD) and the remainder is the command itself.
	// +listType=atomic
	// +optional
	Test []string `json:"test,omitempty" yaml:"test,omitempty"`
	// Interval is the time in seconds to wait between checks.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Interval int `json:"interval,omitempty" yaml:"interval,omitempty"`
	// Timeout is the time in seconds to wait before considering a check to have hung.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	// Retries is the number of consecutive failures needed to consider the container unhealthy.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Retries int `json:"retries,omitempty" yaml:"retries,omitempty"`
	// StartPeriod is the time in seconds to wait for the container to initialize before the
	// retries countdown starts.
	// +kubebuilder:validation:Minimum=0
	// +optional
	StartPeriod int `json:"start-period,omitempty" yaml:"start-period,omitempty"`
}

// CertificateConfig represents the configuration of a TLS infrastructure used by a node.
type CertificateConfig struct {
	// Issue indicates if the node should have a certificate issued -- when unset the node does
	// not use TLS.
	// +optional
	Issue *bool `json:"issue,omitempty" yaml:"issue,omitempty"`
	// KeySize is the size of the certificate key in bits.
	// +optional
	KeySize int `json:"key-size,omitempty" yaml:"key-size,omitempty"`
	// ValidityDuration is how long the issued certificate is valid for, as a Go duration --
	// i.e. 8760h for a year.
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`
	// +optional
	ValidityDuration string `json:"validity-duration,omitempty" yaml:"validity-duration,omitempty"`
	// SANs is the list of subject alternative names to add to the node's certificate.
	// +listType=atomic
	// +optional
	SANs []string `json:"sans,omitempty" yaml:"sans,omitempty"`
}

// Component holds a hardware component configuration (i.e. an SR-OS card or mda).
type Component struct {
	// Slot is the slot identifier of the component.
	// +optional
	Slot string `json:"slot,omitempty" yaml:"slot,omitempty"`
	// Type is the type of the component.
	// +optional
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	// Env holds environment variables for the component.
	// +optional
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	// SFM is the SFM (switch fabric module) of the component.
	// +optional
	SFM string `json:"sfm,omitempty" yaml:"sfm,omitempty"`
	// XIOM holds the xiom configuration of the component.
	// +optional
	XIOM XIOMS `json:"xiom,omitempty" yaml:"xiom,omitempty"`
	// MDA holds the mda configuration of the component.
	// +optional
	MDA MDAS `json:"mda,omitempty" yaml:"mda,omitempty"`
}

// XIOM holds a single xiom configuration of a hardware component.
type XIOM struct {
	// Slot is the slot of the xiom.
	// +optional
	Slot int `json:"slot,omitempty" yaml:"slot,omitempty"`
	// Type is the type of the xiom.
	// +optional
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	// MDA holds the mda configuration of the xiom.
	// +optional
	MDA MDAS `json:"mda,omitempty" yaml:"mda,omitempty"`
}

// XIOMS is a list of XIOM objects.
type XIOMS []XIOM //nolint: recvcheck

// MDA holds a single mda configuration of a hardware component.
type MDA struct {
	// Slot is the slot of the mda.
	// +optional
	Slot int `json:"slot,omitempty" yaml:"slot,omitempty"`
	// Type is the type of the mda.
	// +optional
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
}

// MDAS is a list of MDA objects.
type MDAS []MDA //nolint: recvcheck

// UnmarshalYAML implements yaml unmarshalling with validation for MDAS.
func (l *MDAS) UnmarshalYAML(unmarshal func(any) error) error {
	var entries []MDA

	err := unmarshal(&entries)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		*l = nil

		return nil
	}

	slots := map[int]struct{}{}

	for _, e := range entries {
		if e.Type == "" || e.Slot <= 0 {
			return fmt.Errorf(
				"%w: invalid mda entry. slot and type are required, got slot %q, type %q",
				claberneteserrors.ErrInvalidData,
				e.Slot,
				e.Type,
			)
		}

		if _, exists := slots[e.Slot]; exists {
			return fmt.Errorf(
				"%w: invalid mda entry. duplicate slot %d",
				claberneteserrors.ErrInvalidData,
				e.Slot,
			)
		}

		slots[e.Slot] = struct{}{}
	}

	*l = MDAS(entries)

	return nil
}

// UnmarshalYAML implements yaml unmarshalling with validation for XIOMS.
func (l *XIOMS) UnmarshalYAML(unmarshal func(any) error) error {
	var entries []XIOM

	err := unmarshal(&entries)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		*l = nil

		return nil
	}

	slots := map[int]struct{}{}

	for _, e := range entries {
		if e.Type == "" || e.Slot <= 0 {
			return fmt.Errorf(
				"%w: invalid xiom entry. slot and type are required, got slot %q, type %q",
				claberneteserrors.ErrInvalidData,
				e.Slot,
				e.Type,
			)
		}

		if _, exists := slots[e.Slot]; exists {
			return fmt.Errorf(
				"%w: invalid xiom entry. duplicate slot %d",
				claberneteserrors.ErrInvalidData,
				e.Slot,
			)
		}

		slots[e.Slot] = struct{}{}
	}

	*l = XIOMS(entries)

	return nil
}
