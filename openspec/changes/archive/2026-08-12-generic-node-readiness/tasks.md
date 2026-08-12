## 1. Launcher readiness signal

- [x] 1.1 Add Docker container-state and image-healthcheck decoding for generic readiness, including unit coverage for running, paused, restarting, dead, unhealthy, healthy, missing-healthcheck, and malformed states.
- [x] 1.2 Initialize the launcher status marker as empty before image loading and centralize empty/healthy marker writes.
- [x] 1.3 Compose the generic Docker signal with optional TCP and SSH checks, and preserve legacy explicit-probe environment compatibility.
- [x] 1.4 Evaluate readiness immediately after launch and poll every 10 seconds, retaining the configured startup allowance and handling Docker inspection failures as not ready.

## 2. Kubernetes probe rendering

- [x] 2.1 Render startup and readiness probes whenever enabled status probes apply, even when no TCP or SSH probe is configured.
- [x] 2.2 Use non-empty status-file checks, pass the generic enablement environment variable, and round custom startup allowances up to a whole probe period.
- [x] 2.3 Add controller deployment tests for generic probes, additive application probes, balanced timing, and custom startup rounding.
- [x] 2.4 Add the manager Deployment readiness probe backed by `/alive` and refresh Helm deployment golden fixtures.

## 3. API and documentation surfaces

- [x] 3.1 Update `StatusProbes` and `ProbeConfiguration` API descriptions to define the generic and additive readiness contract.
- [x] 3.2 Regenerate CRD and OpenAPI artifacts and verify generated output is reproducible.
- [x] 3.3 Document process-level readiness, image healthcheck behavior, and explicit application-probe requirements in launcher-profile and upgrade documentation.

## 4. Verification

- [x] 4.1 Run linting, race-enabled tests, package builds, documentation checks, documentation builds, and manager/launcher image builds.
- [x] 4.2 Run lifecycle validation for deploy, inspect, exec, dataplane connectivity, stop/start/restart, events, and destroy.
- [x] 4.3 Verify readiness improvement on a generic multitool workload, reducing observed readiness from approximately 84 seconds to approximately 34–35 seconds.
