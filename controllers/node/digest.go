package node

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
)

// link attachment materialization modes -- how the launcher realizes a given local interface.
const (
	// attachmentModeDirect -- both endpoints live in this launcher pod, containerlab wires them
	// directly.
	attachmentModeDirect = "direct"
	// attachmentModeTunnel -- the remote endpoint lives in another launcher pod, containerlab
	// creates a node<->host veth that the connectivity machinery attaches a tunnel to.
	attachmentModeTunnel = "tunnel"
	// attachmentModeHost -- the link terminates on the launcher pod itself (reserved `host`
	// node name), materialized verbatim.
	attachmentModeHost = "host"
)

// LinkAttachmentsDigest computes the digest of the *attachment set* of a launcher group: for
// every link endpoint local to the group, the local node+interface, the materialization mode,
// and the mtu -- but deliberately *not* the remote end. Changing a link's remote endpoint
// ("rewiring") therefore keeps the digest stable (the launcher moves the tunnel live), while
// any change to the set of local attachments (or their mode/mtu) changes the digest, which
// changes the pod template annotation, which rolls the pod. The launcher computes the same
// digest from the links it fetched and compares it against the annotation (via the downward
// api) to know its link view is complete.
func LinkAttachmentsDigest(
	groupMembers []string,
	links []clabernetesapisv1alpha1.Link,
) string {
	members := make(map[string]bool, len(groupMembers))

	for _, member := range groupMembers {
		members[member] = true
	}

	entries := make([]string, 0)

	for idx := range links {
		link := &links[idx]

		endpointAInGroup := members[link.Spec.EndpointA.NodeName]
		endpointBInGroup := members[link.Spec.EndpointB.NodeName]

		for _, endpoint := range []struct {
			local  clabernetesapisv1alpha1.LinkEndpointSpec
			remote clabernetesapisv1alpha1.LinkEndpointSpec
			inside bool
		}{
			{link.Spec.EndpointA, link.Spec.EndpointB, endpointAInGroup},
			{link.Spec.EndpointB, link.Spec.EndpointA, endpointBInGroup},
		} {
			if !endpoint.inside ||
				endpoint.local.NodeName == clabernetesapisv1alpha1.LinkHostNodeName {
				continue
			}

			var mode string

			switch {
			case endpoint.remote.NodeName == clabernetesapisv1alpha1.LinkHostNodeName:
				mode = attachmentModeHost
			case members[endpoint.remote.NodeName]:
				mode = attachmentModeDirect
			default:
				mode = attachmentModeTunnel
			}

			entries = append(
				entries,
				fmt.Sprintf(
					"%s:%s:%s:%d",
					endpoint.local.NodeName,
					endpoint.local.InterfaceName,
					mode,
					link.Spec.MTU,
				),
			)
		}
	}

	sort.Strings(entries)

	digest := sha256.Sum256([]byte(strings.Join(entries, "\n")))

	return hex.EncodeToString(digest[:])
}

// ConfigDigest computes the digest of the launcher-relevant configuration of a launcher
// group that is *not* otherwise visible in the deployment spec: the node definitions (specs) of
// all group members, their expose port allocations (published ports are boot time for docker),
// and the management network settings. Group members are hashed in sorted-name order so the
// digest is deterministic.
func ConfigDigest(
	groupMembers []string,
	nodes map[string]*clabernetesapisv1alpha1.Node,
	exposedPorts map[string]*clabernetesapisv1alpha1.NodeExposedPorts,
	mgmt *clabernetesapisv1alpha1.MgmtNet,
) (string, error) {
	sortedMembers := make([]string, len(groupMembers))

	copy(sortedMembers, groupMembers)
	sort.Strings(sortedMembers)

	hash := sha256.New()

	for _, member := range sortedMembers {
		memberNode, ok := nodes[member]
		if !ok {
			continue
		}

		specBytes, err := json.Marshal(memberNode.Spec)
		if err != nil {
			return "", err
		}

		_, _ = fmt.Fprintf(hash, "%s=%s\n", member, specBytes)

		memberExposedPorts := exposedPorts[member]
		if memberExposedPorts != nil {
			for _, port := range memberExposedPorts.Ports {
				_, _ = fmt.Fprintf(
					hash,
					"%s:%d:%d:%s\n",
					member,
					port.ExposePort,
					port.DestinationPort,
					port.Protocol,
				)
			}
		}
	}

	if mgmt != nil {
		mgmtBytes, err := json.Marshal(mgmt)
		if err != nil {
			return "", err
		}

		_, _ = fmt.Fprintf(hash, "mgmt=%s\n", mgmtBytes)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
