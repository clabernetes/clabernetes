## Context

Before this change, the launcher only wrote a healthy status marker when an explicit TCP or SSH
probe was configured. The Node Deployment rendered Kubernetes startup and readiness probes only
for those same configurations, so a generic node had no readiness signal. Even configured probes
did not account for the nested Docker container stopping, restarting, or reporting an image-defined
healthcheck failure.

The launcher already owns the nested Docker daemon and the status-file handoff to Kubernetes. The
manager already exposes `/alive` and tracks whether controller-runtime startup has completed, but
the manager Deployment used that endpoint only as a liveness probe.

## Goals / Non-Goals

**Goals:**

- Give every enabled, non-excluded Node a generic readiness baseline without inferring device kind,
  image-specific ports, or credentials.
- Preserve explicit TCP and SSH checks as additive application-level requirements.
- Keep startup failure behavior bounded for slow network operating system images while making fast
  nodes ready without waiting for the full allowance.
- Make manager rollout availability depend on the existing controller-readiness state.
- Preserve compatibility with launchers that receive only the legacy TCP/SSH environment variables.

**Non-Goals:**

- Defining application readiness for images without a Docker healthcheck or explicit TCP/SSH probe.
- Adding a new readiness API field or changing Node status vocabulary.
- Replacing the existing `/alive` endpoint or changing leader-election semantics.
- Reporting structured readiness failure reasons through the status file.

## Decisions

### 1. Use Docker state and image healthchecks as the generic signal

The launcher runs `docker inspect --format '{{json .State}}'` for the represented nested
container. Readiness requires `Running=true` and rejects `Paused`, `Restarting`, and `Dead`.
When Docker provides a health object, its status must be `healthy`; a missing health object means
the image has no healthcheck and the running-state signal is accepted.

This avoids hardcoded assumptions about node kinds, image names, management ports, or credentials.
Inferring readiness from a port was rejected because generic images may expose no stable port or may
expose a port before the service is usable.

### 2. Compose generic and explicit probes additively

`statusProbes.enabled` controls whether the launcher writes the status marker and whether the
Deployment renders Kubernetes startup/readiness probes. The generic Docker signal is always
evaluated when enabled. Configured TCP and SSH checks are evaluated after the generic signal and
all configured checks must pass.

The controller sets a dedicated `LAUNCHER_STATUS_PROBES_ENABLED` environment variable. The launcher
also enables probing when legacy TCP or SSH variables are present, so a newer launcher remains
compatible with an older manager during a rolling image transition.

### 3. Represent unhealthy and healthy states with an empty/non-empty file

The launcher creates `.nodestatus` as an empty file before image loading and container startup.
Each probe cycle rewrites it with `healthy` only when all checks pass, otherwise it rewrites it
empty. Kubernetes uses `test -s` for both startup and readiness probes.

An empty marker is preferred over testing for file existence because it explicitly distinguishes the
normal not-ready state from a healthy state without treating a missing file as a special startup
error. It also avoids depending on `grep` being available or on the marker's textual contents.

### 4. Poll immediately, then every 10 seconds

The launcher evaluates readiness once as soon as probing starts and then uses a 10-second ticker.
The Deployment uses a 10-second probe period, a 10-second initial delay, and a three-failure
readiness threshold. The default startup failure threshold is 90 periods, retaining roughly
15 minutes for image transfer and slow NOS startup. A configured `startupSeconds` value is rounded
up to a whole 10-second period and bounded to at least one failure.

This removes the old first-result delay of one full 30-second polling interval while retaining
enough startup budget for large images. A successful startup probe immediately hands control to
the readiness probe.

### 5. Reuse `/alive` for manager readiness

The chart adds a readiness probe to the manager container using the existing HTTPS `/alive`
endpoint. The endpoint already returns success only after the controller-runtime cache is synced
and controllers are registered (or when this replica is not the elected leader). No new endpoint
or manager state machine is introduced.

## Risks / Trade-offs

- **Running without an image healthcheck is only process-level readiness** → Document this limitation
  and require an image healthcheck or explicit TCP/SSH probe when service-level readiness matters.
- **A Docker inspect error reports not ready** → Log the error and keep the status marker empty;
  container lifecycle monitoring still handles a missing or terminated nested container.
- **The empty marker does not expose a failure reason** → Keep diagnostics in launcher logs and avoid
  coupling the probe contract to a new status schema.
- **The default startup budget is intentionally generous** → Fast nodes are not delayed because the
  first successful startup check transitions to readiness immediately.
- **The manager readiness probe can delay rollout while controllers initialize** → This is intentional;
  it prevents Kubernetes from advertising a manager that cannot yet reconcile resources.

## Migration Plan

No manifest migration is required. Existing `statusProbes` fields retain their names and explicit
TCP/SSH configurations continue to work, but enabled profiles now also require the nested container
and any image-defined healthcheck to be ready. Users whose images contain failing healthchecks must
fix the healthcheck or disable status probes when process-level readiness is sufficient.

The generated CRDs, OpenAPI documents, Helm templates, and golden fixtures are regenerated with the
updated API descriptions. A rollback to the previous release remains possible; older launchers
ignore the new enablement variable and retain the previous explicit-probe behavior.

## Open Questions

- Whether future versions should expose the Docker or application probe failure reason in Node
  status rather than only in launcher logs.
- Whether manager `/alive` should eventually be split into separate liveness and readiness
  endpoints if controller startup and process health need independent semantics.
