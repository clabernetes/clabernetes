# direct-connectivity Specification

## Purpose
Define direct, restart-safe connectivity for all supported Link flavors across grouped Pods and multiple Kubernetes workers.

## Requirements

### Requirement: Every Link flavor has one Pod-local realization

The runtime SHALL directly realize same-Pod, loopback, host, and cross-Pod Links with the
requested endpoint names and MTU, entirely from within the Pod: cross-Pod transports terminate
inside the Pod network namespace on the sidecar-preserved Kubernetes underlay, and host Links
place one veth end into the worker network namespace through the sidecar's read-only
host-namespace handle. The requested MTU SHALL be realized exactly on every endpoint interface,
defaulting to containerlab's default link MTU when unset; the Pod underlay MTU MUST NOT bound
any Link's MTU, and MTU realization MUST NOT depend on which Pods or workers the endpoints land
on. The device receives a plain interface leg and never owns the transport underlay, so a kind
that adopts its presented interfaces cannot disturb any transport. The realization is derived
from endpoint shape alone; Links carry no connectivity selector. No Link flavor MAY use a
nested network-device container and no Link flavor MAY require a node-resident agent.

#### Scenario: Realize each supported flavor

- **WHEN** a valid Link resolves to the cross-Pod, same-Pod, loopback, or host flavor
- **THEN** the declared interfaces and dataplane are realized in the endpoint namespaces with
  the requested MTU by the Pod's own connectivity sidecar

#### Scenario: Requested MTU exceeds the underlay MTU

- **WHEN** a cross-Pod Link requests an MTU larger than the Pod underlay carries
- **THEN** the device leg and sidecar leg realize the requested MTU exactly and MTU-sized
  frames cross the Link with zero configuration, on any cluster

### Requirement: Cross-Pod Links use one loss-preserving wire with carrier propagation

Cross-Pod Link frames SHALL cross the Pod network through one sidecar-owned datagram wire that
segments each frame to the locally observed underlay MTU and reassembles it at the peer
sidecar; segment sizing SHALL be a purely local decision so mixed-MTU worker sets need no
coordination. The wire SHALL enforce a minimum segment size so an unidentifiable or implausibly
small underlay still yields a functional wire; on an underlay smaller than that minimum plus
the wire's transport overhead, the wire SHALL keep functioning through outer IP-layer
fragmentation rather than failing. A frame missing any segment SHALL be dropped whole within a
bounded reassembly window and MUST NOT be retransmitted or acknowledged, so the loss devices
observe on an emulated Link reflects loss on the path. Reassembly state MUST be memory-bounded
per Link.

The wire SHALL forward frames transparently regardless of source and destination MAC,
EtherType, and VLAN tagging: single-tagged frames SHALL cross at the full Link MTU, and
double-tagged (QinQ) frames SHALL share the endpoint legs' single-tag headroom budget above
the Link MTU, matching common physical NIC behavior.

Endpoint carrier state and peer liveness SHALL share the wire's socket and path with the data
they describe. Each sidecar SHALL advertise its local endpoint state on change and
periodically, and SHALL prove liveness with periodic heartbeats. A receiver SHALL realize a
peer-down or peer-lost condition as loss of carrier on the local device leg by holding its own
sidecar-owned leg administratively down; it MUST NOT touch the device leg's administrative
state, and it MUST NOT re-advertise a wire-imposed down as local endpoint state. Datagrams from
a superseded peer process generation MUST be rejected.

#### Scenario: Peer endpoint goes down gracefully

- **WHEN** one endpoint's device-facing interface goes operationally down
- **THEN** the peer's device leg shows loss of carrier within 500 ms while remaining
  administratively up, and carrier restores the same way when the endpoint returns

#### Scenario: Peer Pod dies

- **WHEN** one endpoint Pod dies without any shutdown signal
- **THEN** every Link terminating on it shows loss of carrier at its peers within the
  heartbeat timeout, and carrier restores without manual action once a replacement Pod
  converges

