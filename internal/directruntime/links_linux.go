//go:build linux

//nolint:err113,funlen,gocognit,gocyclo,mnd,nestif // single-pass boundary logic with structured one-off diagnostics and protocol literals.
package directruntime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
)

type peerAddressResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type netlinkOperations struct {
	resolver peerAddressResolver
}

const vethLinkType = "veth"

const managementAddressReadyTimeout = 5 * time.Second

var errVethOwnership = errors.New("veth ownership invariant failed")

func newLinkOperations(networkNamespace EndpointNamespace) LinkOperations {
	resolver := peerAddressResolver(net.DefaultResolver)
	if networkNamespace != nil {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial:     networkNamespaceDialContext(networkNamespace),
		}
	}

	return netlinkOperations{resolver: resolver}
}

func (netlinkOperations) EnsureSysctl(name, value string) error {
	if !validLinuxSysctlName(name) {
		return errors.New("sysctl name is invalid")
	}

	parts := strings.Split(name, ".")

	path := filepath.Join(append([]string{"/proc/sys"}, parts...)...)
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil { //nolint:gosec // the artifact must be readable by the device process.
		return fmt.Errorf("writing sysctl: %w", err)
	}

	return nil
}

func (netlinkOperations) ListVethInterfaces(ownerPrefix string) ([]VethInterface, error) {
	if ownerPrefix == "" {
		return nil, fmt.Errorf("%w: owner prefix is empty", errVethOwnership)
	}

	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}

	linksByIndex := make(map[int]netlink.Link, len(links))
	for _, link := range links {
		linksByIndex[link.Attrs().Index] = link
	}

	result := []VethInterface{}

	for _, link := range links {
		attributes := link.Attrs()
		if !strings.HasPrefix(attributes.Alias, ownerPrefix) {
			continue
		}

		if link.Type() != vethLinkType {
			return nil, fmt.Errorf(
				"%w: Pod-owned interface %q is not a veth",
				errVethOwnership,
				attributes.Name,
			)
		}

		peerIndex, peerErr := netlink.VethPeerIndex(&netlink.Veth{LinkAttrs: *attributes})
		if peerErr != nil {
			return nil, fmt.Errorf("reading veth peer for %q: %w", attributes.Name, peerErr)
		}

		peer := linksByIndex[peerIndex]
		if peer == nil {
			return nil, fmt.Errorf(
				"%w: veth peer for %q is unavailable",
				errVethOwnership,
				attributes.Name,
			)
		}

		result = append(result, VethInterface{
			Name: attributes.Name, PeerName: peer.Attrs().Name, Owner: attributes.Alias,
		})
	}

	slices.SortFunc(result, func(left, right VethInterface) int {
		return strings.Compare(left.Name, right.Name)
	})

	return result, nil
}

func (netlinkOperations) EnsureVethPair(
	leftName,
	rightName string,
	mtu int,
	owner string,
) error {
	if mtu < 0 || uint64(mtu) > math.MaxUint32 {
		return fmt.Errorf("%w: MTU is outside the supported range", errVethOwnership)
	}

	left, leftExists, err := lookupLink(leftName)
	if err != nil {
		return err
	}

	right, rightExists, err := lookupLink(rightName)
	if err != nil {
		return err
	}

	if leftExists != rightExists {
		return errors.New("only one veth endpoint already exists")
	}

	created := false

	if !leftExists {
		attributes := netlink.NewLinkAttrs()
		attributes.Name = leftName

		attributes.Alias = owner
		if mtu != 0 {
			attributes.MTU = mtu
		}

		veth := netlink.NewVeth(attributes)

		veth.PeerName = rightName
		if mtu != 0 {
			veth.PeerMTU = uint32(mtu)
		}

		if err = netlink.LinkAdd(veth); err != nil {
			return fmt.Errorf("creating veth pair: %w", err)
		}

		created = true

		left, _, err = lookupLink(leftName)
		if err == nil {
			right, _, err = lookupLink(rightName)
		}

		if err != nil {
			if left != nil {
				_ = netlink.LinkDel(left)
			}

			return fmt.Errorf("reading created veth pair: %w", err)
		}

		if err = netlink.LinkSetAlias(left, owner); err == nil {
			err = netlink.LinkSetAlias(right, owner)
		}

		if err != nil {
			_ = netlink.LinkDel(left)

			return fmt.Errorf("marking created veth pair: %w", err)
		}

		left, _, err = lookupLink(leftName)
		if err == nil {
			right, _, err = lookupLink(rightName)
		}

		if err != nil {
			if left != nil {
				_ = netlink.LinkDel(left)
			}

			return fmt.Errorf("reading marked veth pair: %w", err)
		}
	}

	if left.Type() != vethLinkType || right.Type() != vethLinkType ||
		left.Attrs().Alias != owner || right.Attrs().Alias != owner {
		if created {
			_ = netlink.LinkDel(left)
		}

		return fmt.Errorf(
			"interface names collide with foreign state: left=%s/%q right=%s/%q",
			left.Type(),
			left.Attrs().Alias,
			right.Type(),
			right.Attrs().Alias,
		)
	}

	peerIndex, err := netlink.VethPeerIndex(&netlink.Veth{LinkAttrs: *left.Attrs()})
	if err != nil || peerIndex != right.Attrs().Index {
		if created {
			_ = netlink.LinkDel(left)
		}

		return errors.New("existing veth endpoints are not peers")
	}

	for _, link := range []netlink.Link{left, right} {
		if mtu != 0 && link.Attrs().MTU != mtu {
			if err = netlink.LinkSetMTU(link, mtu); err != nil {
				return fmt.Errorf("setting veth MTU: %w", err)
			}
		}

		if link.Attrs().Flags&net.FlagUp == 0 {
			if err = netlink.LinkSetUp(link); err != nil {
				return fmt.Errorf("bringing veth endpoint up: %w", err)
			}
		}
	}

	return nil
}

