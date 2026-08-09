package connectivity

import (
	"fmt"
	"os"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
	clabernetesutilcontainerlab "github.com/srl-labs/clabernetes/util/containerlab"
)

// Tunnel holds the *local view* of a tunnel between two interfaces on different launcher pods --
// launchers derive this view from the Link objects terminating on their nodes; nothing persists
// it.
type Tunnel struct {
	// TunnelID is the id number of the tunnel (vxlan vnid or slurpeeth segment id).
	TunnelID int
	// Connectivity is the normalized per-Link connectivity flavor.
	Connectivity clabernetesapisv1alpha1.LinkConnectivity
	// Destination is the remote launcher's fabric service to connect to (qualified k8s service
	// name) -- a pure function of the link spec since fabric services exist per node.
	Destination string
	// LocalNode is the name of the local (containerlab) node for this side of the tunnel.
	LocalNode string
	// LocalInterface is the local termination of this tunnel.
	LocalInterface string
	// RemoteNode is the name of the remote (containerlab) node for this side of the tunnel.
	RemoteNode string
	// RemoteInterface is the remote termination interface of this tunnel.
	RemoteInterface string
	// MTU is the mtu for the link this tunnel realizes; zero means "unset" (containerlab
	// default).
	MTU int
}

// linkDestination returns the qualified service name of the remote node's fabric service --
// derived rather than persisted: fabric services exist per (containerlab) node as
// `<node>-vx.<namespace>.<dns suffix>` (a grouped node's service selects its primary's pod), so
// the destination follows from the link spec alone.
func linkDestination(namespace, remoteNodeName string) string {
	return fmt.Sprintf(
		"%s-vx.%s.%s",
		remoteNodeName,
		namespace,
		clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.LauncherInClusterDNSSuffixEnv,
			clabernetesconstants.KubernetesDefaultInClusterDNSSuffix,
		),
	)
}

// LinkToLocalTunnel converts a link to the "local view" tunnel for the launcher hosting the
// given local nodes. It returns nil for links that need no tunnel: host links, links fully
// local to this launcher (both endpoints in localNodes -- those become direct containerlab
// links), and links not touching this launcher at all.
func LinkToLocalTunnel(
	localNodes map[string]bool,
	link *clabernetesapisv1alpha1.Link,
) *Tunnel {
	local, remote := link.Spec.EndpointA, link.Spec.EndpointB

	localAIn := localNodes[local.NodeName]
	localBIn := localNodes[remote.NodeName]

	if localAIn == localBIn {
		// both local (direct link) or neither local (not our link) -- no tunnel either way
		return nil
	}

	if !localAIn {
		local, remote = remote, local
	}

	if remote.NodeName == clabernetesapisv1alpha1.LinkHostNodeName ||
		local.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
		// host links are materialized verbatim, no tunnel
		return nil
	}

	return &Tunnel{
		TunnelID:        link.Status.TunnelID,
		Connectivity:    link.Spec.NormalizedConnectivity(),
		Destination:     linkDestination(link.GetNamespace(), remote.NodeName),
		LocalNode:       local.NodeName,
		LocalInterface:  local.InterfaceName,
		RemoteNode:      remote.NodeName,
		RemoteInterface: remote.InterfaceName,
		MTU:             link.Spec.MTU,
	}
}

// TunnelsFromLinks converts the given links to the sorted local tunnel view of the launcher
// hosting the given local nodes -- links without an allocated tunnel id are skipped (the link
// watch picks them up once the controller fills the allocation in).
func TunnelsFromLinks(
	localNodes map[string]bool,
	links []clabernetesapisv1alpha1.Link,
) []*Tunnel {
	tunnels := make([]*Tunnel, 0, len(links))
	activeLinks := clabernetesutilcontainerlab.ActiveLinks(links)

	for idx := range activeLinks {
		if activeLinks[idx].Status.TunnelID == 0 {
			continue
		}

		tunnel := LinkToLocalTunnel(localNodes, &activeLinks[idx])
		if tunnel == nil {
			continue
		}

		tunnels = append(tunnels, tunnel)
	}

	sort.Slice(
		tunnels,
		func(i, j int) bool {
			if tunnels[i].LocalNode != tunnels[j].LocalNode {
				return tunnels[i].LocalNode < tunnels[j].LocalNode
			}

			return tunnels[i].LocalInterface < tunnels[j].LocalInterface
		},
	)

	return tunnels
}

// LocalNodesFromEnv returns the set of (containerlab) nodes hosted by this launcher pod -- the
// launcher's own node plus any group members from the environment.
func LocalNodesFromEnv() map[string]bool {
	localNodes := map[string]bool{
		os.Getenv(clabernetesconstants.LauncherNodeNameEnv): true,
	}

	groupMembers := os.Getenv(clabernetesconstants.LauncherGroupMembersEnv)
	if groupMembers != "" {
		for member := range strings.SplitSeq(groupMembers, ",") {
			localNodes[member] = true
		}
	}

	return localNodes
}
