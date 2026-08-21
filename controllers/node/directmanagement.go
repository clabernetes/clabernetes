//nolint:err113,gocognit,gocyclo,mnd // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package node

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

type directManagementPool struct {
	network    netip.Prefix
	allocation netip.Prefix
	ipv4       bool
	enabled    bool
}

func newDirectManagementPool(subnet, addressRange string, ipv4 bool) (directManagementPool, error) {
	if subnet == "" {
		if addressRange != "" {
			return directManagementPool{}, errors.New("management range requires a subnet")
		}

		return directManagementPool{ipv4: ipv4}, nil
	}

	network, err := netip.ParsePrefix(subnet)
	if err != nil || network.Addr().Is4() != ipv4 {
		return directManagementPool{}, errors.New("management subnet is invalid")
	}

	network = network.Masked()

	allocation := network
	if addressRange != "" {
		allocation, err = netip.ParsePrefix(addressRange)
		if err != nil || allocation.Addr().Is4() != ipv4 {
			return directManagementPool{}, errors.New("management range is invalid")
		}

		allocation = allocation.Masked()
		if allocation.Bits() < network.Bits() ||
			!network.Contains(allocation.Addr()) ||
			!network.Contains(lastPrefixAddress(allocation)) {
			return directManagementPool{}, errors.New("management range is outside its subnet")
		}
	}

	return directManagementPool{
		network: network, allocation: allocation, ipv4: ipv4, enabled: true,
	}, nil
}

func allocateDirectManagementAddresses(
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
	pool directManagementPool,
	address func(*clabernetesapisv1alpha1.Node) string,
	gateway string,
) (map[string]string, error) {
	result := map[string]string{}
	if !pool.enabled {
		return result, nil
	}

	reserved := map[netip.Addr]string{}

	for name, node := range nodesByName {
		if node == nil || address(node) == "" {
			continue
		}

		candidate, err := parseLooseManagementAddress(address(node), pool.ipv4)
		if err != nil || !pool.allocation.Contains(candidate) {
			continue
		}

		if existing := reserved[candidate]; existing != "" && existing != name {
			return nil, fmt.Errorf(
				"management address %q is declared by both %q and %q",
				candidate,
				existing,
				name,
			)
		}

		reserved[candidate] = name
	}

	gatewayAddress := netip.Addr{}

	if gateway != "" {
		var err error

		gatewayAddress, err = netip.ParseAddr(gateway)
		if err != nil || gatewayAddress.Is4() != pool.ipv4 ||
			!pool.network.Contains(gatewayAddress) {
			return nil, errors.New("management gateway is invalid or outside its subnet")
		}

		if existing := reserved[gatewayAddress]; existing != "" {
			return nil, fmt.Errorf(
				"management gateway address is already declared by Node %q",
				existing,
			)
		}

		reserved[gatewayAddress] = "gateway"
	}

	names := make([]string, 0, len(nodesByName))
	for name, node := range nodesByName {
		if node != nil && address(node) == "" {
			names = append(names, name)
		}
	}

	slices.SortFunc(names, func(left, right string) int {
		leftNode, rightNode := nodesByName[left], nodesByName[right]
		leftIdentity := string(leftNode.GetUID()) + "\x00" + left
		rightIdentity := string(rightNode.GetUID()) + "\x00" + right

		return strings.Compare(leftIdentity, rightIdentity)
	})

	maxAttempts := max(4096, len(nodesByName)*1024)
	for _, name := range names {
		node := nodesByName[name]

		seed := string(node.GetUID())
		if seed == "" {
			seed = name
		}

		allocated := netip.Addr{}

		for attempt := range maxAttempts {
			candidate := hashedPrefixAddress(pool.allocation, seed, attempt)
			if !usableManagementAddress(pool, candidate, gatewayAddress) ||
				reserved[candidate] != "" {
				continue
			}

			allocated = candidate

			break
		}

		if !allocated.IsValid() {
			return nil, errors.New("management address range has no free usable address")
		}

		reserved[allocated] = name
		result[name] = netip.PrefixFrom(allocated, pool.network.Bits()).String()
	}

	return result, nil
}

