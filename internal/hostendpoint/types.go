//nolint:err113,gocritic,noinlineerr,perfsprint,wsl_v5 // RPC validation uses compact guards.
package hostendpoint

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

const (
	// SchemaVersion is the only accepted node-local host-endpoint RPC schema.
	SchemaVersion = "c9s.host-endpoint/v1alpha1"
	// SocketDirectory is the fixed hostPath shared only with c9s connectivity helpers.
	SocketDirectory = "/var/run/clabernetes/host-endpoint"
	// DefaultSocketPath is shared only between the fixed connectivity helper and the node-local
	// daemon through a narrowly scoped hostPath directory.
	DefaultSocketPath = SocketDirectory + "/daemon.sock"
	ownerPrefix       = "c9s:host:v1:"
	ownerRoleHost     = "host"
	ownerRolePod      = "pod"
	maximumMessage    = 64 << 10
)

// Link annotations identify the one node and Pod for which a host endpoint was accepted before
// any host object is created. The annotation makes finalizer ownership recoverable after crashes.
const (
	AppliedNodeAnnotation   = "c9s.run/host-endpoint-node"
	AppliedPodUIDAnnotation = "c9s.run/host-endpoint-pod-uid"
)

// ObjectIdentity is one immutable namespaced Kubernetes identity.
type ObjectIdentity struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// Endpoint is one desired host-to-Pod veth pair. It contains no kind or vendor information.
type Endpoint struct {
	Link          ObjectIdentity `json:"link"`
	Node          ObjectIdentity `json:"node"`
	HostInterface string         `json:"hostInterface"`
	PodInterface  string         `json:"podInterface"`
	MTU           int            `json:"mtu,omitempty"`
	pod           ObjectIdentity
}

// ReconcileRequest replaces all host endpoints owned by one immutable Pod identity.
type ReconcileRequest struct {
	SchemaVersion string         `json:"schemaVersion"`
	Pod           ObjectIdentity `json:"pod"`
	Endpoints     []Endpoint     `json:"endpoints,omitempty"`
}

// Response is the bounded one-request/one-response wire result.
type Response struct {
	Error string `json:"error,omitempty"`
}

// Ownership is the immutable metadata carried by each c9s-owned host object.
type Ownership struct {
	LinkUID string
	NodeUID string
	PodUID  string
}

// OwnedEndpoint is the host-namespace inventory exposed to reconciliation and sweeping.
type OwnedEndpoint struct {
	HostInterface string
	Ownership     Ownership
}

//nolint:gocyclo // Each branch validates a distinct immutable RPC boundary.
func normalizeRequest(request ReconcileRequest) (ReconcileRequest, error) {
	if request.SchemaVersion != SchemaVersion {
		return ReconcileRequest{}, fmt.Errorf("host-endpoint RPC schema is unsupported")
	}
	if err := validateObjectIdentity(request.Pod); err != nil {
		return ReconcileRequest{}, fmt.Errorf("Pod identity is invalid: %w", err)
	}
	request.Endpoints = slices.Clone(request.Endpoints)
	slices.SortFunc(request.Endpoints, func(left, right Endpoint) int {
		if compared := strings.Compare(left.Link.UID, right.Link.UID); compared != 0 {
			return compared
		}

		return strings.Compare(left.HostInterface, right.HostInterface)
	})
	seenLinks := map[string]bool{}
	seenHostInterfaces := map[string]bool{}
	seenPodInterfaces := map[string]bool{}
	for index := range request.Endpoints {
		endpoint := &request.Endpoints[index]
		if err := validateObjectIdentity(endpoint.Link); err != nil ||
			endpoint.Link.Namespace != request.Pod.Namespace {
			return ReconcileRequest{}, fmt.Errorf("endpoint Link identity is invalid")
		}
		if err := validateObjectIdentity(endpoint.Node); err != nil ||
			endpoint.Node.Namespace != request.Pod.Namespace {
			return ReconcileRequest{}, fmt.Errorf("endpoint Node identity is invalid")
		}
		if !validInterfaceName(endpoint.HostInterface) ||
			!validInterfaceName(endpoint.PodInterface) || endpoint.MTU < 0 ||
			uint64(endpoint.MTU) > math.MaxUint32 {
			return ReconcileRequest{}, fmt.Errorf("endpoint interface intent is invalid")
		}
		if seenLinks[endpoint.Link.UID] || seenHostInterfaces[endpoint.HostInterface] ||
			seenPodInterfaces[endpoint.PodInterface] {
			return ReconcileRequest{}, fmt.Errorf("endpoint identities are not unique")
		}
		seenLinks[endpoint.Link.UID] = true
		seenHostInterfaces[endpoint.HostInterface] = true
		seenPodInterfaces[endpoint.PodInterface] = true
	}

	return request, nil
}

func validateObjectIdentity(identity ObjectIdentity) error {
	if identity.Namespace == "" || identity.Name == "" || !validUID(identity.UID) {
		return fmt.Errorf("identity is incomplete")
	}

	return nil
}

func validUID(value string) bool {
	return value != "" && len(value) <= 64 && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, ":/\x00\n\r\t ")
}

func validInterfaceName(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= 15 &&
		!strings.ContainsAny(value, ":/\x00\n\r\t ")
}

func ownershipFor(endpoint Endpoint, pod ObjectIdentity) Ownership {
	return Ownership{
		LinkUID: endpoint.Link.UID,
		NodeUID: endpoint.Node.UID,
		PodUID:  pod.UID,
	}
}

func ownerAlias(role string, ownership Ownership) (string, error) {
	if role != ownerRoleHost && role != ownerRolePod || !validUID(ownership.LinkUID) ||
		!validUID(ownership.NodeUID) || !validUID(ownership.PodUID) {
		return "", fmt.Errorf("host-endpoint ownership is invalid")
	}

	return ownerPrefix + role + ":" + ownership.LinkUID + ":" + ownership.NodeUID + ":" +
		ownership.PodUID, nil
}

func parseOwnerAlias(value, role string) (Ownership, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 7 || parts[0] != "c9s" || parts[1] != "host" || parts[2] != "v1" ||
		parts[3] != role {
		return Ownership{}, false
	}
	ownership := Ownership{LinkUID: parts[4], NodeUID: parts[5], PodUID: parts[6]}
	if !validUID(ownership.LinkUID) || !validUID(ownership.NodeUID) ||
		!validUID(ownership.PodUID) {
		return Ownership{}, false
	}

	return ownership, true
}
