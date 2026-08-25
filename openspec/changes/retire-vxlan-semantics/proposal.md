# Proposal: retire-vxlan-semantics

## Why

The fabric wire replaced VXLAN as the cross-Pod Link transport, but the surrounding semantics
still encode the VXLAN era: Link tunnel IDs are allocated cluster-wide (with an uncached
cluster-wide List on every Link reconcile) because they used to be VNIs in shared worker host
namespaces, the management mesh VNI is kept above the Link-ID ceiling to avoid a collision that
can no longer occur, and the API, plan schema, Services, error messages, specs, and docs still
speak VXLAN. This is a clean breaking release — the right moment to scope the identifier
correctly, delete migration code for an implementation that never shipped, and make every name
and claim match the wire that actually exists.

## What Changes

- **BREAKING** `Link.status.tunnelID` is renamed to `Link.status.wireID` and re-documented as
  the wire's link identifier (not a VNI). The API group is unreleased, so nothing external can
  depend on the old name.
- Wire-ID allocation becomes namespace-scoped: the wire validates the source Pod and dispatches
  IDs inside one receiving sidecar, so uniqueness within the namespace is sufficient. The
  uncached cluster-wide Link List in the Link controller is removed.
- The management mesh VNI no longer needs to sit above the Link-ID ceiling: the wire (UDP
  14790, userspace) and the mesh (kernel VXLAN, UDP 14789) cannot collide by construction. The
  mesh VNI derives from the namespace over the full 24-bit VNI space.
- VXLAN vocabulary is retired where it no longer describes reality: the plan connectivity value
  `vxlan` becomes `wire`, the per-node fabric Service suffix `-vx` becomes `-wire`, "VXLAN
  Link" validation errors, VNI-flavored constants and comments, and the stale pre-wire VTEP
  sweep (migration code for an unreleased implementation) are all removed or renamed. The
  management mesh keeps its VXLAN vocabulary — it genuinely is kernel VXLAN.
- Claims are tightened to what the wire delivers: the "any cluster" MTU claim gains its real
  floor (underlays below ~1250 bytes fall back to outer IP fragmentation), the carrier claim
  becomes "same socket and path" rather than "can never disagree", and VLAN transparency is
  pinned by a spec scenario, an e2e test, and headroom for double-tagged frames.
- The management contract consistently says **namespace**: the implementation derives one mesh
  VNI and one L2 domain per namespace, and the specs/docs stop alternating between "topology"
  and "namespace".
- Docs that still describe VXLAN Link transport (installation, 0.9 release notes, Nokia SR-SIM
  guide) are corrected.
- Inbound management connections translated at the Pod boundary are additionally
  source-translated to the Pod-local gateway: real-cluster validation showed SR OS holds only
  its connected management route (it derives routes from a Docker-shaped environment a Pod
  does not present), so LoadBalancer/ClusterIP/Pod-IP management access silently black-holed
  while on-subnet probes passed. With the gateway as the flow source, every device can answer
  over its connected route — the same client identity containerlab's Docker port publishing
  presents.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `link-lifecycle`: the allocation requirement becomes the wire identifier, unique within the
  Link's namespace (not the cluster).
- `direct-connectivity`: the cross-Pod wire requirement gains the underlay-MTU floor boundary
  and a tagged-frame (VLAN/QinQ) transparency scenario; the management identity requirement
  states the namespace as the mesh scope; the boundary-translation requirement gains the
  gateway source translation for inbound flows.
- `management-mesh`: the mesh scope is the namespace (multiple Topologies in one namespace
  share one management L2 domain); the mesh identifier requirement drops disjointness from
  Link IDs and instead requires deterministic namespace derivation within the VNI space, with
  wire/mesh non-collision guaranteed by construction (different planes and ports).

## Impact

- `apis/v1alpha1/link.go` (field rename + docs) and everything generated from it: deepcopy,
  CRDs, `assets/crd/`, generated clients, openapi, docs CRD pages.
- `controllers/link` (allocation scope, comments, status message), `controllers/node`
  (plan-input connectivity value, mesh VNI derivation, fabric Service suffix).
- `internal/deviceplan` (connectivity value, field rename) — changes plan bytes, so
  compatibility invalidation digests refresh and recorded conformance evidence is invalidated
  by design.
- `internal/directruntime` (plan consumption, error strings, VTEP-sweep removal, frame
  headroom, comment wording).
- e2e golden fixtures (Service names, Link status fields) and a new VLAN-transparency e2e.
- Specs listed above; docs: `installation.mdx`, `release-notes/0.9.mdx`,
  `guides/nokia-srsim.md`, `guides/link-wire.md`, CRD reference pages.