func validateUniqueExplicitManagementAddresses(
	nodesByName map[string]*clabernetesapisv1alpha1.Node,
) error {
	names := make([]string, 0, len(nodesByName))
	for name := range nodesByName {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, family := range []struct {
		ipv4    bool
		address func(*clabernetesapisv1alpha1.Node) string
	}{
		{ipv4: true, address: func(node *clabernetesapisv1alpha1.Node) string {
			return node.Spec.MgmtIPv4
		}},
		{ipv4: false, address: func(node *clabernetesapisv1alpha1.Node) string {
			return node.Spec.MgmtIPv6
		}},
	} {
		owners := map[netip.Addr]string{}

		for _, name := range names {
			node := nodesByName[name]
			if node == nil || family.address(node) == "" {
				continue
			}

			address, err := parseLooseManagementAddress(family.address(node), family.ipv4)
			if err != nil {
				// The owning group's ordinary normalization reports malformed addresses with
				// its applicable subnet context. This pass is only the namespace uniqueness gate.
				continue
			}

			if existing := owners[address]; existing != "" {
				return fmt.Errorf(
					"management address %q is declared by both %q and %q",
					address,
					existing,
					name,
				)
			}

			owners[address] = name
		}
	}

	return nil
}

func normalizeDirectManagementAddress(
	raw string,
	pool directManagementPool,
) (string, error) {
	if raw == "" {
		return "", nil
	}

	if prefix, err := netip.ParsePrefix(raw); err == nil {
		if prefix.Addr().Is4() != pool.ipv4 {
			return "", errors.New("management address has the wrong family")
		}

		if pool.enabled && (prefix.Bits() != pool.network.Bits() ||
			!pool.network.Contains(prefix.Addr())) {
			return "", errors.New("management address is outside its declared subnet")
		}

		return prefix.String(), nil
	}

	address, err := netip.ParseAddr(raw)
	if err != nil || address.Is4() != pool.ipv4 {
		return "", errors.New("management address is invalid")
	}

	if !pool.enabled || !pool.network.Contains(address) {
		return "", errors.New("management address without a prefix requires a matching subnet")
	}

	return netip.PrefixFrom(address, pool.network.Bits()).String(), nil
}

func validateDirectManagementGateway(raw, source string, ipv4 bool) error {
	if raw == "" {
		return nil
	}

	prefix, err := netip.ParsePrefix(source)
	if err != nil || prefix.Addr().Is4() != ipv4 {
		return errors.New("management gateway requires a same-family source address")
	}

	gateway, err := netip.ParseAddr(raw)
	if err != nil || gateway.Is4() != ipv4 || !prefix.Contains(gateway) {
		return errors.New("management gateway is invalid or off-link")
	}

	return nil
}

func validateDirectManagementHostAddress(
	raw string,
	pool directManagementPool,
	gateway string,
) error {
	if raw == "" || !pool.enabled {
		return nil
	}

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return errors.New("management address is invalid")
	}

	gatewayAddress := netip.Addr{}
	if gateway != "" {
		gatewayAddress, _ = netip.ParseAddr(gateway)
	}

	if !usableManagementAddress(pool, prefix.Addr(), gatewayAddress) {
		return errors.New("management address is a reserved subnet address")
	}

	return nil
}

func directManagementAddressIdentity(raw string) string {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return ""
	}

	return prefix.Addr().String()
}

func parseLooseManagementAddress(raw string, ipv4 bool) (netip.Addr, error) {
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		if prefix.Addr().Is4() != ipv4 {
			return netip.Addr{}, errors.New("management address has the wrong family")
		}

		return prefix.Addr(), nil
	}

	address, err := netip.ParseAddr(raw)
	if err != nil || address.Is4() != ipv4 {
		return netip.Addr{}, errors.New("management address is invalid")
	}

	return address, nil
}

func usableManagementAddress(
	pool directManagementPool,
	address,
	gateway netip.Addr,
) bool {
	if !address.IsValid() || address == pool.allocation.Masked().Addr() || address == gateway {
		return false
	}

	if pool.ipv4 && address == lastPrefixAddress(pool.allocation) {
		return false
	}

	return true
}

