## Why

Service exposure has two controls with the same effective disabled state: `disableExpose: true`
and `exposeType: None`. Keeping both creates unclear precedence, misleading documentation, and
unnecessary branches across the Topology, NodeProfile, compiler, and Node controller APIs.

## What Changes

- **BREAKING** Remove `disableExpose` from `Topology.spec.expose` and
  `NodeProfile.spec.expose`; manifests that need no expose Service must use
  `exposeType: None`.
- Make `exposeType` the single exposure-mode control, with `LoadBalancer` remaining the built-in
  default and `ClusterIP`, `Headless`, and `None` retaining their current behavior.
- Remove the redundant resolved-profile and reconciliation branches while preserving automatic
  port control through the separate `disableAutoExpose` field.
- Clarify that exposure policy controls only the per-Node expose Service; fabric and alias
  Services remain available when `exposeType` is `None`.
- Document direct `NodeProfile` configuration, built-in default resolution, accepted Service
  modes, and the required `disableExpose: true` to `exposeType: None` manifest migration.
- Add focused tests for every exposure mode, no-port suppression, and Service transitions.
- Regenerate API clients, OpenAPI, and CRDs from the changed source types.
- Keep the retiring clabverter out of feature scope; make only the mechanical adaptation required
  for its existing disable-exposure input to emit `exposeType: None` and keep the repository
  building until clabverter is removed.

## Capabilities

### New Capabilities

- `service-exposure`: Defines expose Service modes, default resolution, suppression semantics,
  Service-role boundaries, and the supported configuration surfaces.

### Modified Capabilities

- `node-profiles`: Removes the redundant NodeProfile disable boolean and makes `exposeType` the
  sole profile field controlling whether an expose Service exists.
- `topology-resource`: Removes the mirrored Topology disable boolean and requires compilation of
  `exposeType` into generated NodeProfiles.

## Impact

- Public `v1alpha1` Topology and NodeProfile schemas change; old manifests containing
  `disableExpose` are rejected by the regenerated structural schemas and must be updated before
  upgrade.
- API source types, profile resolution, Topology compilation, Node Service reconciliation,
  generated clients/OpenAPI/CRDs, tests, examples, and user documentation are affected.
- Existing resources that relied on `disableExpose: true` must be changed to `exposeType: None`
  before the new controller and CRDs are installed to avoid falling back to the default
  `LoadBalancer` exposure mode.
- No new Kubernetes Service type, Config-level exposure default, or per-Node Topology override is
  introduced.