#### Scenario: Underlay loses one fragment of a jumbo frame

- **WHEN** the Pod network drops one segment of a frame the wire fragmented
- **THEN** the whole frame is dropped, nothing is retransmitted, and subsequent frames are
  unaffected

#### Scenario: Underlay is smaller than the wire's minimum segment

- **WHEN** the Pod network MTU is below the wire's minimum segment plus transport overhead
- **THEN** frames still cross the Link correctly, with the outer datagrams fragmented at the
  IP layer instead of sized to the observed underlay

#### Scenario: Devices exchange VLAN-tagged frames

- **WHEN** the endpoint devices exchange 802.1Q single-tagged frames at the Link MTU or
  double-tagged frames within the single-tag headroom budget
- **THEN** the frames arrive at the peer device with their tags intact


### Requirement: Required interfaces exist before device boot

For kinds whose compatibility plan requires interfaces at process start, the runtime SHALL create correctly named endpoint interfaces before the corresponding device application container starts. Initial connectivity readiness MUST be observable and MUST gate application start or readiness as declared by the plan.

#### Scenario: Cold-start a strict vendor kind

- **WHEN** a Node plan requires all dataplane interfaces before its device process starts
- **THEN** every accepted Link endpoint is present with its final name and MTU before that process starts

#### Scenario: Remote endpoint is not ready

- **WHEN** a cross-worker peer is absent during initial realization
- **THEN** the local interface is prepared as required, connectivity remains unready, and reconciliation completes after the peer appears

#### Scenario: Inspect connectivity helper authority

- **WHEN** a direct workload and its connectivity helper identity are rendered
- **THEN** the helper receives only namespace-scoped Link observation and Pod-log read authority, has no image-import or workload-mutation permission, and device containers receive no Kubernetes credential

### Requirement: Link reconciliation is live and idempotent

Creating, changing, rewiring, or deleting a Link SHALL converge from current state without recreating a device Pod unless the affected kind's declared link-apply mode requires restart or recreation. Repeated reconciliation MUST neither duplicate interfaces nor leak tunnel, process, route, or allocation state.

For an imported `Live` transition, c9s SHALL treat unchanged non-interface planning input as proof that freshly emitted container or artifact differences are creation-time consequences of the new endpoint inventory. The running Pod SHALL retain its accepted cold-only state while the connectivity revision applies the new interfaces and interface-wait actions. A later legitimate recreation SHALL use the complete current cold plan. c9s MUST NOT recognize a kind, vendor, environment name, file, or template to make this decision.

#### Scenario: Add a live Link

- **WHEN** a Link is added to running Nodes whose plans support live link application
- **THEN** traffic can pass without recreating their device containers

#### Scenario: Rewire an endpoint

- **WHEN** a Link changes one endpoint
- **THEN** obsolete state is removed and only the new endpoints converge

#### Scenario: Kind requires lifecycle action

- **WHEN** a changed Link terminates on a kind whose plan declares restart or recreation
- **THEN** c9s performs exactly that declared action and reports it through Kubernetes events and status

### Requirement: Cross-worker connectivity survives scheduling changes

Cross-worker Links SHALL derive peer identity from current Pod placement and SHALL converge after Pod deletion, Pod IP change, rescheduling, worker loss, or controller and connectivity-sidecar restart. Link identity and allocations MUST remain bounded in Link status and MUST not depend on stale Pod-local state.

#### Scenario: Endpoint Pod is rescheduled

- **WHEN** one endpoint Pod is recreated on another worker with a different Pod IP
- **THEN** both ends update their dataplane state and traffic resumes without recreating the unaffected device Pod unless its plan requires it

#### Scenario: Connectivity component restarts

- **WHEN** a responsible controller, CNI component, or Pod sidecar restarts
- **THEN** it reconstructs desired state from Kubernetes resources and removes stale local state

### Requirement: Connectivity cleanup is ownership safe

