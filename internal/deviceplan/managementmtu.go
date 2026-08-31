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

// maximumAutomaticManagementIPMTU keeps the runtime management value compatible with imported
// device management ports whose default Ethernet MTU is 1514 bytes. Smaller realized paths remain
// authoritative; a jumbo Pod underlay must not be advertised as an IP MTU unless the device port
// was explicitly enlarged as well.
const maximumAutomaticManagementIPMTU = 1500

// ManagementPathMTU reports the automatic IP MTU advertised to an imported device for one logical
// Node. It is the realized management mesh capacity, capped at 1500 bytes so a jumbo Pod underlay
// does not exceed a device management port's conventional default; an imported package that
// configures its management interface from the runtime's management network then receives a value
// that fits both the mesh and the default device port.
//
// The value is read from the realized interface rather than carried in the plan because the
// bound depends on the CNI MTU of the Kubernetes node the Pod landed on, which the controller
// does not know when it plans. A management path this process cannot observe reports 0, which
// leaves the imported package on its own default.
func ManagementPathMTU(plan Plan, nodeID string) int {
	return capAutomaticManagementIPMTU(interfaceMTU(managementInterfaceName(plan, nodeID)))
}

func capAutomaticManagementIPMTU(mtu int) int {
	return min(mtu, maximumAutomaticManagementIPMTU)
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
