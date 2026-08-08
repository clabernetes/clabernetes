package v1alpha1

import (
	"encoding/json"
	"fmt"

	claberneteserrors "github.com/srl-labs/clabernetes/errors"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// This file holds the containerlab *vocabulary* -- the node definition (and its sub objects) as
// a user would write it in a containerlab topology file. These types carry both json and yaml
// tags with identical (containerlab native) names: the json side is what the Node CRD schema is
// built from, the yaml side is what the launcher marshals into the topo.clab.yaml it feeds to
// containerlab. util/containerlab aliases these types so there is exactly one definition of the
// vocabulary in the codebase.

// NodeDefinition represents a configuration a given node can have in a (containerlab) lab
// definition file. This is also, verbatim, the containerlab portion of the clabernetes Node
// custom resource spec.
type NodeDefinition struct {
	// Kind is the containerlab kind of the node -- i.e. nokia_srlinux.
	// +optional
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
	// Group is the (containerlab) group of the node.
	// +optional
	Group string `json:"group,omitempty" yaml:"group,omitempty"`
	// Type is the type of the node -- i.e. ixrd2.
	// +optional
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	// StartupConfig is the startup configuration for the node -- either a path or an inline
	// (multiline string) config.
	// +optional
	StartupConfig string `json:"startup-config,omitempty" yaml:"startup-config,omitempty"`
	// StartupDelay is the delay (in seconds) to wait before starting the node.
	// +optional
	StartupDelay uint `json:"startup-delay,omitempty" yaml:"startup-delay,omitempty"`
	// EnforceStartupConfig enforces the startup config even if the node has a saved config.
	// +optional
	EnforceStartupConfig bool `json:"enforce-startup-config,omitempty" yaml:"enforce-startup-config,omitempty"` //nolint:lll
	// AutoRemove enables the auto removal of the node's container when it stops.
	// +optional
	AutoRemove *bool `json:"auto-remove,omitempty" yaml:"auto-remove,omitempty"`
	// Config holds containerlab config engine settings for the node.
	// +optional
	Config *ConfigDispatcher `json:"config,omitempty" yaml:"config,omitempty"`
	// Image is the container image for the node.
	// +optional
	Image string `json:"image,omitempty" yaml:"image,omitempty"`
	// ImagePullPolicy is the (containerlab, so docker) image pull policy for the node.
	// +optional
	ImagePullPolicy string `json:"image-pull-policy,omitempty" yaml:"image-pull-policy,omitempty"`
	// License is the path to the license file for the node.
	// +optional
	License string `json:"license,omitempty" yaml:"license,omitempty"`
	// Position is the position of the node (used by graphing tooling).
	// +optional
	Position string `json:"position,omitempty" yaml:"position,omitempty"`
	// Entrypoint overrides the container entrypoint.
	// +optional
	Entrypoint string `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
	// Cmd overrides the container command.
	// +optional
	Cmd string `json:"cmd,omitempty" yaml:"cmd,omitempty"`
	// SANs is the list of subject alternative names to be added to the node's certificate.
	// +listType=atomic
	// +optional
	SANs []string `json:"SANs,omitempty" yaml:"SANs,omitempty"`
	// Exec is a list of commands to run in the node's container once it is started.
	// +listType=atomic
	// +optional
	Exec []string `json:"exec,omitempty" yaml:"exec,omitempty"`
	// Binds is a list of bind (mount) compatible strings.
	// +listType=atomic
	// +optional
	Binds []string `json:"binds,omitempty" yaml:"binds,omitempty"`
	// Ports is a list of (docker style) port bindings for the node. Ports listed here are also
	// what feeds the clabernetes expose machinery -- the allocated (load balancer) port set ends
	// up in the Node status.
	// note: no yaml omitempty -- historically we always render the (possibly empty) list so that
	// rendered topology comparisons never see nil vs empty slice differences.
	// +listType=atomic
	// +optional
	Ports []string `json:"ports,omitempty" yaml:"ports"`
	// MgmtIPv4 is the user-defined IPv4 address of the node in the management network.
	// +optional
	MgmtIPv4 string `json:"mgmt-ipv4,omitempty" yaml:"mgmt-ipv4,omitempty"`
	// MgmtIPv6 is the user-defined IPv6 address of the node in the management network.
	// +optional
	MgmtIPv6 string `json:"mgmt-ipv6,omitempty" yaml:"mgmt-ipv6,omitempty"`
	// Publish is a list of ports to publish with mysocketctl.
	// +listType=atomic
	// +optional
	Publish []string `json:"publish,omitempty" yaml:"publish,omitempty"`
	// Env holds the environment variables for the node's container.
	// +optional
	Env map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	// EnvFiles is a list of external files containing environment variables for the node.
	// +listType=atomic
	// +optional
	EnvFiles []string `json:"env-files,omitempty" yaml:"env-files,omitempty"`
	// User is the linux user to use in the node's container.
	// +optional
	User string `json:"user,omitempty" yaml:"user,omitempty"`
	// Labels holds the container labels for the node.
	// +optional
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	// NetworkMode is the container networking mode. `container:<name>` expresses that this node
	// shares the network namespace of another node -- clabernetes derives node "grouping" (nodes
	// co-located in one launcher pod) from exactly this containerlab-native field.
	// +optional
	NetworkMode string `json:"network-mode,omitempty" yaml:"network-mode,omitempty"`
	// Sandbox is the ignite sandbox image name.
	// +optional
	Sandbox string `json:"sandbox,omitempty" yaml:"sandbox,omitempty"`
	// Kernel is the ignite kernel image name.
	// +optional
	Kernel string `json:"kernel,omitempty" yaml:"kernel,omitempty"`
	// Runtime overrides the container runtime for the node.
	// +optional
	Runtime string `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	// CPU is the node CPU limit (cgroup or hypervisor) -- note that this is the *containerlab*
	// (docker) cpu setting, launcher pod resources are LauncherProfile territory.
	// +optional
	CPU float64 `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	// CPUSet is the set of CPUs the node can use.
	// +optional
	CPUSet string `json:"cpu-set,omitempty" yaml:"cpu-set,omitempty"`
	// Memory is the node memory limit (cgroup or hypervisor) -- as with CPU this is the
	// *containerlab* (docker) setting, not the launcher pod resources.
	// +optional
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`
	// Sysctls holds sysctl settings for the node.
	// +optional
	Sysctls map[string]string `json:"sysctls,omitempty" yaml:"sysctls,omitempty"`
	// Extras holds extra, possibly kind specific, node parameters.
	// +optional
	Extras *Extras `json:"extras,omitempty" yaml:"extras,omitempty"`
	// WaitFor is a list of node names to wait for before starting this particular node.
	// +listType=atomic
	// +optional
	WaitFor []string `json:"wait-for,omitempty" yaml:"wait-for,omitempty"`
	// DNS holds the DNS configuration for the node.
	// +optional
	DNS *DNSConfig `json:"dns,omitempty" yaml:"dns,omitempty"`
	// Certificate holds the TLS certificate configuration for the node.
	// +optional
	Certificate *CertificateConfig `json:"certificate,omitempty" yaml:"certificate,omitempty"`
	// Healthcheck holds the healthcheck configuration for the node.
	// +optional
	Healthcheck *HealthcheckConfig `json:"healthcheck,omitempty" yaml:"healthcheck,omitempty"`
	// Aliases is a list of network aliases for the node.
	// +listType=atomic
	// +optional
	Aliases []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
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
	// MysocketProxy is the proxy address that mysocketctl will use.
	// +optional
	MysocketProxy string `json:"mysocket-proxy,omitempty" yaml:"mysocket-proxy,omitempty"`
	// CeosCopyToFlash is a list of paths to files which are to be copied to the ceos flash dir.
	// +listType=atomic
	// +optional
	CeosCopyToFlash []string `json:"ceos-copy-to-flash,omitempty" yaml:"ceos-copy-to-flash,omitempty"` //nolint:lll
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

// CertificateConfig represents the configuration of a TLS infrastructure used by a node.
type CertificateConfig struct {
	// Issue indicates if the node should have a certificate issued -- the default false value
	// indicates that the node does not use TLS.
	// +optional
	Issue bool `json:"issue,omitempty" yaml:"issue,omitempty"`
}

// HealthcheckConfig represents healthcheck options a node has.
type HealthcheckConfig struct {
	// Test is the command to run to check the health of the container.
	// +listType=atomic
	// +optional
	Test []string `json:"test,omitempty" yaml:"test"`
	// StartPeriod is the time in seconds to wait for the container to bootstrap before running
	// the first health check.
	// +optional
	StartPeriod int `json:"start-period,omitempty" yaml:"start-period,omitempty"`
	// Retries is the number of consecutive healthcheck failures needed to report the container
	// as unhealthy.
	// +optional
	Retries int `json:"retries,omitempty" yaml:"retries,omitempty"`
	// Interval is the time interval between the health checks in seconds.
	// +optional
	Interval int `json:"interval,omitempty" yaml:"interval,omitempty"`
	// Timeout is the time in seconds to wait for a single health check operation to complete.
	// +optional
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"`
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

// MgmtNet defines the (containerlab, so docker) management network options for the network that
// the nodes in a launcher pod get attached to.
type MgmtNet struct {
	// Network is the name of the docker network to use for the management network.
	// +optional
	Network string `json:"network,omitempty" yaml:"network,omitempty"`
	// IPv4Subnet is the IPv4 subnet of the management network.
	// +optional
	IPv4Subnet string `json:"ipv4-subnet,omitempty" yaml:"ipv4-subnet,omitempty"`
	// IPv4Gw is the IPv4 gateway of the management network.
	// +optional
	IPv4Gw string `json:"ipv4-gw,omitempty" yaml:"ipv4-gw,omitempty"`
	// IPv4Range is the IPv4 range of the management network.
	// +optional
	IPv4Range string `json:"ipv4-range,omitempty" yaml:"ipv4-range,omitempty"`
	// IPv6Subnet is the IPv6 subnet of the management network.
	// +optional
	IPv6Subnet string `json:"ipv6-subnet,omitempty" yaml:"ipv6-subnet,omitempty"`
	// IPv6Gw is the IPv6 gateway of the management network.
	// +optional
	IPv6Gw string `json:"ipv6-gw,omitempty" yaml:"ipv6-gw,omitempty"`
	// IPv6Range is the IPv6 range of the management network.
	// +optional
	IPv6Range string `json:"ipv6-range,omitempty" yaml:"ipv6-range,omitempty"`
	// MTU is the MTU of the management network.
	// +optional
	MTU int `json:"mtu,omitempty" yaml:"mtu,omitempty"`
	// ExternalAccess enables (or disables) external access to the management network.
	// +optional
	ExternalAccess *bool `json:"external-access,omitempty" yaml:"external-access,omitempty"`
}
