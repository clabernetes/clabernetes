## Why

PR #290 adds runtime and compiler behavior without an OpenSpec change record. The current
compiler can accept native Containerlab input while silently losing fields, and grouped launcher
readiness only observes the primary nested container. Concurrent Node and Topology status writes
also collide during rapid reconciliation. The latest PR commit narrows explicit link compatibility,
adds source locations to warnings, and uses uncached API reads when retrying status writes.

## What Changes

- Define warning and strict compiler policies with deterministic structured diagnostics.
- Continue warning for lossy compatibility fields with source locations, but reject structures that
  cannot produce valid c9s resources under every policy.
- Validate pseudo-nodes, special or unresolved link endpoints, invalid launcher-group network modes,
  and explicit unsupported link types before emitting resources. Explicit native `veth` and `host`
  link forms are rejected because their structured endpoints and semantics are not represented by
  the c9s Link API; brief endpoints remain supported.
- Make grouped launcher readiness atomic across every nested Docker container, including Docker
  lifecycle and image-healthcheck state; keep application TCP/SSH checks on the primary node.
- Retry Node and Topology status writes after resource-version conflicts using direct API reads
  rather than a potentially stale informer cache.
- Document compatibility warnings and failures, grouped readiness, healthcheck behavior, and the
  limitations of process-level readiness.

## Capabilities

### Modified Capabilities

- `topology-resource`: compilation exposes compatibility diagnostics and rejects structurally
  unrealizable source definitions.
- `node-lifecycle`: grouped Nodes share an all-members generic readiness result.
- `documentation-site`: public docs describe the new compiler and readiness contracts.

## Impact

The change affects the topology compiler, Node and Topology reconcilers, launcher Docker inspection,
compiler and controller tests, OpenSpec requirements, and the architecture/release documentation.
No new dependency or CRD field is required. The strict compiler policy is a library contract; this
repository currently has no user-facing CLI flag that selects it. The current PR still contains no
user-facing documentation files, so the documentation work remains outstanding.

## Review Status

The latest PR head passes lint, unit, e2e, try-smoke, and image checks. The uncached retry path and
warning locations are covered by regression tests. Status replacement is intentionally full-object
because Node and Topology status are controller-owned; the remaining verification is a conflict
test that changes the stored object between retry attempts and proves the fresh resource version is
used before replacement.

## Non-goals

- Supporting native host networking, bridge pseudo-nodes, `mgmt-net`, macvlan, or unsupported link
  types inside c9s.
- Inferring application readiness from ports, device kinds, or credentials.
- Adding structured readiness failure reasons to Node status.
