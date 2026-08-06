package containerlab

import (
	"fmt"
	"maps"

	claberneteserrors "github.com/srl-labs/clabernetes/errors"
)

// Config defines lab configuration as it is provided in the YAML file.
type Config struct {
	// Lab name
	Name string `yaml:"name"`
	// Lab prefix
	Prefix *string `yaml:"prefix,omitempty"`
	// Management network configuration
	Mgmt *MgmtNet `yaml:"mgmt,omitempty"`
	// Topology definition
	Topology *Topology `yaml:"topology,omitempty"`
	// Debug mode flag
	Debug bool `yaml:"debug"`
}

// MgmtNet struct defines the management network options.
type MgmtNet struct {
	Network        string `yaml:"network,omitempty"`
	IPv4Subnet     string `yaml:"ipv4-subnet,omitempty"`
	IPv4Gw         string `yaml:"ipv4-gw,omitempty"`
	IPv4Range      string `yaml:"ipv4-range,omitempty"`
	IPv6Subnet     string `yaml:"ipv6-subnet,omitempty"`
	IPv6Gw         string `yaml:"ipv6-gw,omitempty"`
	IPv6Range      string `yaml:"ipv6-range,omitempty"`
	MTU            int    `yaml:"mtu,omitempty"`
	ExternalAccess *bool  `yaml:"external-access,omitempty"`
}

// Topology represents a lab topology.
type Topology struct {
	Defaults *NodeDefinition            `yaml:"defaults"`
	Kinds    map[string]*NodeDefinition `yaml:"kinds,omitempty"`
	Groups   map[string]*NodeDefinition `yaml:"groups,omitempty"`
	Nodes    map[string]*NodeDefinition `yaml:"nodes,omitempty"`
	Links    []*LinkDefinition          `yaml:"links,omitempty"`
}

// GetNodeKindType returns the kind and type of the given node name -- it cannot fail, it can only
// return empty strings.
func (t *Topology) GetNodeKindType(nodeName string) (
	containerlabKind,
	containerlabType string,
) {
	containerlabKind = t.Defaults.Kind
	containerlabType = t.Defaults.Type

	nodeDefinition, nodeDefinitionOk := t.Nodes[nodeName]
	if nodeDefinitionOk {
		if nodeDefinition.Kind != "" {
			containerlabKind = nodeDefinition.Kind
		}
	}

	kindDefinition, kindDefinitionOk := t.Kinds[nodeName]
	if kindDefinitionOk {
		if kindDefinition.Type != "" {
			containerlabType = kindDefinition.Type
		}
	}

	if nodeDefinitionOk {
		// override type based on the node (most specific) lastly
		if nodeDefinition.Type != "" {
			containerlabType = nodeDefinition.Type
		}
	}

	return containerlabKind, containerlabType
}

// GetNodeImage returns the resolved image for the given node.
func (t *Topology) GetNodeImage(nodeName string) string {
	containerlabKind, _ := t.GetNodeKindType(nodeName)

	nodeDefinition, nodeDefinitionOk := t.Nodes[nodeName]
	if nodeDefinitionOk {
		if nodeDefinition.Image != "" {
			return nodeDefinition.Image
		}
	}

	kindDefinition, kindDefinitionOk := t.Kinds[containerlabKind]
	if kindDefinitionOk {
		if kindDefinition.Image != "" {
			return kindDefinition.Image
		}
	}

	return t.Defaults.Image
}

// GetNodeLicense returns the resolved license for the given node.
func (t *Topology) GetNodeLicense(nodeName string) string {
	containerlabKind, _ := t.GetNodeKindType(nodeName)

	nodeDefinition, nodeDefinitionOk := t.Nodes[nodeName]
	if nodeDefinitionOk {
		if nodeDefinition.License != "" {
			return nodeDefinition.License
		}
	}

	kindDefinition, kindDefinitionOk := t.Kinds[containerlabKind]
	if kindDefinitionOk {
		if kindDefinition.License != "" {
			return kindDefinition.License
		}
	}

	return t.Defaults.License
}

