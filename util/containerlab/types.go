package containerlab

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	"gopkg.in/yaml.v3"
)

// The containerlab *vocabulary* -- the node definition and its sub objects -- lives in
// apis/v1alpha1 (it is, verbatim, the containerlab portion of the Node custom resource spec);
// the aliases here exist so that topology parsing/rendering code keeps reading naturally in
// containerlab terms. The types in this file proper (Config, Topology, links) are the file-level
// containerlab constructs that are *not* part of any custom resource.
type (
	// NodeDefinition represents a configuration a given node can have in the lab definition
	// file.
	NodeDefinition = clabernetesapisv1alpha1.NodeDefinition
	// ConfigDispatcher represents the config of a configuration machine that is responsible to
	// execute configuration commands on the nodes after they started.
	ConfigDispatcher = clabernetesapisv1alpha1.ConfigDispatcher
	// Extras contains extra node parameters which are not entitled to be part of a generic node
	// config.
	Extras = clabernetesapisv1alpha1.Extras
	// DNSConfig represents DNS configuration options a node has.
	DNSConfig = clabernetesapisv1alpha1.DNSConfig
	// CertificateConfig represents the configuration of a TLS infrastructure used by a node.
	CertificateConfig = clabernetesapisv1alpha1.CertificateConfig
	// Component holds a hardware component configuration (i.e. an SR-OS card or mda).
	Component = clabernetesapisv1alpha1.Component
	// XIOM holds a single xiom configuration of a hardware component.
	XIOM = clabernetesapisv1alpha1.XIOM
	// XIOMS is a list of XIOM objects.
	XIOMS = clabernetesapisv1alpha1.XIOMS
	// MDA holds a single mda configuration of a hardware component.
	MDA = clabernetesapisv1alpha1.MDA
	// MDAS is a list of MDA objects.
	MDAS = clabernetesapisv1alpha1.MDAS
	// MgmtNet struct defines the management network options.
	MgmtNet = clabernetesapisv1alpha1.MgmtNet
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

// Topology represents a lab topology.
type Topology struct {
	Defaults *NodeDefinition            `yaml:"defaults"`
	Kinds    map[string]*NodeDefinition `yaml:"kinds,omitempty"`
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

// LinkDefinition represents a link definition in the topology file.
type LinkDefinition struct {
	LinkConfig `yaml:",inline"`

	Type string `yaml:"type,omitempty"`
}

// LinkConfig is the vendor'd (ish) clab link config object.
type LinkConfig struct {
	Endpoints LinkEndpoints
	Labels    map[string]string `yaml:"labels,omitempty"`
	Vars      map[string]any    `yaml:"vars,omitempty"`
	MTU       int               `yaml:"mtu,omitempty"`
}

// LinkEndpoints accepts both containerlab's brief "node:interface" endpoint syntax and the
// equivalent structured node/interface syntax used by explicit veth links. It stores the canonical
// brief form because that is the complete endpoint vocabulary the c9s Link API can represent.
type LinkEndpoints []string

func (e *LinkEndpoints) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("%w: link endpoints must be a sequence", claberneteserrors.ErrParse)
	}

	endpoints := make(LinkEndpoints, 0, len(value.Content))

	for _, endpointNode := range value.Content {
		switch endpointNode.Kind {
		case yaml.ScalarNode:
			endpoints = append(endpoints, endpointNode.Value)
		case yaml.MappingNode:
			endpoint := struct {
				Node      string `yaml:"node"`
				Interface string `yaml:"interface"`
			}{}

			err := endpointNode.Decode(&endpoint)
			if err != nil {
				return err
			}

			if endpoint.Node == "" || endpoint.Interface == "" {
				return fmt.Errorf(
					"%w: structured link endpoints require node and interface",
					claberneteserrors.ErrParse,
				)
			}

			endpoints = append(endpoints, fmt.Sprintf("%s:%s", endpoint.Node, endpoint.Interface))
		case yaml.DocumentNode, yaml.SequenceNode, yaml.AliasNode:
			return fmt.Errorf(
				"%w: link endpoint must be a string or node/interface mapping",
				claberneteserrors.ErrParse,
			)
		}
	}

	*e = endpoints

	return nil
}

func (e LinkEndpoints) MarshalYAML() (any, error) {
	return []string(e), nil
}
