package directruntime

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

const (
	// InterpositionConditionsFile records the named interposition readiness conditions in the
	// connectivity state directory, so probe failures surface the exact failed invariant.
	InterpositionConditionsFile = "interposition-conditions"
	// TransportInterfaceName is the sidecar-owned identity of the preserved CNI interface. The
	// device never receives this name and must never own this interface.
	TransportInterfaceName = "c9s0"
	// RouterInterfaceName is the sidecar-owned router leg of the synthetic management pair; it
	// carries the management gateway address.
	RouterInterfaceName = "c9sr0"
	// interpositionTransportTable is the sidecar-owned policy routing table carrying Kubernetes
	// transport, distinct from the source-specific management tables.
	interpositionTransportTable = 20_000
	// interpositionRouterRulePriority selects the transport table for device-originated traffic
	// entering through the router leg.
	interpositionRouterRulePriority = 900
	// interpositionTransportRulePriority selects the transport table for Pod-sourced traffic:
	// transport replies and the fabric VTEPs (whose encapsulation sources the Pod address).
	// Scoping by source keeps a kernel-dataplane device's own data routes in main authoritative.
	interpositionTransportRulePriority = 901
	// interpositionManagementRulePriority selects the transport table for traffic to the
	// management subnet, so application hooks reach the device even when a device stripped or
	// rewrote the main table.
	interpositionManagementRulePriority = 902
)

// InterpositionSpec is the complete pod-namespace state one interposed management identity
// requires, derived entirely from the plan and the Pod's downward-API identity.
type InterpositionSpec struct {
	// PodAddress is the bare kubelet-assigned Pod IPv4 address.
	PodAddress string
	// TransportInterface is the sidecar-owned name for the preserved CNI interface.
	TransportInterface string
	// RouterInterface is the sidecar-owned router leg name.
	RouterInterface string
	// DeviceInterface is the synthetic device-leg name the device expects.
	DeviceInterface string
	// DeviceMAC optionally pins the device-leg MAC address.
	DeviceMAC string
	// ManagementIPv4 is the allocated management address in CIDR form.
	ManagementIPv4 string
	// GatewayIPv4 is the bare management gateway address carried by the router leg.
	GatewayIPv4 string
	// ManagementIPv6 optionally carries an allocated IPv6 management address in CIDR form; it is
	// assigned to the device leg without translation.
	ManagementIPv6 string
	// GatewayIPv6 optionally carries the bare IPv6 management gateway for the router leg.
	GatewayIPv6 string
	// StateDirectory is the sidecar-owned state root where the captured transport gateway is
	// persisted, so re-assertion survives a device stripping routes from every table.
	StateDirectory string
}

var errInterposition = errors.New("management interposition invariant failed")

// interposedManagementEntry selects the single interposed management entry of a Pod plan. A Pod
// has exactly one synthetic management leg; more than one interposed entry is a planning
// invariant violation.
func interposedManagementEntry(
	plan clabernetesinternaldeviceplan.Plan,
) (*clabernetesinternaldeviceplan.ManagementPlan, error) {
	var selected *clabernetesinternaldeviceplan.ManagementPlan

	for index := range plan.Management {
		entry := &plan.Management[index]
		if entry.InterfaceSelector != clabernetesinternaldeviceplan.ManagementInterfaceInterposed {
			continue
		}

		if selected != nil {
			return nil, fmt.Errorf(
				"%w: plan carries more than one interposed management entry",
				errInterposition,
			)
		}

		selected = entry
	}

	return selected, nil
}

