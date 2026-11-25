//go:build linux

package directruntime

import (
	"errors"
	"fmt"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"github.com/vishvananda/netlink"
)

const (
	// netemHandleMajor marks the root netem qdiscs the sidecar owns ("c9:"), so it removes only
	// its own impairment when a Link stops declaring one and never a qdisc the device or a user
	// installed.
	netemHandleMajor = 0xc9
	// netemLimit is the number of packets the qdisc holds while delaying them, the same limit
	// containerlab programs.
	netemLimit = 10000
	// netemKbitBytes converts a rate in kbit/s to the bytes/s the kernel takes.
	netemKbitBytes = 1000 / 8
)

func (o netlinkOperations) EnsureNetem(
	interfaceName string,
	netem *clabernetesinternaldeviceplan.Netem,
) error {
	// Fabric and host endpoints move their device leg into the isolated device namespace;
	// every other endpoint stays in the Pod namespace the sidecar runs in.
	handle := &netlink.Handle{}

	if o.deviceNetns != nil {
		_, isolated, err := o.deviceNetns.link(interfaceName)
		if err != nil {
			return err
		}

		if isolated {
			handle = o.deviceNetns.handle
		}
	}

	return ensureNetem(handle, interfaceName, netem)
}

func ensureNetem(
	handle *netlink.Handle,
	interfaceName string,
	netem *clabernetesinternaldeviceplan.Netem,
) error {
	link, err := handle.LinkByName(interfaceName)
	if err != nil {
		// An endpoint that is not realized yet gets its impairment on a later pass.
		if errors.As(err, new(netlink.LinkNotFoundError)) {
			return nil
		}

		return fmt.Errorf("looking up interface %q: %w", interfaceName, err)
	}

	current, err := rootNetemQdisc(handle, link)
	if err != nil {
		return fmt.Errorf("listing qdiscs of %q: %w", interfaceName, err)
	}

	owned := current != nil && current.Attrs().Handle == netlink.MakeHandle(netemHandleMajor, 0)

	if netem == nil {
		if !owned {
			return nil
		}

		if err = handle.QdiscDel(current); err != nil {
			return fmt.Errorf("removing the netem impairment of %q: %w", interfaceName, err)
		}

		return nil
	}

	desired := desiredNetemQdisc(link.Attrs().Index, netem)
	if owned && sameNetemQdisc(current, desired) {
		return nil
	}

	// The kernel changes an existing netem qdisc in place and keeps the rate and corruption a
	// request leaves out, which netlink does for zero values. The sidecar's own qdisc is
	// therefore recreated rather than changed, so an impairment can drop them.
	if owned {
		if err = handle.QdiscDel(current); err != nil {
			return fmt.Errorf("replacing the netem impairment of %q: %w", interfaceName, err)
		}
	}

	if err = handle.QdiscReplace(desired); err != nil {
		return fmt.Errorf("setting the netem impairment of %q: %w", interfaceName, err)
	}

	return nil
}

// rootNetemQdisc returns the root qdisc of the link when it is a netem qdisc, whoever installed it.
func rootNetemQdisc(handle *netlink.Handle, link netlink.Link) (*netlink.Netem, error) {
	qdiscs, err := handle.QdiscList(link)
	if err != nil {
		return nil, err
	}

	for _, qdisc := range qdiscs {
		if root, ok := qdisc.(*netlink.Netem); ok &&
			root.Attrs().Parent == netlink.HANDLE_ROOT {
			return root, nil
		}
	}

	return nil, nil //nolint:nilnil // a link without a root netem qdisc is a valid observation.
}

// desiredNetemQdisc builds the root netem qdisc for an impairment. netlink converts the delay and
// jitter to 32-bit kernel ticks, which the API bounds keep from overflowing.
func desiredNetemQdisc(linkIndex int, netem *clabernetesinternaldeviceplan.Netem) *netlink.Netem {
	latency := uint32(netem.Delay.Microseconds()) //nolint:gosec // bounded by MaxNetemDelay.
	jitter := uint32(netem.Jitter.Microseconds()) //nolint:gosec // bounded by MaxNetemJitter.
	rate := uint64(netem.Rate) * netemKbitBytes   //nolint:gosec // bounded by MaxNetemRate.

	return netlink.NewNetem(
		netlink.QdiscAttrs{
			LinkIndex: linkIndex,
			Handle:    netlink.MakeHandle(netemHandleMajor, 0),
			Parent:    netlink.HANDLE_ROOT,
		},
		netlink.NetemQdiscAttrs{
			Latency:     latency,
			Jitter:      jitter,
			Loss:        float32(netem.Loss),
			CorruptProb: float32(netem.Corruption),
			Rate64:      rate,
			Limit:       netemLimit,
		},
	)
}

// sameNetemQdisc compares the fields the sidecar programs, as the kernel reports them back.
func sameNetemQdisc(current, desired *netlink.Netem) bool {
	return current.Latency == desired.Latency &&
		current.Jitter == desired.Jitter &&
		current.Loss == desired.Loss &&
		current.CorruptProb == desired.CorruptProb &&
		current.Rate64 == desired.Rate64 &&
		current.Limit == desired.Limit
}
