package deviceplan

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysfsInterfaceRoot is where the kernel publishes the attributes of the interfaces present in
// the caller's network namespace.
const sysfsInterfaceRoot = "/sys/class/net"

// ManagementPathMTU reports the MTU of the management path c9s realized for one logical Node, as
// the device sees it. The management mesh is bounded by the Pod underlay minus the encapsulation
// it rides in, which is smaller than the 1500 bytes a device assumes by default -- an imported
// package that configures its management interface from the runtime's management network then
// configures the size that actually fits, instead of black-holing every packet above the path
// MTU (there is no router on the path to answer with fragmentation-needed).
//
// The value is read from the realized interface rather than carried in the plan because the
// bound depends on the CNI MTU of the Kubernetes node the Pod landed on, which the controller
// does not know when it plans. A management path this process cannot observe reports 0, which
// leaves the imported package on its own default.
func ManagementPathMTU(plan Plan, nodeID string) int {
	return interfaceMTU(managementInterfaceName(plan, nodeID))
}

// managementInterfaceName returns the name the device's management interface has in the Pod
// network namespace.
func managementInterfaceName(plan Plan, nodeID string) string {
	for _, management := range plan.Management {
		if management.NodeID != nodeID {
			continue
		}

		if management.Interposition != nil && management.Interposition.DeviceInterface != "" {
			return management.Interposition.DeviceInterface
		}

		return management.InterfaceName
	}

	return ""
}

func interfaceMTU(name string) int {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return 0
	}

	// The name is a single path element validated above, so the read stays inside sysfs.
	raw, err := os.ReadFile(filepath.Join(sysfsInterfaceRoot, name, "mtu")) //nolint:gosec
	if err != nil {
		return 0
	}

	mtu, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || mtu <= 0 {
		return 0
	}

	return mtu
}
