//go:build linux

//nolint:err113,gocritic,noinlineerr,perfsprint,wsl_v5 // Exact ownership diagnostics are explicit.
package hostendpoint

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strings"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const hostEndpointLinkType = "veth"

type linuxOperations struct{}

func newOperations() Operations {
	return linuxOperations{}
}

func (linuxOperations) List(ctx context.Context) ([]OwnedEndpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("opening host netlink handle: %w", err)
	}
	defer handle.Close()
	links, err := handle.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing host interfaces: %w", err)
	}
	result := []OwnedEndpoint{}
	for _, link := range links {
		ownership, owned := parseOwnerAlias(link.Attrs().Alias, ownerRoleHost)
		if !owned {
			continue
		}
		if link.Type() != hostEndpointLinkType {
			return nil, fmt.Errorf(
				"owned host interface %q is not a veth",
				link.Attrs().Name,
			)
		}
		result = append(result, OwnedEndpoint{
			HostInterface: link.Attrs().Name,
			Ownership:     ownership,
		})
	}
	slices.SortFunc(result, func(left, right OwnedEndpoint) int {
		return strings.Compare(left.HostInterface, right.HostInterface)
	})

	return result, nil
}

// vethPairIntent parameterizes one owned host-to-Pod veth pair independently of whether it
// realizes a host Link or a fabric endpoint leg.
type vethPairIntent struct {
	hostName  string
	podName   string
	mtu       int
	hostAlias string
	podAlias  string
}

func (linuxOperations) Ensure(
	ctx context.Context,
	endpoint Endpoint,
	pod ObjectIdentity,
	namespaceFD int,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if namespaceFD < 0 {
		return fmt.Errorf("Pod network-namespace handle is invalid")
	}
	if _, err := normalizeRequest(ReconcileRequest{
		SchemaVersion: SchemaVersion,
		Pod:           pod,
		Endpoints:     []Endpoint{endpoint},
	}); err != nil {
		return err
	}
	ownership := ownershipFor(endpoint, pod)
	hostAlias, err := ownerAlias(ownerRoleHost, ownership)
	if err != nil {
		return err
	}
	podAlias, err := ownerAlias(ownerRolePod, ownership)
	if err != nil {
		return err
	}
	hostHandle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("opening host netlink handle: %w", err)
	}
	defer hostHandle.Close()
	podHandle, err := netlink.NewHandleAt(netns.NsHandle(namespaceFD), unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("opening Pod netlink handle: %w", err)
	}
	defer podHandle.Close()
	_, _, err = ensureOwnedVethPair(hostHandle, podHandle, vethPairIntent{
		hostName:  endpoint.HostInterface,
		podName:   endpoint.PodInterface,
		mtu:       endpoint.MTU,
		hostAlias: hostAlias,
		podAlias:  podAlias,
	})

	return err
}

