## Context

See `proposal.md` for motivation. Today both Topology and NodeProfile expose a disable boolean in
addition to the `None` enum value. Topology compilation copies both fields into generated profiles,
profile resolution carries both into runtime policy, and two independent Node reconciliation paths
short-circuit on either value. The public APIs are `v1alpha1`, generated schemas reject unknown
fields, and the repository's established breaking-API policy favors an explicit clean cutover over
silent reinterpretation.

The Topology CRD defaults an omitted `exposeType` to `LoadBalancer`. Direct Nodes may omit a
NodeProfile entirely, in which case profile resolution supplies the same built-in default. The
global Config CR has no exposure-mode field. Clabverter is planned for retirement but currently
compiles against the shared Topology Go type, so removing the field requires a small mechanical
adaptation even though no clabverter feature work is intended.

## Goals / Non-Goals

**Goals:**

- Represent expose-Service state with one enum across source APIs, generated profiles, and runtime
  reconciliation.
- Preserve current behavior for all four `exposeType` values and for `disableAutoExpose`.
- Make the breaking migration explicit enough to prevent accidental LoadBalancer exposure.
- Keep generated schemas and user documentation aligned with the source API.

**Non-Goals:**

- Add `NodePort`, `ExternalName`, or another Kubernetes Service type.
- Add exposure defaults to the global Config CR.
- Add per-Node exposure overrides to the Topology API.
- Change fabric or alias Service behavior.
- Redesign, extend, or document clabverter ahead of its removal.
- Provide an in-place conversion webhook or retain a compatibility alias for `disableExpose`.

## Decisions

### 1. Remove both public disable fields in one schema boundary

Delete `Topology.spec.expose.disableExpose` and `NodeProfile.spec.expose.disableExpose` together,
then regenerate all derived API artifacts. Keeping the field on one API would leave two policy
models and force the compiler or resolver to retain conversion logic indefinitely.

Alternative considered: deprecate the fields for one release. This is safer for stable APIs but
retains the ambiguity and is inconsistent with the repository's existing clean-cut approach for
the current breaking `v1alpha1` API transition.

### 2. Preserve `None` as an explicit enum value

`None` remains a valid `exposeType` alongside `LoadBalancer`, `ClusterIP`, and `Headless`. Runtime
port allocation and Service rendering use only this value to suppress exposure. The separate
`disableAutoExpose` field remains because it controls port selection rather than Service mode.

Alternative considered: make an empty `exposeType` mean disabled. This conflicts with the current
built-in LoadBalancer default and makes omission indistinguishable from intentional suppression.

### 3. Keep default resolution at the existing boundaries

The Topology schema continues defaulting an omitted value to `LoadBalancer`, while direct profile
resolution retains its built-in LoadBalancer fallback for a missing profile or an omitted profile
field. No Config field is added. Generated Topology profiles copy only `exposeType` for Service
mode.

Alternative considered: add a global Config exposure default. That expands scope, changes cluster
policy semantics, and is unnecessary to remove the duplicate field.

### 4. Use a clean manifest migration rather than runtime compatibility

Before installing the changed CRDs and controller, users must replace every effective
`disableExpose: true` with `exposeType: None`. Removed fields are rejected by the new structural
schemas. No controller fallback reads unstructured legacy data.

This ordering is security-critical: an existing direct NodeProfile that only sets
`disableExpose: true` otherwise resolves to the built-in LoadBalancer default after the field is
removed. Topologies commonly persist `exposeType: LoadBalancer` through CRD defaulting even while
the old boolean wins, so those resources also require an explicit change to `None`.

Alternative considered: preserve an internal deprecated field while hiding it from documentation.
That would not actually simplify the schema or resolution model and would continue accepting old
manifests silently.

### 5. Limit clabverter work to compilation compatibility

The existing clabverter disable input remains accepted temporarily, but its output sets
`exposeType: None` instead of the removed Topology boolean. Any expected-output changes required by
that mapping are mechanical. No new expose-type option or documentation is added.

Alternative considered: leave clabverter untouched. This is not buildable because it accesses the
removed Go field. Removing clabverter in this change would combine two independently reviewable
breaking changes and is outside the selected scope.

### 6. Test behavior at resolution and rendering boundaries

Focused tests cover built-in/profile resolution, each Service-producing mode, `None`, empty port
allocations, preservation of internal Service roles, and ordinary/headless transition behavior.
Compiler tests assert generated profiles contain the selected enum without a disable boolean.
This targets the shared boundaries rather than duplicating tests across every caller.

## Risks / Trade-offs

- [Existing disabled manifests can become externally exposed if not migrated] -> Put the required
  pre-upgrade replacement prominently in migration and release documentation; test the resulting
  `None` path.
- [Removed fields make old declarative sources fail to apply] -> Treat rejection as intentional
  fail-closed behavior and provide an exact one-line field replacement.
- [Generated artifacts can drift from source types] -> Run `make verify-generated` and inspect all
  regenerated CRDs, clients, and OpenAPI output.
- [Service transition tests may miss API-server immutability] -> Retain explicit recreation tests
  for ordinary-to-headless and headless-to-ordinary transitions.
- [Mechanical clabverter changes prolong a retiring surface] -> Change only its mapping and
  affected assertions; do not add new flags or user documentation.

## Migration Plan

1. Before upgrade, replace `disableExpose: true` with `exposeType: None` in every Topology and
   directly authored NodeProfile manifest and apply those updates while the old controller still
   supports both fields.
2. Verify disabled Nodes have no expose Service while their required fabric and alias Services
   remain.
3. Install the regenerated CRDs and controller containing the simplified API.
4. Confirm old manifests containing `disableExpose` are rejected and all migrated resources retain
   the intended exposure mode.

Rollback requires reinstalling the prior CRDs and controller. Manifests migrated to
`exposeType: None` remain valid under the prior release, so the removed boolean does not need to be
reintroduced into declarative sources during rollback.
