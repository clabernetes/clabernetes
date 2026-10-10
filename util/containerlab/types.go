package containerlab

import (
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabtypes "github.com/srl-labs/containerlab/types"
)

// The containerlab *vocabulary* -- the node definition and its sub objects -- lives in
// apis/v1alpha1 (it is, verbatim, the containerlab portion of the Node custom resource spec);
// the aliases here exist so that topology parsing/rendering code keeps reading naturally in
// containerlab terms. The file-level containerlab constructs (the lab Config, the Topology, and
// the link definitions) live in containerlab's compile engine since the compile moved there, so
// this package only carries the vocabulary aliases and the grouping/link/network-mode/port
// helpers the controllers share.
type (
	// NodeDefinition represents a configuration a given node can have in the lab definition
	// file.
	NodeDefinition = clabernetesapisv1alpha1.NodeDefinition
	// ConfigDispatcher represents the config of a configuration machine that is responsible to
	// execute configuration commands on the nodes after they started.
	ConfigDispatcher = clabernetesapisv1alpha1.ConfigDispatcher
	// DNSConfig represents DNS configuration options a node has.
	DNSConfig = clabernetesapisv1alpha1.DNSConfig
	// CertificateConfig represents the configuration of a TLS infrastructure used by a node.
	CertificateConfig = clabernetesapisv1alpha1.CertificateConfig
	// HealthcheckConfig represents a containerlab process health contract for a node.
	HealthcheckConfig = clabernetesapisv1alpha1.HealthcheckConfig
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
	MgmtNet = clabtypes.MgmtNet
)