// interpositionSpecForEntry validates and converts one interposed plan entry into the namespace
// spec. It fails closed on every incomplete identity rather than degrading.
func interpositionSpecForEntry(
	entry *clabernetesinternaldeviceplan.ManagementPlan,
	podAddress string,
) (InterpositionSpec, InterpositionNATSpec, error) {
	spec := InterpositionSpec{
		PodAddress:         strings.TrimSpace(podAddress),
		TransportInterface: TransportInterfaceName,
		RouterInterface:    RouterInterfaceName,
	}

	if entry.Interposition == nil {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q carries no contract",
			errInterposition,
			entry.ID,
		)
	}

	if spec.PodAddress == "" {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: Pod address is required for interposition",
			errInterposition,
		)
	}

	managementPrefix, err := netip.ParsePrefix(entry.IPv4)
	if err != nil {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q management address %q is invalid",
			errInterposition,
			entry.ID,
			entry.IPv4,
		)
	}

	gateway, err := netip.ParseAddr(entry.IPv4Gateway)
	if err != nil || !managementPrefix.Masked().Contains(gateway) {
		return spec, InterpositionNATSpec{}, fmt.Errorf(
			"%w: interposed entry %q gateway %q is invalid for %q",
			errInterposition,
			entry.ID,
			entry.IPv4Gateway,
			entry.IPv4,
		)
	}

	spec.DeviceInterface = entry.Interposition.DeviceInterface
	spec.DeviceMAC = entry.Interposition.DeviceMAC
	spec.ManagementIPv4 = entry.IPv4
	spec.GatewayIPv4 = entry.IPv4Gateway
	spec.ManagementIPv6 = entry.IPv6
	spec.GatewayIPv6 = entry.IPv6Gateway

	natSpec := InterpositionNATSpec{
		PodAddress:         spec.PodAddress,
		ManagementAddress:  managementPrefix.Addr().String(),
		ManagementSubnet:   managementPrefix.Masked().String(),
		TransportInterface: spec.TransportInterface,
		DeviceInterface:    spec.DeviceInterface,
	}

	for _, port := range entry.Interposition.InboundPorts {
		natSpec.InboundPorts = append(natSpec.InboundPorts, InterpositionPortMap{
			Protocol:   port.Protocol,
			PodPort:    port.PodPort,
			DevicePort: port.DevicePort,
		})
	}

	return spec, natSpec, nil
}

// reconcileInterposition converges the Pod namespace to the plan's interposed management
// identity. It runs before any device container starts and again on every revision tick so
// sidecar-owned state displaced by a device is re-asserted. It never mutates device-owned state.
func reconcileInterposition(
	plan clabernetesinternaldeviceplan.Plan,
	options ConnectivityOptions,
	operations LinkOperations,
) error {
	entry, err := interposedManagementEntry(plan)
	if err != nil {
		return err
	}

	if entry == nil {
		return nil
	}

	spec, natSpec, err := interpositionSpecForEntry(entry, options.PodAddress)
	if err != nil {
		return err
	}

	spec.StateDirectory = options.StateDirectory

	if err := operations.EnsureInterposition(spec); err != nil {
		recordInterpositionConditions(options.StateDirectory, err, nil)

		return fmt.Errorf("ensuring management interposition: %w", err)
	}

	if options.NATOperations == nil {
		err := fmt.Errorf("%w: translation operations are unavailable", errInterposition)
		recordInterpositionConditions(options.StateDirectory, nil, err)

		return err
	}

	if err := options.NATOperations.EnsureInterpositionNAT(natSpec); err != nil {
		recordInterpositionConditions(options.StateDirectory, nil, err)

		return fmt.Errorf("ensuring management translation: %w", err)
	}

	recordInterpositionConditions(options.StateDirectory, nil, nil)

	return nil
}

// recordInterpositionConditions persists the two named interposition conditions. Recording is
// best-effort observability: the fail-closed path is the returned error itself.
func recordInterpositionConditions(stateDirectory string, underlayErr, translationErr error) {
	if stateDirectory == "" {
		return
	}

	condition := func(name string, failure error, blocked bool) string {
		switch {
		case failure != nil:
			return name + "=False: " + failure.Error() + "\n"
		case blocked:
			return name + "=Unknown: blocked by an earlier condition\n"
		default:
			return name + "=True\n"
		}
	}

	content := condition("CNIUnderlayPreserved", underlayErr, false) +
		condition("ManagementTranslationReady", translationErr, underlayErr != nil)

	//nolint:gosec,mnd // non-sensitive sidecar-owned observability record, standard file mode.
	_ = os.WriteFile(
		filepath.Join(filepath.Clean(stateDirectory), InterpositionConditionsFile),
		[]byte(content),
		0o644,
	)
}
