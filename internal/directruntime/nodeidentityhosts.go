package directruntime

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

// nodeIdentityHostsMarker tags the hosts line carrying the Pod's own node identity, and
// peerHostsMarker tags the namespace peer entries; a rewrite replaces exactly the marked lines
// without disturbing kubelet- or package-written content.
const (
	nodeIdentityHostsMarker = "# c9s-node-identity"
	peerHostsMarker         = "# c9s-peer"
)

// podHostsFilePath is the kubelet-managed hosts file bind-mounted into every container of the
// Pod; a write from any container is visible in all of them.
const podHostsFilePath = "/etc/hosts"

// applyOwnedHostsBestEffort realizes the c9s-owned hosts entries: the Pod's own node name
// resolves to its management address (the kubelet writes "<podIP> <hostname>" first and the
// first match wins, so the owned line is prepended ahead of it), and every namespace peer —
// with its aliases and chassis component names — resolves to its management address, so peer
// traffic rides the management mesh on any port, exactly like the Docker management network.
// The peer set comes from the mounted peer directory, so lab membership changes reach a
// running Pod without a restart. Best effort by design: a failed rewrite leaves the kubelet's
// resolution in place and must not keep the device from booting.
func applyOwnedHostsBestEffort(
	plan clabernetesinternaldeviceplan.Plan,
	peers []PeerIdentity,
	operations LaunchOperations,
) {
	hostname, err := operations.Hostname()
	if err != nil {
		reportSkippedOwnedHosts(err)

		return
	}

	identity := renderNodeIdentityHostsLines(plan, hostname)
	peerLines := renderPeerHostsLines(peers, hostname)

	if identity == "" && peerLines == "" {
		return
	}

	err = operations.UpdateFile(
		podHostsFilePath,
		func(current []byte) ([]byte, bool) {
			updated := prependOwnedHosts(current, identity+peerLines)

			return updated, !bytes.Equal(current, updated)
		},
	)
	if err != nil {
		reportSkippedOwnedHosts(err)
	}
}

// renderNodeIdentityHostsLines renders the c9s-owned hosts lines for the Pod's primary node,
// or "" when the hostname names no planned node or the node has no management address.
func renderNodeIdentityHostsLines(
	plan clabernetesinternaldeviceplan.Plan,
	hostname string,
) string {
	nodeID := ""

	for _, node := range plan.Nodes {
		if node.Name == hostname {
			nodeID = node.ID

			break
		}
	}

	if nodeID == "" {
		return ""
	}

	ipv4 := ""
	ipv6 := ""

	for _, management := range plan.Management {
		if management.NodeID != nodeID {
			continue
		}

		if address := bareManagementHostsAddress(management.IPv4); address != "" {
			ipv4 = address
		}

		if address := bareManagementHostsAddress(management.IPv6); address != "" {
			ipv6 = address
		}
	}

	lines := strings.Builder{}

	for _, address := range []string{ipv4, ipv6} {
		if address == "" {
			continue
		}

		fmt.Fprintf(&lines, "%s\t%s\t%s\n", address, hostname, nodeIdentityHostsMarker)
	}

	return lines.String()
}

// renderPeerHostsLines renders the namespace peer entries. The Pod's own hostname is owned by
// the identity line; the own node's aliases and component names still resolve through its peer
// entry, and grouped in-Pod names keep their kubelet hostAliases.
func renderPeerHostsLines(peers []PeerIdentity, hostname string) string {
	lines := strings.Builder{}
	seen := map[string]bool{hostname: true}

	for _, peer := range peers {
		for _, name := range append([]string{peer.Name}, peer.Aliases...) {
			if name == "" || seen[name] {
				continue
			}

			seen[name] = true

			for _, address := range []string{peer.IPv4, peer.IPv6} {
				if address == "" {
					continue
				}

				fmt.Fprintf(&lines, "%s\t%s\t%s\n", address, name, peerHostsMarker)
			}
		}
	}

	return lines.String()
}

func bareManagementHostsAddress(cidr string) string {
	if cidr == "" {
		return ""
	}

	address, _, found := strings.Cut(cidr, "/")
	if !found {
		return cidr
	}

	return address
}

// prependOwnedHosts places the owned lines ahead of everything else and drops any previously
// written owned lines, leaving all other content untouched.
func prependOwnedHosts(current []byte, owned string) []byte {
	kept := strings.Builder{}

	for line := range strings.Lines(string(current)) {
		if strings.Contains(line, nodeIdentityHostsMarker) ||
			strings.Contains(line, peerHostsMarker) {
			continue
		}

		kept.WriteString(line)
	}

	remainder := kept.String()
	if remainder != "" && !strings.HasSuffix(remainder, "\n") {
		remainder += "\n"
	}

	return []byte(owned + remainder)
}

// hostsFileMemo remembers the hosts file fingerprint (size and modification time) observed
// right after the owned entries were last realized, so a tick on which neither the peer set
// nor the file changed costs one stat call instead of a render and a file scan proportional
// to the namespace size. Any rewrite by the kubelet or a container moves the fingerprint.
type hostsFileMemo struct {
	mutex       sync.Mutex
	fingerprint string
}

func hostsFileFingerprint(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	return strconv.FormatInt(info.Size(), 10) + "@" +
		strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// unchanged reports whether the file still carries the fingerprint of the last realization.
func (m *hostsFileMemo) unchanged(path string) bool {
	if m == nil {
		return false
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	return m.fingerprint != "" && m.fingerprint == hostsFileFingerprint(path)
}

// remember records the file's fingerprint after a realization.
func (m *hostsFileMemo) remember(path string) {
	if m == nil {
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.fingerprint = hostsFileFingerprint(path)
}

// assertOwnedHosts is the sidecar-side entry: the connectivity sidecar shares the Pod's UTS
// hostname and the kubelet-managed hosts file, so it realizes the same owned entries as the
// launch boundary, from the peers read off the sidecar's own peer-directory mount. With an
// unchanged peer set the realization runs only when the file itself changed underneath.
func assertOwnedHosts(
	plan clabernetesinternaldeviceplan.Plan,
	peers []PeerIdentity,
	memo *hostsFileMemo,
	peersChanged bool,
) {
	if !peersChanged && memo.unchanged(podHostsFilePath) {
		return
	}

	applyOwnedHostsBestEffort(plan, peers, newLaunchOperations())
	memo.remember(podHostsFilePath)
}

// reportSkippedOwnedHosts notes a best-effort hosts rewrite failure on the container's own log
// stream; the device still runs, resolving names the way the kubelet left them.
func reportSkippedOwnedHosts(err error) {
	fmt.Fprintf(
		os.Stderr,
		"c9s: owned hosts entries skipped, name resolution falls back to kubelet content: %v\n",
		err,
	)
}