// NodeDefinition represents a configuration a given node can have in the lab definition file.
type NodeDefinition struct {
	Kind                 string            `yaml:"kind,omitempty"`
	Group                string            `yaml:"group,omitempty"`
	Type                 string            `yaml:"type,omitempty"`
	StartupConfig        string            `yaml:"startup-config,omitempty"`
	StartupDelay         uint              `yaml:"startup-delay,omitempty"`
	EnforceStartupConfig bool              `yaml:"enforce-startup-config,omitempty"`
	AutoRemove           *bool             `yaml:"auto-remove,omitempty"`
	Config               *ConfigDispatcher `yaml:"config,omitempty"`
	Image                string            `yaml:"image,omitempty"`
	ImagePullPolicy      string            `yaml:"image-pull-policy,omitempty"`
	License              string            `yaml:"license,omitempty"`
	Position             string            `yaml:"position,omitempty"`
	Entrypoint           string            `yaml:"entrypoint,omitempty"`
	Cmd                  string            `yaml:"cmd,omitempty"`
	// list of subject Alternative Names (SAN) to be added to the node's certificate
	SANs []string `yaml:"SANs,omitempty"`
	// list of commands to run in container
	Exec []string `yaml:"exec,omitempty"`
	// list of bind mount compatible strings
	Binds []string `yaml:"binds,omitempty"`
	// list of port bindings -- *NOTE* we dropped omitempty, this is different than normal clab, we
	// do this because when comparing topos during reconciliation we had some nil and some empty
	// slices which reflect deep equal says are not the same (because duh, but also come on man!)
	Ports []string `yaml:"ports"`
	// user-defined IPv4 address in the management network
	MgmtIPv4 string `yaml:"mgmt-ipv4,omitempty"`
	// user-defined IPv6 address in the management network
	MgmtIPv6 string `yaml:"mgmt-ipv6,omitempty"`
	// list of ports to publish with mysocketctl
	Publish []string `yaml:"publish,omitempty"`
	// environment variables
	Env map[string]string `yaml:"env,omitempty"`
	// external file containing environment variables
	EnvFiles []string `yaml:"env-files,omitempty"`
	// linux user used in a container
	User string `yaml:"user,omitempty"`
	// container labels
	Labels map[string]string `yaml:"labels,omitempty"`
	// container networking mode. if set to `host` the host networking will be used for this
	// node, else bridged network
	NetworkMode string `yaml:"network-mode,omitempty"`
	// Ignite sandbox and kernel imageNames
	Sandbox string `yaml:"sandbox,omitempty"`
	Kernel  string `yaml:"kernel,omitempty"`
	// Override container runtime
	Runtime string `yaml:"runtime,omitempty"`
	// Set node CPU (cgroup or hypervisor)
	CPU float64 `yaml:"cpu,omitempty"`
	// Set node CPUs to use
	CPUSet string `yaml:"cpu-set,omitempty"`
	// Set node Memory (cgroup or hypervisor)
	Memory string `yaml:"memory,omitempty"`
	// Set the nodes Sysctl
	Sysctls map[string]string `yaml:"sysctls,omitempty"`
	// Extra options, may be kind specific
	Extras *Extras `yaml:"extras,omitempty"`
	// List of node names to wait for before satarting this particular node
	WaitFor []string `yaml:"wait-for,omitempty"`
	// DNS configuration
	DNS *DNSConfig `yaml:"dns,omitempty"`
	// Certificate Configuration
	Certificate *CertificateConfig `yaml:"certificate,omitempty"`
	// Healthcheck configuration
	Healthcheck *HealthcheckConfig `yaml:"healthcheck,omitempty"`
	// Network aliases
	Aliases    []string     `yaml:"aliases,omitempty"`
	Components []*Component `yaml:"components,omitempty"`
}

// ConfigDispatcher represents the config of a configuration machine
// that is responsible to execute configuration commands on the nodes
// after they started.
type ConfigDispatcher struct {
	Vars map[string]any `yaml:"vars,omitempty"`
}

