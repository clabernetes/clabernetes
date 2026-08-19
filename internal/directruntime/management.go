package directruntime

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// PodAddressEnvironmentVariable carries the Pod's own cluster address into every direct
// application container and connectivity helper through the Kubernetes downward API. The direct
// runtime realizes the containerlab management network as the Pod network, so this is the
// runtime-discoverable management identity of every logical Node the controller left
// unaddressed.
const PodAddressEnvironmentVariable = "C9S_POD_ADDRESS"

// podAddressRecordName is the artifacts-root file the preparation container writes while the
// Pod's primary interface is still pristine: it records the Pod address with its real prefix so
// later lifecycle boundaries keep the full management identity after a device implementation
// strips the interface.
const podAddressRecordName = "pod-address"

// RecordPodAddress persists the Pod's prefixed management identity into every plan-owned node
// artifact directory (the only writable mounts under the artifacts root). The preparation
// boundary calls it before any device container starts, which is the only moment the Pod's
// primary interface reliably still carries the address.
func RecordPodAddress(artifactRoot string) error {
	address := runtimePodAddress()
	if address == "" {
		return nil
	}
	root := filepath.Clean(artifactRoot)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return fmt.Errorf("pod address record root must be a scoped absolute path")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("listing pod address record root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record := address + "\n"
		if gateway := runtimePodGateway(); gateway != "" {
			record += gateway + "\n"
		}
		if err = os.WriteFile( //nolint:gosec // Non-sensitive plan-scoped identity record.
			filepath.Join(root, entry.Name(), podAddressRecordName),
			[]byte(record),
			0o644,
		); err != nil {
			return fmt.Errorf("recording pod address: %w", err)
		}
	}

	return nil
}

// runtimePodAddressWithRecord returns the richest management identity available: the live
// enriched address when the primary interface is intact, otherwise the preparation-recorded
// prefixed address, otherwise the bare downward-API address.
func runtimePodAddressWithRecord(artifactRoot string) (string, string) {
	address := runtimePodAddress()
	if address == "" {
		return "", ""
	}
	gateway := runtimePodGateway()
	entries, err := os.ReadDir(filepath.Clean(artifactRoot))
	if err != nil {
		return address, gateway
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		recorded, readErr := os.ReadFile( //nolint:gosec // Plan-scoped identity record.
			filepath.Join(filepath.Clean(artifactRoot), entry.Name(), podAddressRecordName),
		)
		if readErr != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(recorded)), "\n")
		if len(lines) == 0 || !strings.HasPrefix(lines[0], strings.Split(address, "/")[0]) {
			continue
		}
		if strings.Contains(address, "/") == false && strings.HasPrefix(lines[0], address+"/") {
			address = lines[0]
		}
		if gateway == "" && len(lines) > 1 {
			gateway = strings.TrimSpace(lines[1])
		}

		return address, gateway
	}

	return address, gateway
}

// runtimePodGateway returns the Pod's IPv4 default gateway while the routing table still
// carries it, or empty afterward. The preparation record preserves it for later boundaries.
func runtimePodGateway() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		var value uint32
		if _, scanErr := fmt.Sscanf(fields[2], "%x", &value); scanErr != nil {
			continue
		}

		return net.IPv4(byte(value), byte(value>>8), byte(value>>16), byte(value>>24)).String()
	}

	return ""
}

// runtimePodAddress returns the executing Pod's own cluster address, or empty outside a direct
// Pod boundary. While the Pod's primary interface still carries the address, the live prefix
// length is attached so imported packages that template "<address>/<length>" render the real
// management prefix; once a device implementation strips the interface, the bare address is
// still returned and dialing keeps working.
func runtimePodAddress() string {
	address := strings.TrimSpace(os.Getenv(PodAddressEnvironmentVariable))
	if address == "" {
		return ""
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return address
	}
	for _, item := range interfaces {
		addresses, addressErr := item.Addrs()
		if addressErr != nil {
			continue
		}
		for _, candidate := range addresses {
			network, ok := candidate.(*net.IPNet)
			if !ok || network.IP.String() != address {
				continue
			}
			length, _ := network.Mask.Size()

			return fmt.Sprintf("%s/%d", address, length)
		}
	}

	return address
}
