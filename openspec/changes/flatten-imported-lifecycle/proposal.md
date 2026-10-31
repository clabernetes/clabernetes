## Why

Imported containerlab code is executed in four places: the planning worker, the preparation init
container, the connectivity sidecar, and the lifecycle readiness/save boundary. Three of them run
inside the device Pod, where a failed hook is visible only as a Pod that never starts, needs the
Docker-shaped runtime facade, the `docker`/`podman` CLI shims, entropy replay, and a re-render path
whose only permitted difference is the runtime management identity. The planner already exists to
run imported code in a sandbox; the other three sites buy complexity, not coverage.

The borrowed contract is also silently incomplete. `PreDestroy` has been part of the imported node
interface since containerlab 0.80 and c9s neither invokes nor reports it: a hook that is never
called emits no recorded operation, so registry-driven conformance cannot fail on it. Any future
method inherits the same invisibility. Until that blind spot is a test, c9s cannot honestly describe
which imported behavior it supports.

## What Changes

- **One imported-code execution site.** Imported kind code runs only in the planning worker.
  Preparation containers and sidecars stage, verify, and apply plan data; they MUST NOT initialize,
  deploy, render, or hook an imported kind.
- **Plan-borne artifact content.** Files a package generates become content-addressed plan artifacts
  with explicit digest and size bounds, replacing generation-at-boot. Preparation keeps its existing
  digest verification and device-modified-file preservation semantics.
- **Explicit runtime management identity.** The management identity a Pod contributes is resolved by
  a bounded, verified substitution over plan bytes instead of re-rendering package templates inside
  the Pod; the `runtime-artifacts.json` divergence record goes away with the re-render path.
- **Imported hooks become plan steps.** Post-deploy, readiness, save, and endpoint-fixup hooks are
  flattened into typed plan steps (concrete exec/file writes plus conditional steps whose predicate
  is evaluated on device output) executed by a c9s-owned interpreter. The `ImportedPostDeploy`,
  `ImportedReadiness`, `ImportedDeployEndpoints`, and imported-save plan actions stop being replayed.
- **Removals:** the in-Pod imported runtime facade, the `docker`/`podman` runtime-CLI shims, in-Pod
  hook replay and its operation-drift validation, and preparation-time entropy sessions.
- **Imported-hook coverage guard.** Every method of the imported node interface and its overwrite
  interfaces is either exercised through the planning boundary or explicitly refused with a recorded
  reason. An unclassified method fails verification instead of being silently ignored.
- **BREAKING (internal plan schema):** `SchemaVersion` is bumped; generator-sourced files and the
  imported action kinds are removed from the plan. There is no change to custom resources or to the
  containerlab version pin, and applied Pods keep running until their next plan is reconciled.

## Capabilities

### New Capabilities

None. This change removes execution surface and adds a verification invariant; it introduces no new
user-facing capability.

### Modified Capabilities

- `device-planning`: planning becomes the single site where imported code executes; plans carry
  generated artifact content; runtime management identity is resolved by substitution rather than
  re-render; and every imported interface method must be classified as exercised or refused.
- `direct-device-runtime`: preparation and connectivity helpers realize plan data instead of
  executing imported kind code, and imported post-deploy, readiness, save, and endpoint behavior is
  realized as plan-defined steps.
- `direct-runtime-conformance`: the hook-coverage inventory becomes part of the release gate, so an
  imported interface method that c9s never exercises is a reported incompatibility.

## Impact

- `internal/deviceplan`: adapter, post-deploy and readiness recorders, save, preparer, codec, and
  plan types (`SchemaVersion`, `FileSourceGenerator`, imported action kinds).
- `internal/directruntime`: imported application/endpoint runtime facade, runtime-CLI shims,
  lifecycle, readiness, and connectivity hook execution.
- `controllers/node`: plan artifact persistence and payload references; worker artifact garbage
  collection.
- `internal/conformance`: hook-coverage inventory and gate.
- `internal/directpod`: rendering of the flattened action set; no Pod-shape change is expected.
- Docs: `docs/architecture.mdx` and `docs/concepts/containerlab-differences.md` describe in-Pod hook
  execution today.
- No API/CRD change, no chart or RBAC change, no containerlab version bump. Existing e2e suites are
  the acceptance gate and are expected to pass unchanged.
