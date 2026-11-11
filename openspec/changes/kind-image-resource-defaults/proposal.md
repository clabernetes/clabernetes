# Proposal

## Why

The installation-wide resource default is a single 200m/512Mi policy for every network OS, so it undersizes VM-backed devices and over-reserves lightweight devices. Users cannot reliably select global defaults by kind or image, while the current API reference does not define a complete resolution model or ship useful per-kind recommendations.

## What Changes

- Add global `Config.spec.deployment.resourcesByKind` and `resourcesByImage` resource-default maps alongside `resourcesDefault`.
- Resolve primary-container resources in this order: explicit NodeProfile, unique image-regexp match, exact kind match, generic default, then no additional policy.
- Validate image expressions and overlapping matches before workload mutation; preserve explicit empty resource blocks as suppression of lower-priority policy.
- Apply global defaults independently to each logical primary container in grouped workloads without replacing imported component requirements.
- Ship documented CPU and memory request recommendations for supported containerlab kinds and aliases, without default limits, and test coverage against the pinned containerlab registry.
- Reconcile Nodes when global resource configuration changes.
- Preserve existing NodeProfile and Topology override APIs and avoid automatically changing existing installations' effective defaults during bootstrap merge.
- Expand the Config and resource-management documentation with examples, precedence, matching, upgrade, variant, and component-container semantics.

## Capabilities

### New Capabilities

- `device-resource-defaults`: Defines installation-wide and per-profile resource-default selection, image and kind matching, supported-kind request recommendations, safe upgrade behavior, and workload realization.

### Modified Capabilities

None. Existing NodeProfile and Topology policy remains the explicit override mechanism; the new capability owns global resource-default behavior.

## Impact

This affects the Config API and generated CRDs/clients, global Config bootstrap and management, Node reconciliation and direct-Pod rendering, Helm values and fixtures, supported-kind default coverage tests, and resource/configuration documentation. It builds on the pinned containerlab registry and documented kind requirements without adding a new dependency. Issue #385 is addressed with regexp rather than glob matching and with deterministic ambiguity handling.
