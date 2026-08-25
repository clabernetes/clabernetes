# direct-connectivity Delta

## MODIFIED Requirements

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

### Requirement: Management identity is allocated and realized Pod-locally

The direct runtime SHALL realize containerlab's always-addressed management model with
controller-allocated identities: every logical Node's management address comes from the topology's
management policy, or, when no policy is declared, from containerlab's default management subnet
and gateway convention. The runtime MUST NOT use the Pod address as a management identity. The
Pod's connectivity sidecar SHALL interpose a synthetic management interface carrying the allocated
address before any device container starts, and management configuration SHALL render at plan time
through each kind's own imported templates using the allocated address, prefix, and sidecar
gateway. The interposed management interface SHALL be a member of its namespace's management L2
mesh, so the allocated identity is reachable from every peer device in the namespace by address,
and the sidecar SHALL maintain mesh peer state on the same reconciliation tick that re-asserts its
other owned state.

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