//nolint:funlen,gocognit,gocyclo,nestif // One transaction guards each cross-namespace mutation.
func ensureOwnedVethPair(
	hostHandle *netlink.Handle,
	podHandle *netlink.Handle,
	intent vethPairIntent,
) (netlink.Link, netlink.Link, error) {
	hostLink, hostExists, err := linkByName(hostHandle, intent.hostName)
	if err != nil {
		return nil, nil, err
	}
	if hostExists &&
		(hostLink.Type() != hostEndpointLinkType || hostLink.Attrs().Alias != intent.hostAlias) {
		return nil, nil, fmt.Errorf(
			"host interface %q collides with unrelated state",
			intent.hostName,
		)
	}
	podLink, err := ownedPodLink(podHandle, intent.podAlias)
	if err != nil {
		return nil, nil, err
	}
	podHostLink, err := ownedPodLink(podHandle, intent.hostAlias)
	if err != nil {
		return nil, nil, err
	}
	if hostExists && podHostLink != nil {
		return nil, nil, fmt.Errorf("host ownership appears in both host and Pod namespaces")
	}
	if hostExists && podLink == nil {
		// The host-side alias is sufficient deletion authority. Its peer may have disappeared with
		// a prior Pod sandbox or an interrupted creation can have left it without its Pod alias.
		if err = hostHandle.LinkDel(hostLink); err != nil {
			return nil, nil, fmt.Errorf("removing incomplete owned host veth: %w", err)
		}
		hostExists = false
		hostLink = nil
	}
	if !hostExists && (podHostLink != nil || podLink != nil) {
		if podHostLink != nil && podLink != nil && !vethsArePeers(podHostLink, podLink) {
			return nil, nil, fmt.Errorf("Pod-local partial host endpoint objects are not peers")
		}
		ownedLink := podHostLink
		if ownedLink == nil {
			ownedLink = podLink
		}
		if err = podHandle.LinkDel(ownedLink); err != nil {
			return nil, nil, fmt.Errorf("removing incomplete Pod-local host endpoint: %w", err)
		}
		podLink = nil
	}
	if !hostExists {
		if err = removeUnmarkedCreationPair(podHandle, intent); err != nil {
			return nil, nil, err
		}
		if _, exists, lookupErr := linkByName(podHandle, intent.podName); lookupErr != nil {
			return nil, nil, lookupErr
		} else if exists {
			return nil, nil, fmt.Errorf(
				"Pod interface %q collides with unrelated state",
				intent.podName,
			)
		}
		hostLink, podLink, err = createVethPair(hostHandle, podHandle, intent)
		if err != nil {
			return nil, nil, err
		}
	} else if podLink.Attrs().Name != intent.podName {
		if _, exists, lookupErr := linkByName(podHandle, intent.podName); lookupErr != nil {
			return nil, nil, lookupErr
		} else if exists {
			return nil, nil, fmt.Errorf(
				"Pod interface %q collides with unrelated state",
				intent.podName,
			)
		}
		if err = podHandle.LinkSetName(podLink, intent.podName); err != nil {
			return nil, nil, fmt.Errorf("renaming owned Pod veth: %w", err)
		}
		podLink, _, err = linkByName(podHandle, intent.podName)
		if err != nil {
			return nil, nil, err
		}
	}
	if !vethsArePeers(hostLink, podLink) {
		return nil, nil, fmt.Errorf("owned host and Pod interfaces are not veth peers")
	}
	for name, item := range map[string]struct {
		handle *netlink.Handle
		link   netlink.Link
	}{
		"host": {handle: hostHandle, link: hostLink},
		"Pod":  {handle: podHandle, link: podLink},
	} {
		if intent.mtu != 0 && item.link.Attrs().MTU != intent.mtu {
			if err = item.handle.LinkSetMTU(item.link, intent.mtu); err != nil {
				return nil, nil, fmt.Errorf("setting %s veth MTU: %w", name, err)
			}
		}
		if item.link.Attrs().Flags&net.FlagUp == 0 {
			if err = item.handle.LinkSetUp(item.link); err != nil {
				return nil, nil, fmt.Errorf("bringing %s veth up: %w", name, err)
			}
		}
	}

	return hostLink, podLink, nil
}

func (linuxOperations) Delete(ctx context.Context, endpoint OwnedEndpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validInterfaceName(endpoint.HostInterface) {
		return fmt.Errorf("owned host interface name is invalid")
	}
	alias, err := ownerAlias(ownerRoleHost, endpoint.Ownership)
	if err != nil {
		return err
	}
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("opening host netlink handle: %w", err)
	}
	defer handle.Close()
	link, exists, err := linkByName(handle, endpoint.HostInterface)
	if err != nil || !exists {
		return err
	}
	if link.Type() != hostEndpointLinkType || link.Attrs().Alias != alias {
		return fmt.Errorf(
			"host interface %q is not the requested owned veth",
			endpoint.HostInterface,
		)
	}
	if err = handle.LinkDel(link); err != nil {
		return fmt.Errorf("deleting owned host veth %q: %w", endpoint.HostInterface, err)
	}

	return nil
}

