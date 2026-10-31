//go:build linux

//nolint:err113,gocyclo,gosec,mnd // netns lifecycle diagnostics and kernel file modes are explicit here.
package directruntime

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const deviceNetnsLoopbackName = "lo"

// EnsureDeviceNetns creates the Pod's private device network namespace and bind-mounts it at
// path. It is idempotent: an existing valid netns file is reused, and a stale regular file is
// replaced. The caller mounts the parent directory with bidirectional propagation so sibling
// containers observe the bind mount.
func EnsureDeviceNetns(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == string(filepath.Separator) {
		return fmt.Errorf("device network namespace path %q is not absolute", path)
	}

	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return fmt.Errorf("creating device network namespace directory: %w", err)
	}

	if valid, err := validDeviceNetnsPath(clean); err != nil {
		return err
	} else if valid {
		return nil
	}

	// A regular file left by an interrupted init, or an old bind mount from a reused Pod
	// identity, is not reusable. Unmount and remove it before creating the new namespace.
	if err := unix.Unmount(clean, unix.MNT_DETACH); err != nil &&
		!errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("removing stale device network namespace mount: %w", err)
	}

	if err := os.Remove(clean); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing stale device network namespace file: %w", err)
	}

	file, err := os.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("creating device network namespace file: %w", err)
	}

	if err = file.Close(); err != nil {
		return fmt.Errorf("closing device network namespace file: %w", err)
	}

	return createBoundDeviceNetns(clean)
}

// CleanupDeviceNetns detaches and removes the netns file on graceful Pod termination. The
// namespace itself remains alive while any device process still holds it.
func CleanupDeviceNetns(path string) error {
	if path == "" {
		return nil
	}

	if err := unix.Unmount(path, unix.MNT_DETACH); err != nil &&
		!errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("unmounting device network namespace %q: %w", path, err)
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing device network namespace file %q: %w", path, err)
	}

	return nil
}

func validDeviceNetnsPath(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspecting device network namespace file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("device network namespace path %q is not a regular file", path)
	}

	handle, err := openDeviceNetworkNamespace(path)
	if err != nil {
		return false, nil
	}
	defer handle.Close() //nolint:errcheck // validation only.

	return true, nil
}

func createBoundDeviceNetns(path string) error {
	runtime.LockOSThread()

	restored := false

	defer func() {
		if restored {
			runtime.UnlockOSThread()
		}
	}()

	current, err := netns.Get()
	if err != nil {
		return fmt.Errorf("reading Pod network namespace: %w", err)
	}
	defer current.Close() //nolint:errcheck // handle used for restoration only.

	created, err := netns.New()
	if err != nil {
		return fmt.Errorf("creating device network namespace: %w", err)
	}
	defer created.Close() //nolint:errcheck // the bind mount owns the reference.

	source := fmt.Sprintf("/proc/%d/task/%d/ns/net", os.Getpid(), unix.Gettid())
	if err = unix.Mount(source, path, "bind", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("binding device network namespace at %q: %w", path, err)
	}

	if err = unix.Setns(int(current), unix.CLONE_NEWNET); err != nil {
		// The calling goroutine must not be released on a thread that remained in the new
		// namespace. The init container exits immediately; tests fail closed.
		return fmt.Errorf("restoring Pod network namespace: %w", err)
	}

	restored = true

	if valid, validateErr := validDeviceNetnsPath(path); validateErr != nil {
		return validateErr
	} else if !valid {
		return fmt.Errorf("device network namespace %q is not readable after binding", path)
	}

	return nil
}

// deviceNetworkNamespace is one open private device namespace plus a netlink handle bound to
// it. Operations use the handle directly, so the sidecar never has to enter the namespace.
type deviceNetworkNamespace struct {
	ns     netns.NsHandle
	handle *netlink.Handle
}

