package launcher

import (
	"fmt"
	"sort"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslauncherconnectivity "github.com/srl-labs/clabernetes/launcher/connectivity"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

// materializeTopology builds the containerlab topology this launcher runs from the primary
// api objects alone: the node definitions of the launcher's group members (their specs,
// verbatim), and the links terminating on them. Per wire flavor:
//
//   - links fully local to the group become direct containerlab links,
//   - host links are materialized verbatim (host side interface preserved),
//   - cross-launcher links become `node <-> host` stanzas -- the host side veth is what the
//     connectivity machinery later attaches the tunnel to.
//
// Expose ports are materialized from the *status allocations* of every member -- and all onto
// the launcher (primary) node, since the group shares its network namespace and docker publishes
// ports per network namespace.
func materializeTopology(
	launcherNodeName string,
	members map[string]*clabernetesapisv1alpha1.Node,
	links []clabernetesapisv1alpha1.Link,
	mgmt *clabernetesutilcontainerlab.MgmtNet,
) *clabernetesutilcontainerlab.Config {
	nodes := make(map[string]*clabernetesutilcontainerlab.NodeDefinition, len(members))

	memberPorts := make([]string, 0)

	memberNames := make([]string, 0, len(members))

	for memberName := range members {
		memberNames = append(memberNames, memberName)
	}

	sort.Strings(memberNames)

	for _, memberName := range memberNames {
		member := members[memberName]

		nodeDefinition := member.Spec.NodeDefinition.DeepCopy()
		nodeDefinition.Ports = []string{}

		if member.Status.ExposedPorts != nil {
			for _, port := range member.Status.ExposedPorts.Ports {
				memberPorts = append(
					memberPorts,
					fmt.Sprintf(
						"%d:%d/%s",
						port.ExposePort,
						port.DestinationPort,
						port.Protocol,
					),
				)
			}
		}

		nodes[memberName] = nodeDefinition
	}

	// all (allocated) expose ports publish on the launcher node -- the group shares its network
	// namespace and docker rejects port mappings on containers joining another container's
	// netns
	nodes[launcherNodeName].Ports = memberPorts

	config := &clabernetesutilcontainerlab.Config{
		Name:   fmt.Sprintf("clabernetes-%s", launcherNodeName),
		Prefix: clabernetesutil.ToPointer(""),
		Mgmt:   mgmt,
		Topology: &clabernetesutilcontainerlab.Topology{
			// node definitions are self contained (emitters expand defaults/kinds), so the
			// defaults stanza stays empty -- it exists so helpers can safely dereference it
			Defaults: &clabernetesutilcontainerlab.NodeDefinition{
				Ports: []string{},
			},
			Nodes: nodes,
			Links: materializeLinks(members, links),
		},
	}

	return config
}

// materializeLinks converts the given link crs to the containerlab link stanzas for the
// launcher hosting the given members -- see materializeTopology for the flavor rundown.
func materializeLinks(
	members map[string]*clabernetesapisv1alpha1.Node,
	links []clabernetesapisv1alpha1.Link,
) []*clabernetesutilcontainerlab.LinkDefinition {
	stanzas := make([]*clabernetesutilcontainerlab.LinkDefinition, 0, len(links))

	for idx := range links {
		link := &links[idx]

		local, remote := link.Spec.EndpointA, link.Spec.EndpointB

		_, localAIn := members[local.NodeName]
		_, localBIn := members[remote.NodeName]

		var endpoints []string

		switch {
		case localAIn && localBIn:
			// both ends in this launcher pod: a direct containerlab link
			endpoints = []string{
				fmt.Sprintf("%s:%s", local.NodeName, local.InterfaceName),
				fmt.Sprintf("%s:%s", remote.NodeName, remote.InterfaceName),
			}
		case localAIn || localBIn:
			if !localAIn {
				local, remote = remote, local
			}

			if local.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
				// `host` is reserved; a Node object must not be named after it
				continue
			}

			if remote.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
				// a genuine host link, materialized verbatim
				endpoints = []string{
					fmt.Sprintf("%s:%s", local.NodeName, local.InterfaceName),
					fmt.Sprintf(
						"%s:%s",
						clabernetesconstants.HostKeyword,
						remote.InterfaceName,
					),
				}
			} else {
				// cross launcher: node <-> host stanza; the host side veth is the tunnel
				// attachment point
				endpoints = []string{
					fmt.Sprintf("%s:%s", local.NodeName, local.InterfaceName),
					fmt.Sprintf(
						"%s:%s-%s",
						clabernetesconstants.HostKeyword,
						local.NodeName,
						local.InterfaceName,
					),
				}
			}
		default:
			// not our link
			continue
		}

		stanzas = append(stanzas, &clabernetesutilcontainerlab.LinkDefinition{
			LinkConfig: clabernetesutilcontainerlab.LinkConfig{
				Endpoints: endpoints,
				MTU:       link.Spec.MTU,
			},
		})
	}

	return stanzas
}

// tunnelsForLinks returns the local tunnel view for the given links -- the same links snapshot
// used for materializing the topology seeds the connectivity manager, so the tunnels line up
// with the materialized host side veths.
func tunnelsForLinks(
	members map[string]*clabernetesapisv1alpha1.Node,
	links []clabernetesapisv1alpha1.Link,
) []*claberneteslauncherconnectivity.Tunnel {
	localNodes := make(map[string]bool, len(members))

	for memberName := range members {
		localNodes[memberName] = true
	}

	return claberneteslauncherconnectivity.TunnelsFromLinks(localNodes, links)
}
