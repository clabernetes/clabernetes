## Why

Launcher Pods sometimes need Kubernetes node, pod affinity, or pod anti-affinity rules to run on
appropriate infrastructure or spread across failure domains. Clabernetes already supports reusable
launcher profiles and topology-wide scheduling policy, so affinity should be added to that shared
launcher-scoped policy instead of introducing global or selector-based policy assignment.

## What Changes

- Add native Kubernetes `Affinity` configuration to `LauncherProfile.spec.scheduling`.
- Apply one profile's affinity to every launcher Pod created for Nodes referencing that profile.
- Preserve primary-profile authority for grouped Nodes that share one launcher Pod.
- Add the equivalent topology-wide field at `Topology.spec.deployment.scheduling.affinity`.
- Copy topology affinity into generated shared and dedicated LauncherProfiles, including affinity-only
  scheduling blocks.
- Render affinity into launcher Deployments and detect affinity drift during reconciliation.
- Add Go unit tests that compare rendered affinity structures with expected YAML/JSON fixtures,
  including node affinity, pod affinity, and pod anti-affinity.
- Regenerate API artifacts and add an "Affinity Rules" section to the documentation explaining
  both Topology and LauncherProfile configuration, with matching scheduling/profile examples.
- Do not add global Config affinity, kind/type affinity lookup, selector-based profile assignment, or
  end-to-end coverage in this change.

## Capabilities

### New Capabilities

### Modified Capabilities

- `launcher-profiles`: Launcher profiles gain launcher-Pod affinity policy and apply it to all
  referencing launcher workloads.
- `topology-resource`: Topology deployment scheduling can express affinity that is preserved in
  generated LauncherProfiles.

## Impact

- API types in `apis/v1alpha1`, generated CRDs, deepcopy code, and OpenAPI artifacts.
- Launcher profile resolution and Node deployment rendering/conformance.
- Topology LauncherProfile rendering and direct CR manifest generation through the shared pipeline.
- Unit tests, scheduling examples, and launcher-profile/resource documentation.