func openDeviceNetworkNamespace(path string) (*deviceNetworkNamespace, error) {
	handle, err := netns.GetFromPath(path)
	if err != nil {
		return nil, fmt.Errorf("opening device network namespace %q: %w", path, err)
	}

	netlinkHandle, err := netlink.NewHandleAt(handle)
	if err != nil {
		_ = handle.Close()

		return nil, fmt.Errorf("opening netlink handle for %q: %w", path, err)
	}

	return &deviceNetworkNamespace{ns: handle, handle: netlinkHandle}, nil
}

func (d *deviceNetworkNamespace) Close() error {
	if d == nil {
		return nil
	}

	d.handle.Close()

	return d.ns.Close()
}

// applySysctl writes one package-requested setting inside the private device namespace. Sysctls
// under /proc/sys/net are resolved against the calling thread's network namespace and have no
// netlink interface, so the write runs on a thread that enters the namespace instead of using
// the netlink handle the other operations share.
func (d *deviceNetworkNamespace) applySysctl(name, value string) error {
	result := make(chan error, 1)

	go func() {
		runtime.LockOSThread()

		if err := unix.Setns(int(d.ns), unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()

			result <- fmt.Errorf("entering device network namespace: %w", err)

			return
		}

		result <- writeNetSysctl(name, value)
		// The goroutine returns while the thread is still in the device namespace. The runtime
		// terminates a thread whose locked goroutine exits, so the namespace entry cannot leak
		// into unrelated work on a reused thread.
	}()

	return <-result
}

func (d *deviceNetworkNamespace) ensureLoopbackUp() error {
	loopback, err := d.handle.LinkByName(deviceNetnsLoopbackName)
	if err != nil {
		return fmt.Errorf("reading device netns loopback: %w", err)
	}

	if loopback.Attrs().Flags&net.FlagUp != 0 {
		return nil
	}

	return d.handle.LinkSetUp(loopback)
}

// moveDeviceLeg moves a freshly created device-facing link from the Pod namespace into the
// private namespace. A link that is already absent is already isolated.
func (d *deviceNetworkNamespace) moveDeviceLeg(name string) error {
	link, present, err := lookupLink(name)
	if err != nil || !present {
		return err
	}

	if err = netlink.LinkSetNsFd(link, int(d.ns)); err != nil {
		return fmt.Errorf("moving device leg %q into the private netns: %w", name, err)
	}

	return nil
}

func (d *deviceNetworkNamespace) link(name string) (netlink.Link, bool, error) {
	link, err := d.handle.LinkByName(name)
	if err == nil {
		return link, true, nil
	}

	if _, notFound := errors.AsType[netlink.LinkNotFoundError](err); notFound {
		return nil, false, nil
	}

	return nil, false, err
}

func (d *deviceNetworkNamespace) ensureLinkUp(name string) error {
	link, present, err := d.link(name)
	if err != nil || !present {
		return err
	}

	if link.Attrs().Flags&net.FlagUp != 0 {
		return nil
	}

	if err = d.handle.LinkSetUp(link); err != nil {
		return fmt.Errorf("bringing device netns link %q up: %w", name, err)
	}

	return nil
}

// addressSyntheticDeviceLeg mirrors addressDeviceLeg inside the private namespace. Only an
// untouched, sidecar-owned leg is addressed; a device that adopted the leg owns it from then
// on and the sidecar must not fight it.
func (d *deviceNetworkNamespace) addressSyntheticDeviceLeg(
	spec InterpositionSpec,
	bringUp bool,
) error {
	device, present, err := d.link(spec.DeviceInterface)
	if err != nil || !present {
		return err
	}

	deviceAddress, err := netlink.ParseAddr(spec.ManagementIPv4)
	if err != nil {
		return fmt.Errorf("parsing management address: %w", err)
	}

	addresses, err := d.handle.AddrList(device, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("reading isolated device leg addresses: %w", err)
	}

	if device.Attrs().Alias == interpositionOwnerAlias && len(addresses) == 0 &&
		device.Attrs().OperState != netlink.OperUp {
		if err = d.handle.AddrReplace(device, deviceAddress); err != nil {
			return fmt.Errorf("addressing isolated device leg: %w", err)
		}
	}

	if spec.ManagementIPv6 != "" && device.Attrs().Alias == interpositionOwnerAlias &&
		device.Attrs().OperState != netlink.OperUp {
		if v6, v6Err := netlink.ParseAddr(spec.ManagementIPv6); v6Err == nil {
			v6Addresses, _ := d.handle.AddrList(device, netlink.FAMILY_V6)
			if len(v6Addresses) == 0 {
				_ = d.handle.AddrReplace(device, v6)
			}
		}
	}

	if !bringUp {
		return nil
	}

	if err = d.handle.LinkSetUp(device); err != nil {
		return fmt.Errorf("bringing isolated device leg up: %w", err)
	}

	return nil
}