func (netlinkOperations) DeleteVethPair(name, owner string) error {
	if name == "" || owner == "" {
		return fmt.Errorf("%w: deletion identity is incomplete", errVethOwnership)
	}

	link, exists, err := lookupLink(name)
	if err != nil || !exists {
		return err
	}

	if link.Type() != vethLinkType || link.Attrs().Alias != owner {
		return fmt.Errorf(
			"%w: interface %q is not the requested owned veth",
			errVethOwnership,
			name,
		)
	}

	peerIndex, err := netlink.VethPeerIndex(&netlink.Veth{LinkAttrs: *link.Attrs()})
	if err != nil {
		return fmt.Errorf("reading veth peer for %q: %w", name, err)
	}

	peer, err := netlink.LinkByIndex(peerIndex)
	if err != nil {
		return fmt.Errorf("reading veth peer for %q: %w", name, err)
	}

	if peer.Type() != vethLinkType || peer.Attrs().Alias != owner {
		return fmt.Errorf(
			"%w: veth peer for %q has another owner",
			errVethOwnership,
			name,
		)
	}

	if err = netlink.LinkDel(link); err != nil {
		return fmt.Errorf("deleting owned veth pair for %q: %w", name, err)
	}

	return nil
}

func (netlinkOperations) ResolvePodTransportInterface(podAddress string) (string, error) {
	target := net.ParseIP(podAddress)
	if target == nil {
		return "", errors.New("Pod transport address is invalid")
	}

	family := netlink.FAMILY_V6
	if target.To4() != nil {
		family = netlink.FAMILY_V4
		target = target.To4()
	}

	links, err := netlink.LinkList()
	if err != nil {
		return "", fmt.Errorf("listing interfaces for Pod transport address: %w", err)
	}

	matches := []string{}

	for _, link := range links {
		addresses, listErr := netlink.AddrList(link, family)
		if listErr != nil {
			return "", fmt.Errorf("listing addresses for Pod transport interface: %w", listErr)
		}

		for _, address := range addresses {
			if address.IP.Equal(target) {
				matches = append(matches, link.Attrs().Name)

				break
			}
		}
	}

	slices.Sort(matches)

	if len(matches) != 1 {
		return "", fmt.Errorf(
			"Pod transport address belongs to %d interfaces, want exactly one",
			len(matches),
		)
	}

	return matches[0], nil
}

func (netlinkOperations) EnsureManagementAddress(
	interfaceName,
	address,
	owner string,
) error {
	if owner == "" {
		return errors.New("management address owner is empty")
	}

	link, exists, err := lookupLink(interfaceName)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("management interface %q does not exist", interfaceName)
	}

	requested, err := netlink.ParseAddr(address)
	if err != nil {
		return fmt.Errorf("parsing management address: %w", err)
	}

	family := netlink.FAMILY_V6
	if requested.IP.To4() != nil {
		family = netlink.FAMILY_V4
	}

	existing, err := netlink.AddrList(link, family)
	if err != nil {
		return fmt.Errorf("listing management addresses: %w", err)
	}

	requestedBits, _ := requested.Mask.Size()
	for _, candidate := range existing {
		if !candidate.IP.Equal(requested.IP) {
			continue
		}

		candidateBits, _ := candidate.Mask.Size()
		if candidateBits != requestedBits {
			return errors.New("management address exists with another prefix length")
		}

		if err = ensureLinkUp(link); err != nil {
			return err
		}

		return waitForManagementAddress(link, requested, family)
	}

	if err = ensureLinkUp(link); err != nil {
		return err
	}

	if err = netlink.AddrAdd(link, requested); err != nil {
		return fmt.Errorf("adding management address: %w", err)
	}

	return waitForManagementAddress(link, requested, family)
}

