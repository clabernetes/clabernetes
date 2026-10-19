## MODIFIED Requirements

### Requirement: Sidecar interposition preserves the Kubernetes underlay

The connectivity sidecar SHALL, before any device container starts, preserve the CNI-installed interface under a sidecar-owned identity with its addresses, routes, and permanent neighbor entries intact, and SHALL present the device with a separate synthetic interface carrying the expected interface name and MAC behavior and the allocated management address. A device process MUST NOT receive exclusive ownership of the interface on which Kubernetes transport depends.

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

#### Scenario: CNI reaches its gateway through a static neighbor

- **WHEN** the CNI plugin installs the Pod gateway as a permanent neighbor entry on the Pod interface instead of relying on address resolution
- **THEN** the entry is present on the preserved interface after interposition, and Pod IP reachability and default egress work without manual repair
- **WHEN** a device flushes the recorded entry from the shared neighbor table
- **THEN** the sidecar re-asserts the recorded entry on the next pass, and Pod IP reachability keeps working without manual repair
