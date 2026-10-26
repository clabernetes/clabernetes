## MODIFIED Requirements

### Requirement: Sidecar interposition preserves the Kubernetes underlay

The connectivity sidecar SHALL, before any device container starts, preserve the CNI-installed interface under a sidecar-owned identity with its addresses and routes intact, and SHALL present the device with a separate synthetic interface carrying the expected interface name and MAC behavior and the allocated management address. A device process MUST NOT receive exclusive ownership of the interface on which Kubernetes transport depends.

When a Node's resolved NodeProfile enables `networkIsolation`, the runtime SHALL place the device's view of the network in a private namespace: the Pod retains `c9s0`/`c9sr0`/`c9sm0`/fabric legs while only the device-side ends of the synthetic and fabric pairs appear in the device namespace, moved by the sidecar via a bound netns handle. Isolation MUST NOT alter management allocation, mesh membership, or link identity.

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

#### Scenario: Private device netns when enabled

- **WHEN** a Node's resolved NodeProfile has `networkIsolation: true`
- **THEN** its device container starts inside a sidecar-created private netns containing only its device-side ports while the sidecar keeps `c9s0`, `c9sr0`, `c9sm0`, and each fabric sidecar leg in the Pod namespace linked across namespaces by veth pairs

#### Scenario: Ixia-c family default at profile resolution

- **WHEN** a `keysight_ixia-c-one` (or alias `keysight_ixia-c`) Node resolves a profile with no explicit `networkIsolation`
- **THEN** profile resolution enables isolation so the single-Node repro behind #372 converges without manual opt-in, and an explicit `false` still wins