Every interface, tunnel, helper process, route, translation rule, and allocation created for a Link or a management identity SHALL carry or derive stable ownership sufficient for exact cleanup. Deleting a Link, Node, or Pod MUST remove only its owned connectivity state and MUST tolerate already-absent state. Pod-owned connectivity state SHALL be bounded by the Pod network namespace lifetime: forced Pod deletion leaves no connectivity residue on the worker, including for host Links, whose worker-side veth ends die with their Pod-side peers.

#### Scenario: Delete a Link

- **WHEN** a realized Link is deleted
- **THEN** all state owned exclusively by that Link is removed from both endpoints

#### Scenario: Delete one Node

- **WHEN** one Node is deleted while unrelated labs use the same workers
- **THEN** its Link state is removed without modifying unrelated namespaces, interfaces, tunnels, or allocations

#### Scenario: Pod is force-deleted

- **WHEN** a direct Pod is force-deleted without any component running cleanup
- **THEN** every piece of its connectivity state, including worker-side host-Link veth ends, vanishes with the Pod network namespace and nothing on the worker requires sweeping

### Requirement: Connectivity maintains namespace isolation

Standalone Nodes SHALL have distinct network namespaces. Only explicitly grouped Nodes and components whose device plans declare namespace sharing MAY share a Pod namespace. Connectivity components MUST scope operations to the resolved Pod UID, container namespace, Node UID, and Link UID so name reuse cannot mutate a replacement or unrelated workload.

#### Scenario: Node name is reused

- **WHEN** a Node and Pod are deleted and replacements are created with the same names but different UIDs
- **THEN** stale connectivity is removed and cannot be attached to the replacements without new resolution

### Requirement: Management identity is allocated and realized Pod-locally

The direct runtime SHALL realize containerlab's always-addressed management model with
controller-allocated identities: every logical Node's management address comes from the topology's
management policy, or, when no policy is declared, from containerlab's default management subnet
and gateway convention. The runtime MUST NOT use the Pod address as a management identity. The
Pod's connectivity sidecar SHALL interpose a synthetic management interface carrying the allocated
address before any device container starts, and management configuration SHALL render at plan time
through each kind's own imported templates using the allocated address, prefix, and sidecar
gateway. The interposed management interface SHALL be a member of its namespace's management L2
mesh, so the allocated identity is reachable from every peer device in the namespace by
address, and the sidecar SHALL maintain mesh peer state on the same reconciliation tick that
re-asserts its other owned state.

#### Scenario: Imported hook dials the management address

- **WHEN** an imported package hook running inside an application container dials the logical
  Node's management address after the device adopted the interface presented to it
- **THEN** the dial reaches the device's management plane pod-locally without any kind- or
  vendor-specific handling

#### Scenario: Operator declares a management policy

- **WHEN** the operator allocates explicit management addresses through a management policy
- **THEN** the controller-allocated addresses are used unchanged

#### Scenario: Topology declares no management policy

- **WHEN** a topology is deployed without a management policy
- **THEN** the controller allocates each node's management identity from containerlab's default
  management subnet with containerlab's gateway convention, and devices observe those addresses
  exactly as they would under containerlab

#### Scenario: Package templates render the management identity

- **WHEN** an imported kind generates configuration from its own management-parameterized templates
- **THEN** the render uses the allocated management address, prefix, and sidecar gateway at plan
  time, so a topology with no startup-config reaches full management without kind-specific handling

#### Scenario: Peer device dials the allocated identity

- **WHEN** a peer device in the same namespace dials the allocated management address from its own
  Pod
- **THEN** the dial reaches this device over the management mesh without translation, and the
  reply returns the same way

### Requirement: Sidecar interposition preserves the Kubernetes underlay

The connectivity sidecar SHALL, before any device container starts, preserve the CNI-installed interface under a sidecar-owned identity with its addresses and routes intact, and SHALL present the device with a separate synthetic interface carrying the expected interface name and MAC behavior and the allocated management address. A device process MUST NOT receive exclusive ownership of the interface on which Kubernetes transport depends.

