## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Every Link flavor has one Pod-local realization

The runtime SHALL directly realize same-Pod, loopback, host, and cross-Pod Links with the requested endpoint names and MTU, entirely from within the Pod: cross-Pod transports terminate inside the Pod network namespace on the sidecar-preserved Kubernetes underlay, and host Links place one veth end into the worker network namespace through the sidecar's read-only host-namespace handle. The device receives a plain interface leg and never owns the transport underlay, so a kind that adopts its presented interfaces cannot disturb any transport. The `vxlan` and `slurpeeth` connectivity values remain accepted input and both select this Pod-local realization. No Link flavor MAY use a nested network-device container and no Link flavor MAY require a node-resident agent.

#### Scenario: Realize each supported flavor

- **WHEN** a valid Link selects VXLAN, slurpeeth, same-Pod, loopback, or host connectivity
- **THEN** the declared interfaces and dataplane are realized in the endpoint namespaces with the requested MTU by the Pod's own connectivity sidecar

#### Scenario: Device adopts its presented interfaces

- **WHEN** a device kind renames, re-addresses, or adopts the interfaces presented to it at boot
- **THEN** cross-Pod Link transports keep working because they terminate on the sidecar-preserved underlay the device never receives

#### Scenario: Flavor cannot be realized

- **WHEN** cluster or Node capabilities cannot satisfy the selected flavor
- **THEN** the Link reports a specific failure and no partially connected substitute is created

### Requirement: Management identity is allocated and realized Pod-locally

The direct runtime SHALL realize containerlab's always-addressed management model with controller-allocated identities: every logical Node's management address comes from the topology's management policy, or, when no policy is declared, from containerlab's default management subnet and gateway convention. The runtime MUST NOT use the Pod address as a management identity. The Pod's connectivity sidecar SHALL interpose a synthetic management interface carrying the allocated address before any device container starts, and management configuration SHALL render at plan time through each kind's own imported templates using the allocated address, prefix, and sidecar gateway.

#### Scenario: Imported hook dials the management address

- **WHEN** an imported package hook running inside an application container dials the logical Node's management address after the device adopted the interface presented to it
- **THEN** the dial reaches the device's management plane pod-locally without any kind- or vendor-specific handling

#### Scenario: Operator declares a management policy

- **WHEN** the operator allocates explicit management addresses through a management policy
- **THEN** the controller-allocated addresses are used unchanged

#### Scenario: Topology declares no management policy

- **WHEN** a topology is deployed without a management policy
- **THEN** the controller allocates each node's management identity from containerlab's default management subnet with containerlab's gateway convention, and devices observe those addresses exactly as they would under containerlab

#### Scenario: Package templates render the management identity

- **WHEN** an imported kind generates configuration from its own management-parameterized templates
- **THEN** the render uses the allocated management address, prefix, and sidecar gateway at plan time, so a topology with no startup-config reaches full management without kind-specific handling


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

The sidecar SHALL translate between the device management identity and the Pod's Kubernetes identity: outbound device-originated management traffic SHALL be source-translated to the Pod IP for both traffic shapes — flows forwarded from a device-internal network context and locally-originated flows that hairpin through the synthetic interface pair — and declared management ports SHALL be reachable at the Pod IP through destination translation so existing Service exposure keeps working. Translation MUST NOT alter management traffic between the device and its own management subnet, and translation state MUST take precedence over any packet-translation state a device programs in the shared namespace.

The sidecar SHALL ensure transport-protocol integrity across the synthetic interface pair (including checksum-offload handling).

#### Scenario: Device dials an off-subnet destination

- **WHEN** the device originates management traffic to a destination outside its management subnet
- **THEN** the traffic leaves the Pod source-translated to the Pod IP and replies reach the device's management plane regardless of which of the two traffic shapes the kind produces

#### Scenario: Client connects through a declared port

- **WHEN** a cluster client connects to a declared management port on the Pod IP or through a Service targeting it
- **THEN** the connection terminates on the device's management plane at the allocated management address, and transport protocols work end to end

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

## REMOVED Requirements

### Requirement: Every Link flavor has one direct realization

**Reason**: Cross-Pod transports no longer terminate in the worker host namespace and no node-local daemon exists; the replacement requirement "Every Link flavor has one Pod-local realization" defines the sidecar-owned realization.

**Migration**: Links keep their vocabulary; realization moves to the Pod connectivity sidecar with no topology changes.

### Requirement: Management identity and reachability are always realized

**Reason**: The Pod-address management identity and the daemon management loop are removed; the replacement requirement "Management identity is allocated and realized Pod-locally" defines controller-allocated identities and sidecar interposition.

**Migration**: Declare a management policy for explicit addressing, or accept containerlab default-subnet allocation; hooks keep dialing the same management addresses.
