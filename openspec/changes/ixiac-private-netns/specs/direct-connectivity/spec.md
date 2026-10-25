## MODIFIED Requirement: Sidecar interposition preserves the Kubernetes underlay

The connectivity sidecar SHALL, before any device container starts, preserve the CNI-installed interface under a sidecar-owned identity with its addresses and routes intact, and SHALL present the device with a separate synthetic interface carrying the expected interface name and MAC behavior and the allocated management address. A device process MUST NOT receive exclusive ownership of the interface on which Kubernetes transport depends.

When a Node's resolved NodeProfile enables `networkIsolation`, the runtime SHALL place the device's view of the network in a private namespace: the Pod retains `c9s0`/`c9sr0`/`c9sm0`/fabric legs while only the device-side ends of the synthetic and fabric pairs appear in the device namespace, moved by the sidecar via a bound netns handle. For the `keysight_ixia-c` family, default topology compilation and standalone resolution enable this setting when no explicit value is set. Isolation MUST NOT alter management allocation, mesh membership, or link identity.

Kubernetes transport — Pod IP reachability, default egress, cluster CIDR routing, and DNS — SHALL remain functional throughout device startup, restart, and steady state, even when the device rewrites the main routing table, namespace sysctls, or shared packet-filter chains. The sidecar SHALL keep transport in state it exclusively owns and SHALL re-assert that state whenever a device mutation invalidates it.

#### Scenario: Private device netns when enabled

- **WHEN** a Node's resolved NodeProfile has `networkIsolation: Enabled`
- **THEN** its device container starts inside a sidecar-created private netns containing only its device-side ports while the sidecar keeps `c9s0`, `c9sr0`, `c9sm0`, and each fabric sidecar leg in the Pod namespace linked across namespaces by veth pairs

#### Scenario: Implicit default for ixia-c

- **WHEN** a `keysight_ixia-c-one` (or alias `keysight_ixia-c`) Node specifies no explicit `networkIsolation`
- **THEN** compilation/resolution enables isolation so the single-Node repro behind #372 converges without manual opt-in