//nolint:gocyclo // Each failure point cleans up according to the namespace reached.
func createVethPair(
	hostHandle *netlink.Handle,
	podHandle *netlink.Handle,
	intent vethPairIntent,
) (netlink.Link, netlink.Link, error) {
	hostNamespace, err := netns.Get()
	if err != nil {
		return nil, nil, fmt.Errorf("opening host network namespace: %w", err)
	}
	defer func() { _ = hostNamespace.Close() }()
	temporaryName := temporaryPodHostName(intent)
	if _, exists, lookupErr := linkByName(podHandle, temporaryName); lookupErr != nil {
		return nil, nil, lookupErr
	} else if exists {
		return nil, nil, fmt.Errorf("temporary Pod interface %q collides", temporaryName)
	}
	attributes := netlink.NewLinkAttrs()
	attributes.Name = temporaryName
	if intent.mtu != 0 {
		attributes.MTU = intent.mtu
	}
	veth := netlink.NewVeth(attributes)
	veth.PeerName = intent.podName
	if intent.mtu != 0 {
		if intent.mtu > math.MaxUint32 {
			return nil, nil, fmt.Errorf("veth MTU is outside the supported range")
		}
		veth.PeerMTU = uint32(intent.mtu) //nolint:gosec // Range checked above.
	}
	if err = podHandle.LinkAdd(veth); err != nil {
		return nil, nil, fmt.Errorf("creating host-to-Pod veth pair: %w", err)
	}
	podHostLink, hostExists, err := linkByName(podHandle, temporaryName)
	if err != nil || !hostExists {
		return nil, nil, createdLinkLookupError("Pod-local host", err)
	}
	podLink, podExists, err := linkByName(podHandle, intent.podName)
	if err != nil || !podExists {
		return nil, nil, cleanupCreatedPodVeth(
			podHandle,
			podHostLink,
			createdLinkLookupError("Pod", err),
		)
	}
	if err = podHandle.LinkSetAlias(podHostLink, intent.hostAlias); err != nil {
		return nil, nil, cleanupCreatedPodVeth(podHandle, podHostLink, err)
	}
	if err = podHandle.LinkSetAlias(podLink, intent.podAlias); err != nil {
		return nil, nil, cleanupCreatedPodVeth(podHandle, podHostLink, err)
	}
	if err = podHandle.LinkSetNsFd(podHostLink, int(hostNamespace)); err != nil {
		return nil, nil, cleanupCreatedPodVeth(podHandle, podHostLink, err)
	}
	hostLink, hostExists, err := linkByName(hostHandle, temporaryName)
	if err != nil || !hostExists {
		return nil, nil, createdLinkLookupError("moved host", err)
	}
	if err = hostHandle.LinkSetName(hostLink, intent.hostName); err != nil {
		return nil, nil, cleanupCreatedHostVeth(hostHandle, hostLink, intent.hostAlias, err)
	}
	hostLink, hostExists, err = linkByName(hostHandle, intent.hostName)
	if err != nil || !hostExists {
		return nil, nil, cleanupCreatedHostVeth(
			hostHandle,
			hostLink,
			intent.hostAlias,
			createdLinkLookupError("renamed host", err),
		)
	}
	podLink, podExists, err = linkByName(podHandle, intent.podName)
	if err != nil || !podExists {
		return nil, nil, cleanupCreatedHostVeth(
			hostHandle,
			hostLink,
			intent.hostAlias,
			createdLinkLookupError("marked Pod", err),
		)
	}

	return hostLink, podLink, nil
}

func removeUnmarkedCreationPair(
	podHandle *netlink.Handle,
	intent vethPairIntent,
) error {
	temporaryName := temporaryPodHostName(intent)
	temporaryLink, exists, err := linkByName(podHandle, temporaryName)
	if err != nil || !exists {
		return err
	}
	podLink, podExists, err := linkByName(podHandle, intent.podName)
	if err != nil {
		return err
	}
	if temporaryLink.Type() != hostEndpointLinkType || !podExists ||
		podLink.Type() != hostEndpointLinkType || !vethsArePeers(temporaryLink, podLink) ||
		(temporaryLink.Attrs().Alias != "" && temporaryLink.Attrs().Alias != intent.hostAlias) ||
		(podLink.Attrs().Alias != "" && podLink.Attrs().Alias != intent.podAlias) {
		return fmt.Errorf("temporary Pod interface %q collides with unrelated state", temporaryName)
	}
	if err = podHandle.LinkDel(temporaryLink); err != nil {
		return fmt.Errorf("removing interrupted Pod-local veth creation: %w", err)
	}

	return nil
}

