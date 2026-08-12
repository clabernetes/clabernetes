## Context

The Topology compiler is shared by the in-cluster compatibility controller and the clabverter
conversion path. Its input vocabulary is native Containerlab, while the emitted resources and
launcher pods support only a curated subset. Grouped Nodes share one launcher pod and one Docker
network namespace, so a primary-only readiness check can report a broken group as healthy.

## Goals

- Preserve the existing permissive conversion behavior for fields that can be omitted without
  making the emitted resources invalid.
- Fail before resource creation when the source cannot identify realizable c9s Nodes or Links.
- Make grouped generic readiness reflect the whole shared launcher.
- Reduce status-update conflicts without introducing a new status API.
- Give users actionable documentation for compatibility and readiness limits.

## Decisions

### 1. Separate lossy compatibility from impossible structures

`CompileTopology` retains warning behavior for unknown fields, host-side port pinning, management
network semantics, unusable labels, and link labels/vars, with the source path included in each
warning. `CompileTopologyWithOptions` exposes an error policy that aggregates those diagnostics and
sorts them by source path, line, code, and message. Pseudo-nodes, unresolved endpoints, special
host-network endpoints, unsupported explicit link types, and invalid `network-mode` group references
are errors under both policies. Explicit native `veth` and `host` link forms are rejected because
their structured endpoint semantics are not represented by the c9s Link API; brief endpoint syntax
remains supported.

The strict policy is intentionally a reusable library surface rather than a new CLI contract:
neither the in-cluster controller nor clabverter currently exposes a flag selecting it. Docs must
not imply that `clabverter` already has a strict mode.

### 2. Validate grouping as a topology-wide graph

The compiler validates that every `container:<primary>` reference names a valid DNS label and an
existing Node, then detects cycles. This complements the Node CRD's single-object validation and
prevents an invalid group from being emitted and discovered only after reconciliation.

### 3. Use Docker state for group-atomic readiness

After deployment, the launcher resolves each primary and secondary by the stable
`clab-node-name` Docker label, including stopped containers so a missing or stopped member is
observable. Each member must be running, not paused/restarting/dead, and healthy when its image
defines a Docker healthcheck. The shared status marker is healthy only when all members pass.
Configured TCP/SSH probes still run against the primary address only.

### 4. Retry status writes with a fresh resource version

Node and Topology status updates retry on Kubernetes conflicts by fetching the current object through
controller-runtime's uncached API reader before each attempt. If the desired status is already
present, the retry is a no-op. Successful updates copy the current status and resource version back
to the object used by the reconcile.

## Risks and Review Constraints

- Running without an image healthcheck remains process-level readiness; docs must recommend an
  image healthcheck or explicit TCP/SSH probe when service readiness matters.
- Docker label lookup with `--all` must not leave ambiguous duplicate containers for one node.
- Node and Topology status are controller-owned, so retries intentionally replace the full status
  with the controller's desired snapshot. If status ownership is ever split between controllers,
  this must become field-scoped merging rather than a full replacement.
- Compatibility-mode warnings now include source context, including link-label and link-variable
  paths.

## Verification

- Existing PR unit, lint, image-build, and e2e checks remain green.
- Add focused checks for duplicate/stopped Docker container discovery, grouped healthcheck and
  lifecycle transitions, deterministic diagnostics, conflict retries, and compatibility warning
  locations. Add a conflict test that changes stored status between retry attempts to verify the
  uncached reader obtains a fresh resource version before the intentional full-status replacement.
- Update architecture and release documentation, then run the docs build.
