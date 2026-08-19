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

//nolint:funlen,gocognit,gocyclo,nestif // One transaction guards each cross-namespace mutation.
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

	hostLink, hostExists, err := linkByName(hostHandle, endpoint.HostInterface)
	if err != nil {
		return err
	}
	if hostExists &&
		(hostLink.Type() != hostEndpointLinkType || hostLink.Attrs().Alias != hostAlias) {
		return fmt.Errorf("host interface %q collides with unrelated state", endpoint.HostInterface)
	}
	podLink, err := ownedPodLink(podHandle, podAlias)
	if err != nil {
		return err
	}
	podHostLink, err := ownedPodLink(podHandle, hostAlias)
	if err != nil {
		return err
	}
	if hostExists && podHostLink != nil {
		return fmt.Errorf("host ownership appears in both host and Pod namespaces")
	}
	if hostExists && podLink == nil {
		// The host-side alias is sufficient deletion authority. Its peer may have disappeared with
		// a prior Pod sandbox or an interrupted creation can have left it without its Pod alias.
		if err = hostHandle.LinkDel(hostLink); err != nil {
			return fmt.Errorf("removing incomplete owned host veth: %w", err)
		}
		hostExists = false
		hostLink = nil
	}
	if !hostExists && (podHostLink != nil || podLink != nil) {
		if podHostLink != nil && podLink != nil && !vethsArePeers(podHostLink, podLink) {
			return fmt.Errorf("Pod-local partial host endpoint objects are not peers")
		}
		ownedLink := podHostLink
		if ownedLink == nil {
			ownedLink = podLink
		}
		if err = podHandle.LinkDel(ownedLink); err != nil {
			return fmt.Errorf("removing incomplete Pod-local host endpoint: %w", err)
		}
		podLink = nil
	}
	if !hostExists {
		if err = removeUnmarkedCreationPair(podHandle, endpoint, hostAlias, podAlias); err != nil {
			return err
		}
		if _, exists, lookupErr := linkByName(podHandle, endpoint.PodInterface); lookupErr != nil {
			return lookupErr
		} else if exists {
			return fmt.Errorf(
				"Pod interface %q collides with unrelated state",
				endpoint.PodInterface,
			)
		}
		hostLink, podLink, err = createVethPair(
			hostHandle,
			podHandle,
			endpoint,
			hostAlias,
			podAlias,
		)
		if err != nil {
			return err
		}
	} else if podLink.Attrs().Name != endpoint.PodInterface {
		if _, exists, lookupErr := linkByName(podHandle, endpoint.PodInterface); lookupErr != nil {
			return lookupErr
		} else if exists {
			return fmt.Errorf(
				"Pod interface %q collides with unrelated state",
				endpoint.PodInterface,
			)
		}
		if err = podHandle.LinkSetName(podLink, endpoint.PodInterface); err != nil {
			return fmt.Errorf("renaming owned Pod veth: %w", err)
		}
		podLink, _, err = linkByName(podHandle, endpoint.PodInterface)
		if err != nil {
			return err
		}
	}
	if !vethsArePeers(hostLink, podLink) {
		return fmt.Errorf("owned host and Pod interfaces are not veth peers")
	}
	for name, item := range map[string]struct {
		handle *netlink.Handle
		link   netlink.Link
	}{
		"host": {handle: hostHandle, link: hostLink},
		"Pod":  {handle: podHandle, link: podLink},
	} {
		if endpoint.MTU != 0 && item.link.Attrs().MTU != endpoint.MTU {
			if err = item.handle.LinkSetMTU(item.link, endpoint.MTU); err != nil {
				return fmt.Errorf("setting %s veth MTU: %w", name, err)
			}
		}
		if item.link.Attrs().Flags&net.FlagUp == 0 {
			if err = item.handle.LinkSetUp(item.link); err != nil {
				return fmt.Errorf("bringing %s veth up: %w", name, err)
			}
		}
	}

	return nil
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
	endpoint Endpoint,
	hostAlias,
	podAlias string,
) (netlink.Link, netlink.Link, error) {
	hostNamespace, err := netns.Get()
	if err != nil {
		return nil, nil, fmt.Errorf("opening host network namespace: %w", err)
	}
	defer func() { _ = hostNamespace.Close() }()
	temporaryName := temporaryPodHostName(endpoint, hostAlias)
	if _, exists, lookupErr := linkByName(podHandle, temporaryName); lookupErr != nil {
		return nil, nil, lookupErr
	} else if exists {
		return nil, nil, fmt.Errorf("temporary Pod interface %q collides", temporaryName)
	}
	attributes := netlink.NewLinkAttrs()
	attributes.Name = temporaryName
	if endpoint.MTU != 0 {
		attributes.MTU = endpoint.MTU
	}
	veth := netlink.NewVeth(attributes)
	veth.PeerName = endpoint.PodInterface
	if endpoint.MTU != 0 {
		if endpoint.MTU > math.MaxUint32 {
			return nil, nil, fmt.Errorf("veth MTU is outside the supported range")
		}
		veth.PeerMTU = uint32(endpoint.MTU) //nolint:gosec // Range checked above.
	}
	if err = podHandle.LinkAdd(veth); err != nil {
		return nil, nil, fmt.Errorf("creating host-to-Pod veth pair: %w", err)
	}
	podHostLink, hostExists, err := linkByName(podHandle, temporaryName)
	if err != nil || !hostExists {
		return nil, nil, createdLinkLookupError("Pod-local host", err)
	}
	podLink, podExists, err := linkByName(podHandle, endpoint.PodInterface)
	if err != nil || !podExists {
		return nil, nil, cleanupCreatedPodVeth(
			podHandle,
			podHostLink,
			createdLinkLookupError("Pod", err),
		)
	}
	if err = podHandle.LinkSetAlias(podHostLink, hostAlias); err != nil {
		return nil, nil, cleanupCreatedPodVeth(podHandle, podHostLink, err)
	}
	if err = podHandle.LinkSetAlias(podLink, podAlias); err != nil {
		return nil, nil, cleanupCreatedPodVeth(podHandle, podHostLink, err)
	}
	if err = podHandle.LinkSetNsFd(podHostLink, int(hostNamespace)); err != nil {
		return nil, nil, cleanupCreatedPodVeth(podHandle, podHostLink, err)
	}
	hostLink, hostExists, err := linkByName(hostHandle, temporaryName)
	if err != nil || !hostExists {
		return nil, nil, createdLinkLookupError("moved host", err)
	}
	if err = hostHandle.LinkSetName(hostLink, endpoint.HostInterface); err != nil {
		return nil, nil, cleanupCreatedHostVeth(hostHandle, hostLink, hostAlias, err)
	}
	hostLink, hostExists, err = linkByName(hostHandle, endpoint.HostInterface)
	if err != nil || !hostExists {
		return nil, nil, cleanupCreatedHostVeth(
			hostHandle,
			hostLink,
			hostAlias,
			createdLinkLookupError("renamed host", err),
		)
	}
	podLink, podExists, err = linkByName(podHandle, endpoint.PodInterface)
	if err != nil || !podExists {
		return nil, nil, cleanupCreatedHostVeth(
			hostHandle,
			hostLink,
			hostAlias,
			createdLinkLookupError("marked Pod", err),
		)
	}

	return hostLink, podLink, nil
}

