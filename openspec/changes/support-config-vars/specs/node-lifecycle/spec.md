# Spec Delta

## ADDED Requirements

### Requirement: Config variables render into Node startup configuration

A Node SHALL accept Containerlab `config.vars` containing nested arbitrary values. Its startup configuration SHALL render through Containerlab's normal template behavior, and the booted Node SHALL receive the resulting configuration. Variables alone MUST NOT trigger an independent post-start configuration mechanism.

#### Scenario: Nested variables are preserved

- **WHEN** a Node declares nested lists, mappings, scalars, and path-like strings under `config.vars`
- **THEN** the complete values reach startup-config rendering unchanged

#### Scenario: Boot with rendered variables

- **GIVEN** a Node declares a startup-config template that references nested `config.vars`
- **WHEN** its direct workload boots
- **THEN** the Node becomes ready and its effective device configuration contains the values supplied by those variables at the referenced locations

#### Scenario: Variables have no startup-config consumer

- **WHEN** a Node declares `config.vars` without a startup-config template that consumes them
- **THEN** the Node remains eligible for planning and boot, and c9s does not invent a separate configuration application action
