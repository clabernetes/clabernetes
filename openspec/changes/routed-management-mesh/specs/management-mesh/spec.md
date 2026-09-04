## MODIFIED Requirements

### Requirement: The management subnet is one L2 domain across the namespace

The direct runtime SHALL present the management subnet to every interposed device as a single
shared subnet spanning every interposed Pod of the namespace, matching containerlab's management
network semantics from the device's point of view: the device keeps its connected subnet route,
resolves peer management addresses with ordinary ARP, and exchanges traffic with the peer device
itself — not a translation proxy — for any protocol and port, in both directions, with source and
destination addresses unchanged end to end. The mesh scope is the namespace: multiple Topologies
deployed into one namespace share one management domain. The runtime SHALL realize this domain
as routed unicast between Pods: no frame is flooded to more than one Pod, no link-layer address is
learned from traffic, and peer-bound traffic is forwarded to exactly the peer's current Pod.
Broadcast semantics are limited to ARP request and reply; other device broadcasts (gratuitous
ARP, LLDP, DHCP) do not cross Pods. Name-based reachability (Service DNS) SHALL keep working
unchanged alongside address-based reachability.

#### Scenario: Hardcoded peer management address

- **WHEN** a node's configuration references a peer by its management address (telemetry target,
  syslog collector, ping destination) instead of by name
- **THEN** the traffic reaches the peer device's management plane across Pods exactly as it would
  on containerlab's shared management network, and the peer observes the originating management
  address as the source

#### Scenario: Peer ARP resolves the peer device

- **WHEN** a device issues an ARP request for a peer's management address
- **THEN** it receives exactly one reply, carrying its own Pod's gateway link-layer identity, and
  subsequent unicast traffic reaches the peer device itself without any further resolution
  across Pods

#### Scenario: Unknown management address

- **WHEN** a device sends traffic to a management-subnet address that no namespace peer holds
- **THEN** the traffic fails locally with host unreachable rather than being flooded or silently
  dropped

#### Scenario: Peer Pod is rescheduled

- **WHEN** a peer's Pod is recreated with a different Pod address
- **THEN** every other Pod converges to the new peer location once the published peer directory
  reaches it, without restarting unaffected device Pods, and traffic to that peer's management
  address resumes

#### Scenario: Two Topologies share one namespace

- **WHEN** two independent Topologies are deployed into the same namespace
- **THEN** their interposed Pods join the same management domain and mesh identity, exactly as
  two containerlab topologies attached to one shared management network

### Requirement: Gateway resolution is always Pod-local

Every interposed Pod hosts the management gateway address. Gateway resolution and gateway-directed
traffic SHALL terminate at the local Pod's gateway: a device resolving or addressing the gateway
MUST receive exactly one answer, from its own Pod, and gateway traffic MUST NOT cross the mesh.
The gateway's link-layer identity SHALL be deterministic across the namespace, and it is also the
identity every proxy ARP answer for a peer carries, so a device sees one stable next hop for
everything beyond its own leg. Containment SHALL be structural: there is no link-layer path from
the mesh to the gateway, so it holds on any kernel that supports the runtime's baseline routing
and tunnel features and does not depend on optional packet-filtering families.

#### Scenario: Device resolves the gateway

- **WHEN** a device ARPs for the management gateway address
- **THEN** it receives exactly one reply, from its own Pod's gateway, and egress traffic follows
  the local translation path

#### Scenario: Kernel without bridge packet filtering

- **WHEN** the cluster kernel provides no bridge-layer packet-filter support
- **THEN** gateway containment still holds and mesh realization reports no degradation

### Requirement: Mesh state is Pod-owned and identity-safe

All mesh state a Pod creates (synthetic pair, tunnel endpoint, per-peer neighbor and forwarding
entries, proxy entries, routes, sysctl scoping) SHALL be bounded by the Pod network namespace
lifetime and owned analogously to Link transport state: forced Pod deletion leaves no mesh
residue anywhere, reconciliation is idempotent, and stale peer entries are removed exactly. Peer
state SHALL be static and exact: one neighbor entry and one forwarding entry per peer, never a
flood entry. The mesh tunnel identifier SHALL derive deterministically from the namespace within
the VXLAN identifier space, so every Pod of a namespace joins the same mesh without coordination;
it carries no relationship to Link wire identifiers, which travel a different port and plane and
cannot reach the mesh by construction. Each Pod's tunnel endpoint link-layer identity SHALL derive
deterministically from the Pod's own management IPv4 address, so every peer computes it without
coordination and the identities are unique within the namespace. Only management traffic of the
owning namespace SHALL ride the mesh. Local ARP responder scoping SHALL prevent any Pod interface
other than the router leg from answering for management addresses it does not hold.

#### Scenario: Force-deleted Pod

- **WHEN** an interposed Pod is force-deleted
- **THEN** its mesh state vanishes with the Pod network namespace and peers converge to its
  replacement when one appears in the published directory

#### Scenario: Node removed from the topology

- **WHEN** a Node is deleted while its peers keep running
- **THEN** each remaining Pod removes exactly the departed peer's neighbor and forwarding state
  once the published directory no longer lists it

#### Scenario: Tunnel identifier collision is impossible by construction

- **WHEN** fabric wire datagrams and management mesh datagrams cross the same workers
- **THEN** they cannot be confused regardless of identifier values: the wire is userspace UDP
  on its own port, the mesh is kernel VXLAN on its own port, and neither dispatches the
  other's traffic

## ADDED Requirements

### Requirement: Peer state is published, not discovered

The controller SHALL publish the namespace peer directory with, for every node that holds a
management identity, its name and aliases, its management addresses, and the address of the Pod
currently realizing it. The directory SHALL be split across a fixed number of shards by a stable
function of the node name, so a change to one node rewrites one shard and the set of objects a
Pod mounts never changes with namespace size. The sidecar SHALL derive all mesh peer state from
the mounted directory: it MUST NOT resolve peers through DNS or any per-Pod query of the API, and
it SHALL converge peer state when the mounted directory changes and on a slow periodic resync.
A directory entry without a Pod address SHALL contribute name resolution only.

#### Scenario: Node joins the namespace

- **WHEN** a Node is created and its Pod obtains an address
- **THEN** exactly one shard of the directory changes, and every running Pod installs that peer's
  neighbor and forwarding state once the shard reaches it, without any Pod restart or DNS query

#### Scenario: Large namespace

- **WHEN** a namespace holds thousands of interposed Pods
- **THEN** each Pod's mesh state is one entry pair per peer, each membership change costs one
  shard write fanned out once per worker node, and no component performs work proportional to
  the Pod count on every reconciliation tick

#### Scenario: Directory shard is absent

- **WHEN** a Pod starts before a directory shard exists
- **THEN** the Pod starts normally with the peers it can read and converges the rest as shards
  appear
