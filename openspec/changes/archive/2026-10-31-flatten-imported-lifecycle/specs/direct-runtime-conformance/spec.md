## MODIFIED Requirements

### Requirement: Compatibility matrix is an executable release gate

The compatibility report SHALL be generated from every kind and alias in the live imported registry and join those names with generic scenario results, image availability observations, imported-interface coverage classifications, and recorded status at verification time. c9s MUST NOT commit expected kind rows or require a matching per-kind fixture. A kind SHALL be reported compatible only when all applicable planning, boot, readiness, management, dataplane, lifecycle, and cleanup evidence passes for the pinned module release, and only when every imported interface method that kind relies on is classified `exercised`.

#### Scenario: Verify matrix completeness

- **WHEN** release verification runs
- **THEN** every registry kind and every supported topology behavior maps to conformance evidence and no entry is implicitly compatible

#### Scenario: Evidence is missing

- **WHEN** a kind lacks a required fixture, scenario, or current validation record
- **THEN** the release gate reports it as incompatible and fails any claim of full direct-runtime parity

#### Scenario: Imported method is unclassified

- **WHEN** an imported interface method has no coverage classification
- **THEN** verification fails, naming the method, before any kind is reported compatible

#### Scenario: Kind depends on a refused method

- **WHEN** a kind's behavior requires an imported interface method classified `refused`
- **THEN** the report lists that kind as incompatible for that behavior and does not count the requirement as covered

#### Scenario: Kind is covered end to end

- **WHEN** a kind has passing boot, readiness, management, dataplane, lifecycle, and cleanup evidence and all imported methods it relies on are classified `exercised`
- **THEN** the report records it as compatible for the pinned module release