func temporaryPodHostName(intent vethPairIntent) string {
	for counter := byte(0); ; counter++ {
		digest := sha256.Sum256(append([]byte(intent.hostAlias), counter))
		name := fmt.Sprintf("c9h%x", digest[:5])
		if name != intent.podName {
			return name
		}
	}
}

func createdLinkLookupError(role string, err error) error {
	if err == nil {
		err = fmt.Errorf("created interface is unavailable")
	}

	return fmt.Errorf("reading created %s veth: %w", role, err)
}

func cleanupCreatedPodVeth(
	handle *netlink.Handle,
	link netlink.Link,
	cause error,
) error {
	if link == nil || link.Type() != hostEndpointLinkType {
		return cause
	}

	return errors.Join(cause, handle.LinkDel(link))
}

func cleanupCreatedHostVeth(
	handle *netlink.Handle,
	link netlink.Link,
	alias string,
	cause error,
) error {
	if link == nil || link.Type() != hostEndpointLinkType || link.Attrs().Alias != alias {
		return cause
	}

	return errors.Join(cause, handle.LinkDel(link))
}

func ownedPodLink(handle *netlink.Handle, alias string) (netlink.Link, error) {
	links, err := handle.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing Pod interfaces: %w", err)
	}
	var result netlink.Link
	for _, link := range links {
		if link.Attrs().Alias != alias {
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("multiple Pod interfaces carry one ownership identity")
		}
		if link.Type() != hostEndpointLinkType {
			return nil, fmt.Errorf("owned Pod interface %q is not a veth", link.Attrs().Name)
		}
		result = link
	}

	return result, nil
}

func vethsArePeers(host, pod netlink.Link) bool {
	if host == nil || pod == nil || host.Type() != hostEndpointLinkType ||
		pod.Type() != hostEndpointLinkType {
		return false
	}
	hostPeer := host.Attrs().ParentIndex
	podPeer := pod.Attrs().ParentIndex
	if hostPeer == 0 && podPeer == 0 {
		return false
	}

	return (hostPeer == 0 || hostPeer == pod.Attrs().Index) &&
		(podPeer == 0 || podPeer == host.Attrs().Index)
}

func linkByName(handle *netlink.Handle, name string) (netlink.Link, bool, error) {
	link, err := handle.LinkByName(name)
	if err == nil {
		return link, true, nil
	}
	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		return nil, false, nil
	}

	return nil, false, fmt.Errorf("looking up interface %q: %w", name, err)
}

const (
	fabricVTEPLinkType = "vxlan"
	// fabricFilterPriority is the fixed tc filter priority used for fabric redirects.
	fabricFilterPriority = 42
)

// ListFabric inventories every c9s-owned fabric object in the host network namespace.
func (linuxOperations) ListFabric(ctx context.Context) ([]OwnedFabricObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("opening host netlink handle: %w", err)
	}
	defer handle.Close()

	return listFabricObjects(handle)
}

func listFabricObjects(handle *netlink.Handle) ([]OwnedFabricObject, error) {
	links, err := handle.LinkList()
	if err != nil {
		return nil, fmt.Errorf("listing host interfaces: %w", err)
	}
	result := []OwnedFabricObject{}
	for _, link := range links {
		for _, role := range []string{fabricRoleLeg, fabricRoleVTEP} {
			ownership, owned := parseFabricOwnerAlias(link.Attrs().Alias, role)
			if !owned {
				continue
			}
			expectedType := hostEndpointLinkType
			if role == fabricRoleVTEP {
				expectedType = fabricVTEPLinkType
			}
			if link.Type() != expectedType {
				return nil, fmt.Errorf(
					"owned fabric interface %q is not a %s",
					link.Attrs().Name,
					expectedType,
				)
			}
			result = append(result, OwnedFabricObject{
				Name:      link.Attrs().Name,
				Role:      role,
				Ownership: ownership,
			})
		}
	}
	slices.SortFunc(result, func(left, right OwnedFabricObject) int {
		return strings.Compare(left.Name, right.Name)
	})

	return result, nil
}