func removeUnmarkedCreationPair(
	podHandle *netlink.Handle,
	endpoint Endpoint,
	hostAlias,
	podAlias string,
) error {
	temporaryName := temporaryPodHostName(endpoint, hostAlias)
	temporaryLink, exists, err := linkByName(podHandle, temporaryName)
	if err != nil || !exists {
		return err
	}
	podLink, podExists, err := linkByName(podHandle, endpoint.PodInterface)
	if err != nil {
		return err
	}
	if temporaryLink.Type() != hostEndpointLinkType || !podExists ||
		podLink.Type() != hostEndpointLinkType || !vethsArePeers(temporaryLink, podLink) ||
		(temporaryLink.Attrs().Alias != "" && temporaryLink.Attrs().Alias != hostAlias) ||
		(podLink.Attrs().Alias != "" && podLink.Attrs().Alias != podAlias) {
		return fmt.Errorf("temporary Pod interface %q collides with unrelated state", temporaryName)
	}
	if err = podHandle.LinkDel(temporaryLink); err != nil {
		return fmt.Errorf("removing interrupted Pod-local veth creation: %w", err)
	}

	return nil
}

func temporaryPodHostName(endpoint Endpoint, hostAlias string) string {
	for counter := byte(0); ; counter++ {
		digest := sha256.Sum256(append([]byte(hostAlias), counter))
		name := fmt.Sprintf("c9h%x", digest[:5])
		if name != endpoint.PodInterface {
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
