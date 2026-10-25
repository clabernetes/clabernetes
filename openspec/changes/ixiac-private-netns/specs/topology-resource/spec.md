## MODIFIED Requirement: Topology emits self-contained Nodes with deployment profile grouping

The compiler SHALL preserve the topology's explicit `spec.deployment.networkIsolation` into emitted NodeProfiles unchanged and SHALL include that field in its profile-grouping key, so distinct isolation modes yield distinct profiles even when other policy matches. For the `keysight_ixia-c` family, an unset value from a source topology SHALL be treated as `Enabled` for emitted nodes of that kind, ensuring standalone repro topologies without manual policy converge.

#### Scenario: Explicit setting passes through

- **WHEN** a Topology sets `spec.deployment.networkIsolation: Enabled`
- **THEN** exactly the Nodes referencing its emitted profiles observe isolation

#### Scenario: Mixed topology groups correctly

- **WHEN** one Topology mixes an explicit isolated Node and a shared Node whose other deployment policy is identical
- **THEN** the compiler emits two profiles differing only in `networkIsolation`, each referenced exactly by the Nodes in that group

#### Scenario: Ixia-c default without user policy

- **WHEN** a Topology contains a `keysight_ixia-c-one` Node and no explicit `networkIsolation` is declared
- **THEN** the compiled profile for that Node has `networkIsolation: Enabled`