func hashedPrefixAddress(prefix netip.Prefix, seed string, attempt int) netip.Addr {
	digest := sha256.Sum256([]byte(seed + "\x00" + strconv.Itoa(attempt)))

	if prefix.Addr().Is4() {
		value := prefix.Masked().Addr().As4()
		for bit := prefix.Bits(); bit < 32; bit++ {
			copyHashBit(value[:], digest[:], bit, bit-prefix.Bits())
		}

		return netip.AddrFrom4(value)
	}

	value := prefix.Masked().Addr().As16()
	for bit := prefix.Bits(); bit < 128; bit++ {
		copyHashBit(value[:], digest[:], bit, bit-prefix.Bits())
	}

	return netip.AddrFrom16(value)
}

func copyHashBit(destination, digest []byte, destinationBit, digestBit int) {
	destinationMask := byte(1 << (7 - destinationBit%8))

	digestMask := byte(1 << (7 - digestBit%8))
	if digest[digestBit/8]&digestMask != 0 {
		destination[destinationBit/8] |= destinationMask
	} else {
		destination[destinationBit/8] &^= destinationMask
	}
}

func lastPrefixAddress(prefix netip.Prefix) netip.Addr {
	if prefix.Addr().Is4() {
		value := prefix.Masked().Addr().As4()
		for bit := prefix.Bits(); bit < 32; bit++ {
			value[bit/8] |= byte(1 << (7 - bit%8))
		}

		return netip.AddrFrom4(value)
	}

	value := prefix.Masked().Addr().As16()
	for bit := prefix.Bits(); bit < 128; bit++ {
		value[bit/8] |= byte(1 << (7 - bit%8))
	}

	return netip.AddrFrom16(value)
}

func directManagementError(field, message string) error {
	return planInputError(
		clabernetesinternaldeviceplan.ErrorInvalidInput,
		"launcherProfile.mgmt."+field,
		message,
	)
}

func directNodeManagementError(
	node *clabernetesapisv1alpha1.Node,
	field,
	message string,
) error {
	return &clabernetesinternaldeviceplan.Error{
		Code: clabernetesinternaldeviceplan.ErrorInvalidInput, NodeID: string(node.GetUID()),
		Field:    "nodes." + node.GetName() + ".spec." + field,
		Behavior: "controller-input", Message: message,
	}
}

// defaultManagementIPv4Subnet mirrors the pinned containerlab core default management network
// (containerlab core/config.go dockerNetIPv4Addr), so a topology without a management policy
// allocates node management identities exactly as containerlab's default network would.
const defaultManagementIPv4Subnet = "172.20.20.0/24"

// applyDefaultManagementPolicy completes an operator policy into a fully allocatable one: a
// missing IPv4 subnet becomes containerlab's default management subnet, and a missing gateway
// becomes its subnet's first usable address (containerlab's bridge-gateway convention). The
// direct runtime never uses the Pod address as a management identity, so every topology needs a
// complete policy.
func applyDefaultManagementPolicy(settings *clabernetesapisv1alpha1.ManagementPolicy) error {
	if settings.IPv4Subnet == "" {
		settings.IPv4Subnet = defaultManagementIPv4Subnet
	}

	if settings.IPv4Gw == "" {
		gateway, err := firstUsableManagementAddress(settings.IPv4Subnet)
		if err != nil {
			return directManagementError("ipv4-subnet", err.Error())
		}

		settings.IPv4Gw = gateway
	}

	if settings.IPv6Subnet != "" && settings.IPv6Gw == "" {
		gateway, err := firstUsableManagementAddress(settings.IPv6Subnet)
		if err != nil {
			return directManagementError("ipv6-subnet", err.Error())
		}

		settings.IPv6Gw = gateway
	}

	return nil
}

func firstUsableManagementAddress(subnet string) (string, error) {
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return "", errors.New("management subnet is invalid")
	}

	return prefix.Masked().Addr().Next().String(), nil
}
