## Why

`statusProbes.enabled` previously produced Kubernetes probes only when a TCP or SSH check was configured. Generic containerlab nodes therefore had no readiness signal, while configured probes ignored the nested Docker container's actual lifecycle and image healthcheck. Startup also reported a missing status file until the first slow polling interval, and the manager Deployment could be considered available before its controllers had finished starting.

## What Changes

- Define readiness for an enabled Node as an additive contract:
  - the nested Docker container is running and is not paused, restarting, or dead;
  - an image-defined Docker healthcheck, when present, is healthy;
  - configured TCP and SSH probes remain additional application-level requirements.
- Render startup and readiness probes for every non-excluded Node with `statusProbes.enabled`, including Nodes without TCP or SSH configuration.
- Initialize the launcher status marker as explicitly unhealthy, update it immediately after launch, and poll readiness every 10 seconds while preserving roughly 15 minutes for slow startup.
- Round custom startup allowances up to a whole probe interval so requested startup time is not shortened.
- Add a manager Deployment readiness probe backed by `/alive`, so rollout availability waits for controller-runtime cache synchronization and controller registration.
- Document the generic readiness contract and regenerate affected CRD/OpenAPI and Helm fixture artifacts.

## Capabilities

### New Capabilities

- `manager-readiness`: Manager rollout availability uses the existing `/alive` controller-readiness signal.

### Modified Capabilities

- `node-lifecycle`: Node readiness is derived from the launcher-reported nested-container observation and is reflected for generic nodes, not only nodes with application probes.
- `launcher-profiles`: Enabled `statusProbes` always establish the generic Docker readiness baseline; configured TCP and SSH checks are additive, and the startup/readiness timing contract is clarified.

## Impact

- Launcher readiness and Docker inspection in `launcher/clabernetes.go` and `launcher/docker.go`, including the launcher status marker and probe interval.
- Node Deployment rendering and tests in `controllers/node/deployment.go` and `controllers/node/deployment_test.go`.
- `StatusProbes` API descriptions, generated CRD/OpenAPI artifacts, Helm manager Deployment templates, and golden fixtures.
- Launcher profile and upgrade documentation.
- No new runtime dependency or user-facing API field is introduced.
