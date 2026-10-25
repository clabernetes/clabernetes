## ADDED Requirements

### Requirement: Topology preserves deployment network isolation policy

The compiler SHALL copy an explicit `spec.deployment.networkIsolation` value into every emitted NodeProfile's `spec.deployment.networkIsolation` unchanged. An omitted value SHALL remain unset in emitted profiles; the Node controller resolves the ixia-c family default at apply time, so compilation MUST NOT synthesize it.

#### Scenario: Explicit setting passes through

- **WHEN** a Topology sets `spec.deployment.networkIsolation: Enabled`
- **THEN** every emitted NodeProfile carries `networkIsolation: Enabled`

#### Scenario: Omitted setting remains unset

- **WHEN** a Topology omits `spec.deployment.networkIsolation`
- **THEN** emitted NodeProfiles leave the field unset, and resolve-time defaulting remains the sole source of the ixia-c family opt-in
