package node

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
)

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
