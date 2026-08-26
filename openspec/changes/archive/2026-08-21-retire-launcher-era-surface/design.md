# Design: Retire the launcher-era surface

## Context

0.9 ships as one breaking cut with a documented full uninstall/reinstall. Anything half-kept in
this release becomes permanent surface: a CRD name users would learn, a selector field users
would set, compat code that only serves upgrade paths the docs forbid.

## Decisions

**D1 — Rename now, once.** `NodeProfile` replaces `LauncherProfile` while every user already
must touch their manifests. The reference field is `profileRef` (on a Node, the profile kind is
implied), the applied status is `appliedProfile`, and the condition is `NodeProfileResolved`.
Internal group-primary vocabulary (`ResolveLauncherNode`, `launcherSelectorLabels`,
`enqueueLaunchers*`, `IsSameLauncherLink`) renames to primary/pod terms in the same pass since
it describes a different live concept (the Node owning a shared Pod), not the profile.

**D2 — One realization means no selector.** With slurpeeth retired to an alias of the VXLAN
realization, `connectivity` selected nothing while still constraining behavior (a uint16 VNI
clamp, a dead TCP/4799 fabric port) and shipping false CRD prose. The field is removed rather
than frozen at a single value; a future second transport should design its own selector.

**D3 — Zero-compat is policy, enforced.** The clean-cutover documentation is authoritative:
in-place migration machinery (`upgrade-preflight`, 0.6.x object cleanup, legacy PVC adoption,
daemon-era finalizer stripping, migration-era status fields) is deleted, not conditionally
kept. The dev installer's API-group guard stays: it enforces the cutover loudly rather than
migrating across it.

**D4 — Specs follow terminology wholesale.** The `launcher-profiles` capability is renamed to
`node-profiles`; other specs replace launcher-era vocabulary (launcher node → primary node,
launcher pod → device pod) without semantic change, so delta specs carry only the semantic
edits and the rename is applied as terminology during sync.

## Risks / Trade-offs

- [Users familiar with the LauncherProfile name] → one-line manifest edit inside an already
  mandatory rework; docs carry the rename prominently in the upgrade guide and release notes.
- [Old manifests setting `connectivity`] → fail CRD validation with a named field; the upgrade
  guide instructs removal.

## Migration Plan

Folded into the existing 0.9 clean cutover; no separate step.