// EnsureFabric realizes one fabric endpoint: the Pod-side veth leg always, and the host-side
// transport (a local patch to the peer's leg or a VTEP toward the peer's worker) when the peer
// placement allows it. The returned status reports transport readiness without failing the
// request for an absent peer.
func (linuxOperations) EnsureFabric(
	ctx context.Context,
	endpoint FabricEndpoint,
	pod ObjectIdentity,
	nodeAddress string,
	namespaceFD int,
) (FabricStatus, error) {
	status := FabricStatus{LinkUID: endpoint.Link.UID}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	if namespaceFD < 0 {
		return status, fmt.Errorf("Pod network-namespace handle is invalid")
	}
	ownership := fabricOwnershipFor(endpoint, pod)
	legAlias, err := fabricOwnerAlias(fabricRoleLeg, ownership)
	if err != nil {
		return status, err
	}
	podAlias, err := fabricOwnerAlias(fabricRolePod, ownership)
	if err != nil {
		return status, err
	}
	hostHandle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return status, fmt.Errorf("opening host netlink handle: %w", err)
	}
	defer hostHandle.Close()
	podHandle, err := netlink.NewHandleAt(netns.NsHandle(namespaceFD), unix.NETLINK_ROUTE)
	if err != nil {
		return status, fmt.Errorf("opening Pod netlink handle: %w", err)
	}
	defer podHandle.Close()
	legLink, _, err := ensureOwnedVethPair(hostHandle, podHandle, vethPairIntent{
		hostName:  fabricLegName(endpoint.Link.UID, endpoint.Node.UID),
		podName:   endpoint.PodInterface,
		mtu:       endpoint.MTU,
		hostAlias: legAlias,
		podAlias:  podAlias,
	})
	if err != nil {
		return status, err
	}

	return reconcileFabricTransport(hostHandle, endpoint, ownership, legLink, nodeAddress)
}

// ReconcileFabricTransports re-realizes the host-side transports for every desired fabric
// endpoint whose leg already exists. It requires no Pod namespace handle, so the periodic sweep
// converges peer moves between helper requests.
func (linuxOperations) ReconcileFabricTransports(
	ctx context.Context,
	endpoints []FabricEndpoint,
	nodeAddress string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("opening host netlink handle: %w", err)
	}
	defer handle.Close()
	var errs []error
	for _, endpoint := range endpoints {
		pod := endpointFabricPod(endpoint)
		if validateObjectIdentity(pod) != nil {
			continue
		}
		ownership := fabricOwnershipFor(endpoint, pod)
		legAlias, aliasErr := fabricOwnerAlias(fabricRoleLeg, ownership)
		if aliasErr != nil {
			errs = append(errs, aliasErr)

			continue
		}
		legName := fabricLegName(endpoint.Link.UID, endpoint.Node.UID)
		legLink, exists, lookupErr := linkByName(handle, legName)
		if lookupErr != nil {
			errs = append(errs, lookupErr)

			continue
		}
		if !exists || legLink.Attrs().Alias != legAlias {
			continue
		}
		if _, transportErr := reconcileFabricTransport(
			handle,
			endpoint,
			ownership,
			legLink,
			nodeAddress,
		); transportErr != nil {
			errs = append(errs, transportErr)
		}
	}

	return errors.Join(errs...)
}

// DeleteFabric removes one owned fabric object after re-verifying its ownership alias.
func (linuxOperations) DeleteFabric(ctx context.Context, object OwnedFabricObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validInterfaceName(object.Name) {
		return fmt.Errorf("owned fabric interface name is invalid")
	}
	alias, err := fabricOwnerAlias(object.Role, object.Ownership)
	if err != nil {
		return err
	}
	expectedType := hostEndpointLinkType
	if object.Role == fabricRoleVTEP {
		expectedType = fabricVTEPLinkType
	}
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("opening host netlink handle: %w", err)
	}
	defer handle.Close()
	link, exists, err := linkByName(handle, object.Name)
	if err != nil || !exists {
		return err
	}
	if link.Type() != expectedType || link.Attrs().Alias != alias {
		return fmt.Errorf("host interface %q is not the requested owned fabric object", object.Name)
	}
	if err = handle.LinkDel(link); err != nil {
		return fmt.Errorf("deleting owned fabric interface %q: %w", object.Name, err)
	}

	return nil
}