// ensureDeviceDefaultRoute gives the isolated device the container-runtime default route:
// main-table default via the management gateway on its own leg, exactly as the shared-namespace
// runtime does. The gateway is a remote peer in this namespace (the router leg lives in the Pod
// namespace), so the route is an ordinary on-link next hop.
func (d *deviceNetworkNamespace) ensureDeviceDefaultRoute(
	spec InterpositionSpec,
	gateway netip.Addr,
) error {
	if !gateway.Is4() {
		return nil
	}

	device, present, err := d.link(spec.DeviceInterface)
	if err != nil || !present {
		return err
	}

	prefix, err := netip.ParsePrefix(spec.ManagementIPv4)
	if err != nil {
		return fmt.Errorf("parsing isolated management address: %w", err)
	}

	addresses, err := d.handle.AddrList(device, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("reading isolated management addresses: %w", err)
	}

	carries := false

	for _, address := range addresses {
		if candidate, ok := netip.AddrFromSlice(address.IP); ok &&
			prefix.Masked().Contains(candidate.Unmap()) {
			carries = true
		}
	}

	if !carries || device.Attrs().Flags&net.FlagUp == 0 {
		return nil
	}

	desired := &netlink.Route{
		Table:     unix.RT_TABLE_MAIN,
		LinkIndex: device.Attrs().Index,
		Gw:        gateway.AsSlice(),
	}

	routes, err := d.handle.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: unix.RT_TABLE_MAIN},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return fmt.Errorf("listing isolated main table routes: %w", err)
	}

	for _, route := range routes {
		if !isDefaultRouteDestination(route.Dst) {
			continue
		}

		if route.LinkIndex == desired.LinkIndex && route.Gw != nil &&
			route.Gw.Equal(desired.Gw) {
			return nil
		}

		stale := route
		if err = d.handle.RouteDel(&stale); err != nil {
			return fmt.Errorf("removing isolated main table default: %w", err)
		}
	}

	if err = d.handle.RouteReplace(desired); err != nil {
		return fmt.Errorf("asserting isolated main table default: %w", err)
	}

	return nil
}

func (d *deviceNetworkNamespace) disableTxChecksumOffload(name string) error {
	_, present, err := d.link(name)
	if err != nil || !present {
		return err
	}

	return executeInNetns(int(d.ns), func() error {
		return (netlinkOperations{}).DisableTxChecksumOffload(name)
	})
}

// executeInNetns runs one operation on a thread temporarily entered into the target namespace.
// It exists for kernel interfaces that resolve names through the thread's namespace; netlink
// operations should prefer a handle created with netlink.NewHandleAt.
func executeInNetns(fd int, operation func() error) error {
	result := make(chan error, 1)

	go func() {
		runtime.LockOSThread()

		current, err := netns.Get()
		if err != nil {
			runtime.UnlockOSThread()

			result <- fmt.Errorf("reading current network namespace: %w", err)

			return
		}

		defer current.Close() //nolint:errcheck // handle used for restoration only.

		if err = unix.Setns(fd, unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()

			result <- fmt.Errorf("entering device network namespace: %w", err)

			return
		}

		operationErr := operation()

		restoreErr := unix.Setns(int(current), unix.CLONE_NEWNET)
		if restoreErr == nil {
			runtime.UnlockOSThread()
		}

		result <- errors.Join(operationErr, restoreErr)
	}()

	return <-result
}
