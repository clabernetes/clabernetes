## Context

Launcher policy is currently split between the reusable `LauncherProfile` API, the
`ResolvedProfile` used by the Node controller, and the launcher Deployment renderer. The
`Scheduling` API type is shared by `LauncherProfile.spec.scheduling` and
`Topology.spec.deployment.scheduling`, while Topology reconciliation materializes the latter as
generated LauncherProfiles before Nodes are reconciled.

The older affinity contribution added affinity to a topology scheduling block, but the current
architecture requires the policy to flow through LauncherProfile resolution. The feature is
intentionally launcher-scoped: there is no global Config affinity, kind/type lookup, or
selector-based profile assignment in this change.

## Goals / Non-Goals

**Goals:**

- Expose the native Kubernetes `corev1.Affinity` structure on the shared `Scheduling` API type.
- Apply a referenced LauncherProfile's affinity to every launcher Pod for its Nodes.
- Preserve primary-profile authority for grouped Nodes sharing one launcher Pod.
- Project Topology deployment affinity into generated shared and dedicated LauncherProfiles.
- Detect affinity changes, additions, and removals during Deployment reconciliation.
- Cover node affinity, pod affinity, and pod anti-affinity with deterministic Go unit tests and
  YAML/JSON comparisons.
- Document both Topology and LauncherProfile affinity entry points in an "Affinity Rules" section.

**Non-Goals:**

- Global Config affinity defaults.
- Affinity lookup by Containerlab kind, type, or image.
- Label- or selector-based assignment of LauncherProfiles.
- Per-secondary-Node affinity when Nodes share a launcher Pod.
- End-to-end or cluster-mutating tests.
- Merging multiple affinity objects from different policy layers.

## Decisions

### Reuse the existing Scheduling type

Add `Affinity *corev1.Affinity` to `apis/v1alpha1.Scheduling` with an optional,
`omitempty` JSON field. This exposes the same Kubernetes vocabulary at both supported API paths:
`LauncherProfile.spec.scheduling.affinity` and
`Topology.spec.deployment.scheduling.affinity`.

Using one shared field avoids two schemas that could diverge. A top-level LauncherProfile affinity
field was considered, but would make the profile shape inconsistent with the existing Topology
scheduling abstraction.

### Treat affinity as LauncherProfile policy

Add `Affinity *corev1.Affinity` to `node.ResolvedProfile`. Profile resolution starts with no
affinity because Config has no affinity default; a non-nil profile affinity is deep-copied into the
resolved policy. An omitted profile affinity therefore leaves the Pod without an affinity policy.

The native affinity object is replaced as a whole rather than merged field-by-field. Combining
required node terms or pod affinity terms from separate sources can unexpectedly make a Pod
unschedulable, and this change has no policy hierarchy that would require merging.

### Render and compare the PodSpec affinity

The Node Deployment renderer copies the resolved affinity to
`Deployment.spec.template.spec.affinity`. Deployment conformance compares the affinity structure
exactly, including transitions between nil and a configured object, so policy edits cause the
launcher Deployment to be updated.

The renderer and profile resolver use deep copies at API boundaries. This prevents generated
Kubernetes objects and test fixtures from sharing mutable affinity substructures with cached API
objects.

### Preserve Topology projection semantics

Topology profile rendering includes affinity in the condition that decides whether to emit a
scheduling block. It deep-copies the complete `Scheduling` value into the generated shared
LauncherProfile. Dedicated profiles already start from the complete shared policy, so they retain
the same affinity when they are emitted for distinct resource policy.

The direct `clabverter --emit-crs` path needs no separate policy conversion because it already
reuses `RenderLauncherProfiles`.

### Test rendered Pod affinity as data

Unit tests will construct representative native Kubernetes affinity values containing
`nodeAffinity`, `podAffinity`, and `podAntiAffinity`, render a launcher Deployment, and compare the
resulting affinity section to expected YAML/JSON fixture data. Separate tests will cover profile
resolution, Deployment conformance drift, affinity-only Topology scheduling, and retention on
dedicated generated profiles.

This validates the complete data path without requiring a scheduler or Kubernetes cluster.

### Document both launcher-scoped entry points

Add an "Affinity Rules" section to `docs/guides/resource-management.md` with native Kubernetes
examples for both `Topology.spec.deployment.scheduling.affinity` and
`LauncherProfile.spec.scheduling.affinity`. Update `docs/concepts/launcher-profiles.md` and the
scheduling example so readers understand that one LauncherProfile can be referenced by multiple
Nodes, while a Topology normally generates the shared profile automatically.

## Risks / Trade-offs

- **[Risk]** Nil versus empty affinity may be normalized differently by API serialization.
  **→ Mitigation:** preserve nil/non-nil pointers in the API and compare serialized fixture data
  where appropriate; test both configured and omitted cases.
- **[Risk]** A malformed affinity definition may be accepted by the CRD but rejected by the
  scheduler. **→ Mitigation:** use the Kubernetes-native typed structure and generated CRD schema;
  do not introduce an untyped YAML escape hatch.
- **[Risk]** Group members may expect different scheduling policies despite sharing one Pod.
  **→ Mitigation:** retain the existing primary LauncherProfile authority and grouped-reference
  conflict validation.
- **[Trade-off]** Users who need global or kind-based defaults must repeat profiles for now.
  **→ Mitigation:** keep the profile field independent so future policy layers can be added without
  changing the launcher Pod representation.

## Migration Plan

This is an additive API change. Existing profiles, Topologies, and generated manifests omit
`affinity` and retain their current scheduling behavior. Regenerate CRDs, deepcopy code, and
OpenAPI artifacts from the updated API source, then deploy the controller normally. Rollback is
safe by removing affinity from manifests and redeploying the controller; no stored-resource
migration is required.

## Open Questions

None for the initial launcher-scoped feature.