//nolint:funlen,gocyclo // Placement branches share leg state but realize distinct transports.
func reconcileFabricTransport(
	handle *netlink.Handle,
	endpoint FabricEndpoint,
	ownership Ownership,
	legLink netlink.Link,
	nodeAddress string,
) (FabricStatus, error) {
	status := FabricStatus{LinkUID: endpoint.Link.UID}
	peer := endpointFabricPeer(endpoint)
	vtepName := fabricVTEPName(endpoint.Link.UID, endpoint.Node.UID)
	vtepAlias, err := fabricOwnerAlias(fabricRoleVTEP, ownership)
	if err != nil {
		return status, err
	}
	removeVTEP := func() error {
		link, exists, lookupErr := linkByName(handle, vtepName)
		if lookupErr != nil || !exists {
			return lookupErr
		}
		if link.Type() != fabricVTEPLinkType || link.Attrs().Alias != vtepAlias {
			return fmt.Errorf("VTEP name %q collides with unrelated state", vtepName)
		}

		return handle.LinkDel(link)
	}
	switch {
	case !peer.present:
		if err = removeVTEP(); err != nil {
			return status, err
		}
		status.Reason = "peer Pod is absent"

		return status, nil
	case peer.sameNode:
		if err = removeVTEP(); err != nil {
			return status, err
		}
		peerLegAlias, aliasErr := fabricOwnerAlias(fabricRoleLeg, peer.ownership)
		if aliasErr != nil {
			return status, aliasErr
		}
		peerLegName := fabricLegName(peer.ownership.LinkUID, peer.ownership.NodeUID)
		peerLeg, exists, lookupErr := linkByName(handle, peerLegName)
		if lookupErr != nil {
			return status, lookupErr
		}
		if !exists || peerLeg.Type() != hostEndpointLinkType ||
			peerLeg.Attrs().Alias != peerLegAlias {
			status.Reason = "peer leg is not yet realized"

			return status, nil
		}
		if err = ensureFabricRedirect(handle, legLink, peerLeg); err != nil {
			return status, err
		}
		if err = ensureFabricRedirect(handle, peerLeg, legLink); err != nil {
			return status, err
		}
		status.Ready = true

		return status, nil
	default:
		if peer.nodeAddress == "" {
			status.Reason = "peer worker address is not yet known"

			return status, nil
		}
		if nodeAddress == "" {
			return status, fmt.Errorf("fabric VTEP requires this worker's address")
		}
		localIP := net.ParseIP(nodeAddress)
		remoteIP := net.ParseIP(peer.nodeAddress)
		if localIP == nil || remoteIP == nil {
			return status, fmt.Errorf("fabric VTEP addresses are invalid")
		}
		vtepLink, err := ensureFabricVTEP(
			handle,
			vtepName,
			vtepAlias,
			endpoint.TunnelID,
			endpoint.MTU,
			localIP,
			remoteIP,
		)
		if err != nil {
			return status, err
		}
		if err = ensureFabricRedirect(handle, legLink, vtepLink); err != nil {
			return status, err
		}
		if err = ensureFabricRedirect(handle, vtepLink, legLink); err != nil {
			return status, err
		}
		status.Ready = true

		return status, nil
	}
}

