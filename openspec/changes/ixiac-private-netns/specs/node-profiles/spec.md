## ADDED Requirements

### Requirement: NodeProfile controls network isolation

`NodeProfile.spec.deployment.networkIsolation` SHALL govern whether direct device Pods place device-side interfaces into a private network namespace. Omitted SHALL leave the field unset so profile resolution can apply the ixia-c family default; an explicit value SHALL always win. Global Config defaults do not synthesize this field.

#### Scenario: Explicit opt-in

- **WHEN** a referenced NodeProfile sets `networkIsolation: true`
- **THEN** affected Nodes run with device-netns isolation regardless of kind

#### Scenario: Explicit opt-out overrides default

- **WHEN** a `keysight_ixia-c-one` Node references a NodeProfile with `networkIsolation: false`
- **THEN** the Node stays in shared-namespace mode despite the implicit kind default and exhibits the pre-existing greedy-interface failure until re-profiled

#### Scenario: Omitted value inherits family default

- **WHEN** a `keysight_ixia-c` or `keysight_ixia-c-one` Node resolves a profile that omits `networkIsolation`
- **THEN** resolution enables isolation while leaving the stored profile field unset
