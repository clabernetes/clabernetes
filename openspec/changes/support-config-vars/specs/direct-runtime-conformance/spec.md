# Spec Delta

## ADDED Requirements

### Requirement: Config variable rendering has running-device evidence

Direct-runtime conformance SHALL boot a real device with a startup-config template consuming nested `config.vars` and verify the rendered values in the device's running configuration. Successful planning or Pod readiness alone MUST NOT satisfy this requirement.

#### Scenario: SR Linux boots with variable-derived configuration

- **GIVEN** an SR Linux Node declares nested configuration variables and a CLI-format startup-config template that references them
- **WHEN** its direct device Pod becomes ready and imported post-deploy configuration completes
- **THEN** querying the running device shows the supplied values at the templated configuration paths