//nolint:gocyclo // Conformance checks precede exactly one delete-and-recreate path.
func ensureFabricVTEP(
	handle *netlink.Handle,
	name, alias string,
	tunnelID, mtu int,
	localIP, remoteIP net.IP,
) (netlink.Link, error) {
	existing, exists, err := linkByName(handle, name)
	if err != nil {
		return nil, err
	}
	if exists {
		if existing.Type() != fabricVTEPLinkType || existing.Attrs().Alias != alias {
			return nil, fmt.Errorf("VTEP name %q collides with unrelated state", name)
		}
		vxlan, isVXLAN := existing.(*netlink.Vxlan)
		conforms := isVXLAN && vxlan.VxlanId == tunnelID &&
			vxlan.SrcAddr.Equal(localIP) && vxlan.Group.Equal(remoteIP) &&
			vxlan.Port == FabricTunnelPort && !vxlan.Learning &&
			(mtu == 0 || vxlan.Attrs().MTU == mtu)
		if conforms {
			if existing.Attrs().Flags&net.FlagUp == 0 {
				if err = handle.LinkSetUp(existing); err != nil {
					return nil, fmt.Errorf("bringing VTEP up: %w", err)
				}
			}

			return existing, nil
		}
		if err = handle.LinkDel(existing); err != nil {
			return nil, fmt.Errorf("replacing stale VTEP %q: %w", name, err)
		}
	}
	attributes := netlink.NewLinkAttrs()
	attributes.Name = name
	if mtu != 0 {
		attributes.MTU = mtu
	}
	vtep := &netlink.Vxlan{
		LinkAttrs: attributes,
		VxlanId:   tunnelID,
		SrcAddr:   localIP,
		Group:     remoteIP,
		Port:      FabricTunnelPort,
		Learning:  false,
	}
	if err = handle.LinkAdd(vtep); err != nil {
		return nil, fmt.Errorf("creating fabric VTEP %q: %w", name, err)
	}
	created, exists, err := linkByName(handle, name)
	if err != nil || !exists {
		return nil, createdLinkLookupError("fabric VTEP", err)
	}
	if err = handle.LinkSetAlias(created, alias); err != nil {
		return nil, errors.Join(
			fmt.Errorf("marking fabric VTEP ownership: %w", err),
			handle.LinkDel(created),
		)
	}
	if err = handle.LinkSetUp(created); err != nil {
		return nil, fmt.Errorf("bringing VTEP up: %w", err)
	}

	return created, nil
}

// ensureFabricRedirect wires one direction of a fabric patch: every frame received on from is
// redirected to the egress of to, giving point-to-point wire semantics without bridge state.
func ensureFabricRedirect(handle *netlink.Handle, from, to netlink.Link) error {
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: from.Attrs().Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}
	if err := handle.QdiscReplace(qdisc); err != nil {
		return fmt.Errorf("installing fabric redirect qdisc on %q: %w", from.Attrs().Name, err)
	}
	// cls_matchall does not implement in-place change, so idempotency is explicit: keep an
	// existing filter that already redirects to the right target, delete one that does not,
	// and only then add.
	existing, err := handle.FilterList(from, netlink.HANDLE_MIN_INGRESS)
	if err != nil {
		return fmt.Errorf("listing fabric redirect filters on %q: %w", from.Attrs().Name, err)
	}
	for _, candidate := range existing {
		if candidate.Attrs().Priority != fabricFilterPriority {
			continue
		}
		matchAll, isMatchAll := candidate.(*netlink.MatchAll)
		if isMatchAll && len(matchAll.Actions) == 1 {
			if mirred, isMirred := matchAll.Actions[0].(*netlink.MirredAction); isMirred &&
				mirred.Ifindex == to.Attrs().Index {
				return nil
			}
		}
		if err = handle.FilterDel(candidate); err != nil {
			return fmt.Errorf(
				"removing stale fabric redirect filter on %q: %w",
				from.Attrs().Name,
				err,
			)
		}
	}
	filter := &netlink.MatchAll{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: from.Attrs().Index,
			Parent:    netlink.HANDLE_MIN_INGRESS,
			Priority:  fabricFilterPriority,
			Protocol:  unix.ETH_P_ALL,
		},
		Actions: []netlink.Action{netlink.NewMirredAction(to.Attrs().Index)},
	}
	if err = handle.FilterAdd(filter); err != nil {
		return fmt.Errorf("installing fabric redirect filter on %q: %w", from.Attrs().Name, err)
	}

	return nil
}
