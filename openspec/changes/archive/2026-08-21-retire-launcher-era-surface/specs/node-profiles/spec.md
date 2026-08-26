# node-profiles — Delta Specification

The `launcher-profiles` capability is renamed to `node-profiles`; the CRD is renamed from
`LauncherProfile` to `NodeProfile` with `Node.spec.profileRef`, `Node.status.appliedProfile`,
and the `NodeProfileResolved` condition. The spec shape is unchanged.

## MODIFIED Requirements

### Requirement: Breaking alpha API migration is explicit and fail closed

The breaking direct-runtime release SHALL remove launcher, Docker, nested-CRI, per-kind c9s
policy, and Docker-management fields from NodeProfile, Config, and mirrored Topology sources in
one generated-schema boundary. It SHALL retain only fields with defined direct semantics and add
explicit global/profile Kubernetes pull-policy defaults. No removed field may be silently
retargeted to a device application container.

The upgrade is a documented clean cutover: the release SHALL NOT ship in-place migration or
preflight tooling, and the new structural schema SHALL reject removed and unknown fields after
the cut.

#### Scenario: Apply removed field after the cut

- **WHEN** a user applies a manifest containing an old launcher, Docker, CRI, kind-keyed, or
  Docker-management path to the new CRD
- **THEN** the API server rejects the unknown field rather than preserving or ignoring it