Kubernetes transport — Pod IP reachability, default egress, cluster CIDR routing, and DNS — SHALL remain functional throughout device startup, restart, and steady state, even when the device rewrites the main routing table, namespace sysctls, or shared packet-filter chains. The sidecar SHALL keep transport in state it exclusively owns and SHALL re-assert that state whenever a device mutation invalidates it.

#### Scenario: Device adopts the synthetic interface

- **WHEN** a device boots and adopts, renames, or strips the synthetic management interface as it would a physical management port
- **THEN** the Pod IP, default route, DNS, and Service reachability observed from the Pod are unchanged, and the device observes its allocated management address on its management plane

#### Scenario: Device rewrites shared namespace state

- **WHEN** a same-namespace device replaces the main-table default route, disables namespace forwarding, or installs default-deny packet-filter chains during boot
- **THEN** Kubernetes transport and cross-Pod fabric continue over sidecar-owned state, and the sidecar re-asserts any of its state the device displaced without fighting the device for state the device owns

#### Scenario: Interposition state dies with the Pod

- **WHEN** a Pod is deleted, force-deleted, or replaced
- **THEN** all interposition state vanishes with the Pod network namespace and no per-Pod management state remains on the worker host

### Requirement: Interposed management traffic is translated at the Pod boundary

The sidecar SHALL translate between the device management identity and the Pod's Kubernetes identity: outbound device-originated management traffic SHALL be source-translated to the Pod IP for both traffic shapes — flows forwarded from a device-internal network context and locally-originated flows that hairpin through the synthetic interface pair — and declared management ports SHALL be reachable at the Pod IP through destination translation so existing Service exposure keeps working. Inbound destination-translated connections SHALL also be source-translated to the Pod-local management gateway, so the device answers an on-subnet peer over its connected management route and MUST NOT need any off-subnet management route for declared-port reachability — matching the source identity containerlab's Docker port publishing presents. Translation MUST NOT alter management traffic between the device and its own management subnet, and translation state MUST take precedence over any packet-translation state a device programs in the shared namespace.

The sidecar SHALL ensure transport-protocol integrity across the synthetic interface pair (including checksum-offload handling).

#### Scenario: Device dials an off-subnet destination

- **WHEN** the device originates management traffic to a destination outside its management subnet
- **THEN** the traffic leaves the Pod source-translated to the Pod IP and replies reach the device's management plane regardless of which of the two traffic shapes the kind produces

#### Scenario: Client connects through a declared port

- **WHEN** a cluster client connects to a declared management port on the Pod IP or through a Service targeting it
- **THEN** the connection terminates on the device's management plane at the allocated management address, and transport protocols work end to end

#### Scenario: Device holds only its connected management route

- **WHEN** the device's management stack carries no route beyond the connected management subnet and an off-subnet client connects through a declared port
- **THEN** the connection succeeds, with the device observing the Pod-local management gateway as the client address

#### Scenario: Pod identity changes on recreation

- **WHEN** a Pod is recreated and receives a different Pod IP
- **THEN** the sidecar re-renders translation state from the new Pod identity while the device management identity is unchanged

### Requirement: Connectivity requires no node-resident agent

The direct runtime SHALL realize all connectivity — management, cross-Pod fabric, and host Links — from Pod-scoped components. No `hostNetwork` DaemonSet, node-resident daemon, or host-side socket SHALL exist or be mounted for connectivity. The sidecar's only host-namespace access SHALL be the read-only worker network-namespace handle used to place host-Link veth ends, and it MUST NOT be mounted for topologies without host Links.

#### Scenario: Deploy without host links

- **WHEN** a topology with only cross-Pod, same-Pod, and loopback Links is deployed
- **THEN** its Pods run with no host-namespace access of any kind and all connectivity converges

#### Scenario: Worker inventory

- **WHEN** an operator inspects a worker running direct workloads
- **THEN** no c9s node-resident connectivity agent, daemon socket, or per-Pod host management state exists on it
