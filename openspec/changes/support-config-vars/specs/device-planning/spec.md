# Spec Delta

## ADDED Requirements

### Requirement: Config variables use imported startup-config rendering

The planner SHALL accept `config.vars` and pass its values unchanged to imported Containerlab startup-config rendering. Rendered bytes SHALL use the existing generated-artifact and lifecycle boundaries. Payload path resolution MUST NOT mutate arbitrary variable values.

#### Scenario: Plan a templated startup configuration

- **GIVEN** a Node declares nested `config.vars` and a startup-config template that references them
- **WHEN** the direct device planner evaluates the Node
- **THEN** planning succeeds and the resulting configuration content contains the declared values instead of template expressions

#### Scenario: Variable text equals a payload destination

- **GIVEN** a Node declares a startup-config payload and a `config.vars` string identical to that payload's declared destination
- **WHEN** planning resolves the startup-config file reference and renders the template
- **THEN** the file reference resolves to verified payload content while the variable retains its original literal string

#### Scenario: Reconstruct a Node with templated configuration

- **WHEN** an imported lifecycle boundary reconstructs a Node whose startup configuration consumes `config.vars`
- **THEN** the same unchanged variable values are available, so lifecycle behavior uses configuration consistent with the accepted plan