// Extras contains extra node parameters which are not entitled to be part of a generic node config.
type Extras struct {
	SRLAgents []string `yaml:"srl-agents,omitempty"`
	// Nokia SR Linux agents. As of now just the agents spec files can be provided here
	MysocketProxy string `yaml:"mysocket-proxy,omitempty"`
	// Proxy address that mysocketctl will use
	CeosCopyToFlash []string `yaml:"ceos-copy-to-flash,omitempty"`
	// paths to files which are to be copied to ceos flash dir
}

// DNSConfig represents DNS configuration options a node has.
type DNSConfig struct {
	// DNS servers
	Servers []string `yaml:"servers,omitempty"`
	// DNS options
	Options []string `yaml:"options,omitempty"`
	// DNS Search Domains
	Search []string `yaml:"search,omitempty"`
}

// CertificateConfig represents the configuration of a TLS infrastructure used by a node.
type CertificateConfig struct {
	// default false value indicates that the node does not use TLS
	Issue bool `yaml:"issue,omitempty"`
	// additional params would go here, e.g. if
	// different algos would be needed or so
}

// LinkDefinition represents a link definition in the topology file.
type LinkDefinition struct {
	LinkConfig `yaml:",inline"`

	Type string `yaml:"type,omitempty"`
}

// LinkConfig is the vendor'd (ish) clab link config object.
type LinkConfig struct {
	Endpoints []string
	Labels    map[string]string `yaml:"labels,omitempty"`
	Vars      map[string]any    `yaml:"vars,omitempty"`
	MTU       int               `yaml:"mtu,omitempty"`
}

// HealthcheckConfig represents healthcheck options a node has.
type HealthcheckConfig struct {
	// Test: the command to run to check the health of the container
	Test []string `yaml:"test"`
	// StartPeriod: the time in seconds to wait for the container to bootstrap before running the
	// first health check
	StartPeriod int `yaml:"start-period,omitempty"`
	// Retries: the number of consecutive healthcheck failures needed to report the container as
	// unhealthy.
	Retries int `yaml:"retries,omitempty"`
	// Interval: the time interval between the health checks in seconds
	Interval int `yaml:"interval,omitempty"`
	// Timeout: the time in seconds to wait for a single health check operation to complete.
	Timeout int `yaml:"timeout,omitempty"`
}

type Component struct {
	Slot string            `yaml:"slot,omitempty"`
	Type string            `yaml:"type,omitempty"`
	Env  map[string]string `yaml:"env,omitempty"`
	SFM  string            `yaml:"sfm,omitempty"`
	XIOM XIOMS             `yaml:"xiom,omitempty"`
	MDA  MDAS              `yaml:"mda,omitempty"`
}

type XIOM struct {
	Slot int    `yaml:"slot,omitempty"`
	Type string `yaml:"type,omitempty"`
	MDA  MDAS   `yaml:"mda,omitempty"`
}

type XIOMS []XIOM //nolint: recvcheck

type MDA struct {
	Slot int    `yaml:"slot,omitempty"`
	Type string `yaml:"type,omitempty"`
}

type MDAS []MDA //nolint: recvcheck

func (c *Component) Copy() *Component {
	if c == nil {
		return nil
	}

	// Deep copy the map
	var envCopy map[string]string
	if c.Env != nil {
		envCopy = make(map[string]string, len(c.Env))

		maps.Copy(envCopy, c.Env)
	}

	return &Component{
		Slot: c.Slot,
		Type: c.Type,
		Env:  envCopy,
		SFM:  c.SFM,
		XIOM: c.XIOM.Copy(),
		MDA:  c.MDA.Copy(),
	}
}

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

func (l MDAS) Copy() MDAS {
	if l == nil {
		return nil
	}

	out := make([]MDA, len(l))
	copy(out, l)

	return out
}

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

func (l XIOMS) Copy() XIOMS {
	if l == nil {
		return nil
	}

	out := make([]XIOM, len(l))
	copy(out, l)

	// deep copy
	for i := range out {
		out[i].MDA = out[i].MDA.Copy()
	}

	return out
}