func waitForManagementAddress(link netlink.Link, requested *netlink.Addr, family int) error {
	deadline := time.Now().Add(managementAddressReadyTimeout)
	for time.Now().Before(deadline) {
		addresses, err := netlink.AddrList(link, family)
		if err != nil {
			return fmt.Errorf("observing management address readiness: %w", err)
		}

		requestedBits, _ := requested.Mask.Size()
		for _, address := range addresses {
			bits, _ := address.Mask.Size()
			if !address.IP.Equal(requested.IP) || bits != requestedBits {
				continue
			}

			if address.Flags&syscall.IFA_F_DADFAILED != 0 {
				return errors.New("management address failed duplicate-address detection")
			}

			if address.Flags&syscall.IFA_F_TENTATIVE == 0 {
				return nil
			}
		}

		time.Sleep(25 * time.Millisecond)
	}

	return errors.New("management address did not become usable before deadline")
}

func (netlinkOperations) EnsureManagementRoute(
	interfaceName,
	source,
	destination,
	gateway string,
	metric,
	table int,
	owner string,
) error {
	if owner == "" || table < 1 || metric < 0 {
		return errors.New("management route identity is invalid")
	}

	link, exists, err := lookupLink(interfaceName)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("management interface %q does not exist", interfaceName)
	}

	_, sourceNetwork, err := net.ParseCIDR(source)
	if err != nil {
		return fmt.Errorf("parsing management route source: %w", err)
	}

	sourceAddress, _, err := net.ParseCIDR(source)
	if err != nil {
		return fmt.Errorf("parsing management route source address: %w", err)
	}

	_, destinationNetwork, err := net.ParseCIDR(destination)
	if err != nil {
		return fmt.Errorf("parsing management route destination: %w", err)
	}

	var gatewayAddress net.IP
	if gateway != "" {
		gatewayAddress = net.ParseIP(gateway)
		if gatewayAddress == nil {
			return errors.New("parsing management route gateway")
		}
	}

	family := netlink.FAMILY_V6
	hostBits := 128

	if sourceAddress.To4() != nil {
		family = netlink.FAMILY_V4
		hostBits = 32
		sourceAddress = sourceAddress.To4()
	}

	if err = ensureSourceRule(sourceAddress, hostBits, family, table); err != nil {
		return err
	}

	connected := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       sourceNetwork,
		Src:       sourceAddress,
		Scope:     netlink.SCOPE_LINK,
		Table:     table,
		Protocol:  syscall.RTPROT_STATIC,
	}
	if err = netlink.RouteReplace(connected); err != nil {
		return fmt.Errorf("ensuring source-specific connected route: %w", err)
	}

	if gatewayAddress == nil && sourceNetwork.String() == destinationNetwork.String() {
		return nil
	}

	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       destinationNetwork,
		Src:       sourceAddress,
		Gw:        gatewayAddress,
		Priority:  metric,
		Table:     table,
		Protocol:  syscall.RTPROT_STATIC,
	}
	if err = netlink.RouteReplace(route); err != nil {
		return fmt.Errorf("ensuring source-specific management route: %w", err)
	}

	return nil
}

func ensureSourceRule(source net.IP, hostBits, family, table int) error {
	rules, err := netlink.RuleList(family)
	if err != nil {
		return fmt.Errorf("listing management source rules: %w", err)
	}

	priority := table + managementRouteTableBase
	sourceNetwork := &net.IPNet{IP: source, Mask: net.CIDRMask(hostBits, hostBits)}

	for _, existing := range rules {
		if existing.Priority != priority {
			continue
		}

		if existing.Table == table && existing.Src != nil &&
			existing.Src.String() == sourceNetwork.String() {
			return nil
		}

		return errors.New("management source-rule priority collides with foreign state")
	}

	rule := netlink.NewRule()
	rule.Family = family
	rule.Priority = priority
	rule.Table = table

	rule.Src = sourceNetwork
	if err = netlink.RuleAdd(rule); err != nil {
		return fmt.Errorf("adding management source rule: %w", err)
	}

	return nil
}

func ensureLinkUp(link netlink.Link) error {
	if link.Attrs().Flags&net.FlagUp != 0 {
		return nil
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bringing interface up: %w", err)
	}

	return nil
}

func lookupLink(name string) (netlink.Link, bool, error) {
	link, err := netlink.LinkByName(name)
	if err == nil {
		return link, true, nil
	}

	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		return nil, false, nil
	}

	return nil, false, fmt.Errorf("looking up interface %q: %w", name, err)
}
