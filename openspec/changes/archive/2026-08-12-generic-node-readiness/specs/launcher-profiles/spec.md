## ADDED Requirements

### Requirement: Status probes compose generic and application readiness

When a LauncherProfile enables status probes, the system SHALL establish the nested Docker
container and image-healthcheck result as the baseline readiness signal. Configured TCP and SSH
checks SHALL remain additional requirements and the system MUST NOT infer application checks from a
Node kind, image name, port, or credentials.

#### Scenario: Enabled profile supplies generic probes

- **WHEN** a Node references a LauncherProfile with `statusProbes.enabled: true` and no TCP or SSH
  configuration
- **THEN** the rendered launcher Deployment contains startup and readiness probes and the launcher
  receives an explicit status-probe enablement signal

#### Scenario: Enabled profile supplies application probes

- **WHEN** a LauncherProfile configures TCP or SSH checks
- **THEN** the rendered launcher requires the generic nested-container signal and every configured
  application check before reporting ready

#### Scenario: Profile does not infer application behavior

- **WHEN** an enabled LauncherProfile targets an arbitrary Node kind or image without explicit
  application probe configuration
- **THEN** the launcher performs no inferred TCP or SSH check

#### Scenario: Custom startup allowance is preserved

- **WHEN** a profile sets `statusProbes.probeConfiguration.startupSeconds` to a value that is not an
  exact multiple of the probe period
- **THEN** the rendered startup probe allows at least the requested duration by rounding up to a
  whole probe interval

#### Scenario: Fast startup is not delayed by the allowance

- **WHEN** the nested container satisfies all readiness checks before the configured startup
  allowance expires
- **THEN** the startup probe succeeds and the readiness probe takes over without waiting for the
  remaining allowance
