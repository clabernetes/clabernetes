## MODIFIED Requirements

### Requirement: Enabled Node readiness reflects generic launcher state

When status probes are enabled for a non-excluded Node, the launcher SHALL report readiness only
when the represented nested Docker container is running and is not paused, restarting, or dead. If
the nested image defines a Docker healthcheck, that healthcheck SHALL also report `healthy`.

When multiple Nodes share one launcher through `network-mode: container:<primary>`, the launcher
SHALL evaluate the generic nested-container readiness of every group member. The shared launcher
Pod SHALL be ready only when all group members are ready; all member Nodes inherit that atomic
group result. Application-specific TCP or SSH probes remain scoped to the primary Node.

#### Scenario: Generic Node has no application-specific probe

- **WHEN** a Node uses an enabled status-probe configuration without TCP or SSH settings
- **THEN** its launcher Deployment renders startup and readiness probes and reports the generic nested-container readiness through the launcher status marker

#### Scenario: Running container without a healthcheck

- **WHEN** the represented nested container is running, not paused, not restarting, not dead, and has no Docker healthcheck
- **THEN** the Node reports ready under the generic readiness contract

#### Scenario: Nested container is not runnable

- **WHEN** the represented nested container is stopped, paused, restarting, or dead
- **THEN** the Node reports not ready

#### Scenario: Grouped secondary container is not ready

- **WHEN** a secondary nested container is paused, restarting, stopped, dead, or has an unhealthy image healthcheck while the primary remains healthy
- **THEN** the shared launcher Pod and every Node in the launcher group report not ready

#### Scenario: Image healthcheck is not healthy

- **WHEN** the represented nested container is running but its Docker healthcheck is `starting` or `unhealthy`
- **THEN** the Node reports not ready

#### Scenario: Image healthcheck becomes healthy

- **WHEN** a running nested container's Docker healthcheck reports `healthy`
- **THEN** the generic readiness condition succeeds

#### Scenario: Explicit application probe fails

- **WHEN** the nested container satisfies the generic readiness contract but a configured TCP or SSH probe fails
- **THEN** the Node remains not ready

#### Scenario: Status probes are disabled or excluded

- **WHEN** status probes are disabled for a Node or the Node is listed in `excludedNodes`
- **THEN** the controller does not render launcher status probes for that Node

## ADDED Requirements

### Requirement: Node status updates tolerate resource-version conflicts

The Node controller SHALL retry status writes after a resource-version conflict and SHALL avoid
issuing an update when the current status already equals the desired status.

#### Scenario: Node status races with a generated-resource update

- **WHEN** a Node status write receives a resource-version conflict
- **THEN** the controller refetches the current Node and retries without failing the reconcile solely because of the conflict
