# Proposal

## Why

Containerlab topologies that assign `ipv4` or `ipv6` addresses beneath `topology.links` currently fail clabernetes compilation, as reported in #355. The direct Link API and device-planning path also have no way to preserve those per-endpoint addresses, so accepting the YAML alone would only move the semantic loss downstream.

## What Changes

- Accept containerlab link-level `ipv4`/`ipv6` arrays and structured veth-endpoint `ipv4`/`ipv6` fields, validating their CIDR values and mapping them onto the corresponding direct Link endpoints.
- Add optional per-endpoint `ipv4` and `ipv6` CIDR fields to the Link API and carry them through rendering, normalized planning inputs, accepted plans, and imported-node reconstruction.
- Supply the planned prefixes to containerlab's generic endpoint objects so the pinned, unmodified kind implementations configure them during initial planning and cold start. Do not add c9s kind-specific configuration commands or generic sidecar address assignment.
- Require recreation of an already-realized affected Node whenever its effective endpoint address set is added, changed, or removed, even when that kind normally permits Live link changes. Initial cold start needs no additional recreation, and unrelated MTU or structural-link lifecycle behavior remains unchanged.
- Document the recreation limitation and preserve existing persistence behavior: recreation does not implicitly erase saved device state; existing enforced-startup and explicit device-state reset controls still govern re-seeding.
- Add compiler, API-validation, planning, lifecycle, generated-resource, and real-device coverage for address preservation and recreation.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `topology-resource`: native containerlab link-address vocabulary must compile into equivalent direct Link resources in both supported endpoint syntaxes, with invalid values rejected deterministically.
- `link-lifecycle`: Link endpoints gain validated optional IPv4/IPv6 prefixes, and changes to a realized Node's endpoint address set require recreation of that Node.
- `device-planning`: endpoint addresses become explicit deterministic inputs and accepted-plan data supplied to imported containerlab endpoints during planning and hook reconstruction.
- `direct-connectivity`: endpoint-address changes are excluded from retained Live connectivity revisions and instead take the existing recreation path.

## Impact

- **API and generated artifacts:** `apis/v1alpha1/link.go` plus regenerated deepcopy, OpenAPI, client, and CRD outputs; this is additive and not a breaking API change.
- **Compilation and rendering:** `compiler/links.go`, compiled endpoint types, and Link rendering.
- **Controllers and planning:** direct-Link validation, `controllers/node/planinput.go`, `internal/deviceplan` schemas/codec/adapter/mapper, and connectivity transition evaluation.
- **Runtime behavior:** no new node-resident component, live command executor, or dataplane transport. Only the controller's existing recreate lifecycle is broadened to cover address changes.
- **Documentation and tests:** Link/Topology documentation and focused unit, integration, and direct-runtime e2e coverage. Device-state persistence semantics and the pinned containerlab dependency remain unchanged.
