## Why

Direct Node reconciliation uses short-lived, content-addressed workers for image discovery and
device planning. Each attempt can create a worker Pod, NetworkPolicy, input ConfigMap, and
persisted output ConfigMap. Without an explicit retention contract, repeated reconciles leave
superseded attempts behind, increasing cluster object noise and making the planner appear to
repeat work indefinitely.

Steady-state reconciliation also needs to distinguish the current accepted workload input from
topology intent. A previously accepted input may contain image roles discovered by the package,
while a new topology declaration does not. Reusing that input is safe only after its declared
image references and complete input identity have been validated.

## What Changes

- Define bounded retention for direct planner and image-discovery attempts.
- Retain the successful attempts in the bounded discovery chain, the input mounted by the
  accepted workload, and in-flight attempts needed for retry or completion.
- Garbage-collect superseded worker Pods, NetworkPolicies, input ConfigMaps, and persisted output
  ConfigMaps using Node ownership and labels.
- Reuse a validated cold workload input, including discovery-derived certificates and image roles,
  as the image-discovery starting point so steady reconciles avoid an unnecessary discovery round.
- Fall back to the normal role-free topology seed whenever the cold workload is absent, foreign,
  incomplete, stale, or has a mismatched input digest.
- Add unit and conformance coverage for retention, ownership safety, cold-input validation, and
  fallback behavior.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `device-planning`: defines the lifecycle, retention, and validated reuse of worker attempts.
- `direct-runtime-conformance`: requires routine reconciliation cleanup without deleting unrelated
  resources.

## Impact

- `controllers/node` worker reconciliation, image discovery, direct reconciliation, and cleanup.
- Canonical device-planning and direct-runtime-conformance specifications.
- Unit tests for worker artifact garbage collection and cold-input matching.
- Runtime validation of repeated reconciles and superseded planner resources.
