package launcher

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

// materializeTopologyLinks ensures the given (parsed) sub-topology contains a "node <-> host"
// link stanza for every tunnel terminating on this launcher -- these stanzas are what make
// containerlab create the host side veths that the connectivity manager later attaches the
// tunnels to. The stanzas are launcher plumbing, so they are *not* part of the node cr config
// (that only holds node things); they get synthesized here, into the topology file that is only
// ever local to the launcher pod. Interfaces that already have a link in the config -- genuine
// user defined host links, links between grouped nodes, or configs rendered by older controllers
// that still included the synthetic host links -- are left alone.
func materializeTopologyLinks(
	config *clabernetesutilcontainerlab.Config,
	tunnels []*clabernetesapisv1alpha1.PointToPointTunnel,
) {
	if config.Topology == nil {
		return
	}

	linkedEndpoints := make(map[string]bool)

	for _, link := range config.Topology.Links {
		for _, endpoint := range link.Endpoints {
			linkedEndpoints[endpoint] = true
		}
	}

	for _, tunnel := range tunnels {
		localEndpoint := fmt.Sprintf("%s:%s", tunnel.LocalNode, tunnel.LocalInterface)

		if linkedEndpoints[localEndpoint] {
			continue
		}

		linkedEndpoints[localEndpoint] = true

		config.Topology.Links = append(
			config.Topology.Links,
			&clabernetesutilcontainerlab.LinkDefinition{
				LinkConfig: clabernetesutilcontainerlab.LinkConfig{
					Endpoints: []string{
						localEndpoint,
						fmt.Sprintf(
							"%s:%s-%s",
							clabernetesconstants.HostKeyword,
							tunnel.LocalNode,
							tunnel.LocalInterface,
						),
					},
					MTU: tunnel.MTU,
				},
			},
		)
	}
}
