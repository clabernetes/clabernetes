## ADDED Requirement: NodeProfile controls network isolation

`NodeProfile.spec.deployment.networkIsolation` SHALL govern whether direct device Pods place device-side interfaces into a private network namespace. Omitted SHALL mean `Disabled`. The field is optional, explicit, and profile-owned; global Config defaults do not synthesize it.

#### Scenario: Explicit opt-in

- **WHEN** a referenced NodeProfile sets `networkIsolation: Enabled`
- **THEN** affected Nodes run with device-netns isolation regardless of kind

#### Scenario: Explicit opt-out overrides default

- **WHEN** a `keysight_ixia-c-one` Node references a NodeProfile with `networkIsolation: Disabled`
- **THEN** the Node stays in shared-namespace mode despite the implicit kind default and exhibits the pre-existing greedy-interface failure until re-profiled
