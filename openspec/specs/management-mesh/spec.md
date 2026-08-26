# management-mesh Specification

## Purpose
Present the management subnet as one shared L2 domain across every interposed Pod of the namespace, matching containerlab's management network semantics, with strictly Pod-local gateway behavior.

## Requirements


### Requirement: The management subnet is one L2 domain across the namespace

The direct runtime SHALL present the management subnet as a single shared broadcast domain
spanning every interposed Pod of the namespace, matching containerlab's management network
semantics. The mesh scope is the namespace: multiple Topologies deployed into one namespace
share one management L2 domain. A device dialing a peer's management address SHALL resolve the
peer device's actual link-layer address and exchange traffic with the peer device itself — not
a translation proxy — for any protocol and port, in both directions. Name-based reachability
(Service DNS) SHALL keep working unchanged alongside address-based reachability.

#### Scenario: Hardcoded peer management address

- **WHEN** a node's configuration references a peer by its management address (telemetry target,
  syslog collector, ping destination) instead of by name
- **THEN** the traffic reaches the peer device's management plane across Pods exactly as it would
  on containerlab's shared management network

#### Scenario: Peer ARP resolves the peer device

- **WHEN** a device issues an ARP request for a peer's management address
- **THEN** the peer device itself answers with its own link-layer address, and subsequent unicast
  traffic flows device-to-device

#### Scenario: Peer Pod is rescheduled

- **WHEN** a peer's Pod is recreated with a different Pod address
- **THEN** the mesh converges to the new peer location on the reconciliation tick without
  restarting unaffected device Pods, and traffic to that peer's management address resumes

#### Scenario: Two Topologies share one namespace

- **WHEN** two independent Topologies are deployed into the same namespace
- **THEN** their interposed Pods join the same management L2 domain and mesh identity, exactly
  as two containerlab topologies attached to one shared management network

### Requirement: Gateway resolution is always Pod-local

Every interposed Pod hosts the management gateway address. Gateway resolution and gateway-directed
traffic SHALL terminate at the local Pod's gateway: a device resolving or addressing the gateway
MUST receive exactly one answer, from its own Pod, and gateway traffic MUST NOT cross the mesh.
The gateway's link-layer identity SHALL be deterministic across the namespace so that any leaked
resolution is indistinguishable from the local answer. Structural containment MUST NOT depend on
optional kernel packet-filtering families; it SHALL hold on any kernel that supports the runtime's
baseline bridge features.

#### Scenario: Device resolves the gateway

- **WHEN** a device ARPs for the management gateway address
- **THEN** it receives exactly one reply, from its own Pod's gateway, and egress traffic follows
  the local translation path

#### Scenario: Kernel without bridge packet filtering

- **WHEN** the cluster kernel provides no bridge-layer packet-filter support
- **THEN** gateway containment still holds and mesh realization reports no degradation

### Requirement: Mesh state is Pod-owned and identity-safe

All mesh state a Pod creates (bridge, tunnel endpoint, peer forwarding entries, sysctl scoping)
SHALL be bounded by the Pod network namespace lifetime and owned analogously to Link transport
state: forced Pod deletion leaves no mesh residue anywhere, reconciliation is idempotent, and
stale peers are removed exactly. The mesh tunnel identifier SHALL derive deterministically from
the namespace within the VXLAN identifier space, so every Pod of a namespace joins the same
mesh without coordination; it carries no relationship to Link wire identifiers, which travel a
different port and plane and cannot reach the mesh by construction. Only management traffic of
the owning namespace SHALL ride the mesh. Local ARP responder scoping SHALL prevent any Pod
interface from answering for management addresses it does not hold.

#### Scenario: Force-deleted Pod

- **WHEN** an interposed Pod is force-deleted
- **THEN** its mesh state vanishes with the Pod network namespace and peers converge to its
  replacement when one appears

#### Scenario: Node removed from the topology

- **WHEN** a Node is deleted while its peers keep running
- **THEN** each remaining Pod removes exactly the departed peer's forwarding state on the
  reconciliation tick

#### Scenario: Tunnel identifier collision is impossible by construction

- **WHEN** fabric wire datagrams and management mesh datagrams cross the same workers
- **THEN** they cannot be confused regardless of identifier values: the wire is userspace UDP
  on its own port, the mesh is kernel VXLAN on its own port, and neither dispatches the
  other's traffic
