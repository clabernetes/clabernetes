## 1. Simplify Exposure Policy

- [x] 1.1 Remove `disableExpose` from the Topology and NodeProfile source API types, remove it from
  resolved profile state, and verify source searches show no remaining controller or compiler
  branch on `DisableExpose`.
- [x] 1.2 Update Topology-to-NodeProfile rendering and direct Node reconciliation to use only
  `exposeType: None` for exposure suppression, and verify focused compiler and Node controller
  tests pass.
- [x] 1.3 Mechanically adapt the existing clabverter disable input to emit `exposeType: None`
  without adding new clabverter options or documentation, and verify its existing unit and golden
  tests pass.
- [x] 1.4 Regenerate deepcopy code, clients, OpenAPI, chart CRDs, and CRD assets from the source API
  changes, then run `make verify-generated` and confirm generated schemas contain no
  `disableExpose` property.

## 2. Lock Down Service Behavior

- [x] 2.1 Add table-driven tests for built-in/default, `LoadBalancer`, `ClusterIP`, `Headless`, and
  `None` exposure resolution and rendering, and verify the Node controller package tests pass.
- [x] 2.2 Add tests proving an enabled mode with no exposed ports creates no expose Service while
  `disableAutoExpose` with explicit ports still uses the selected Service mode, and verify the
  focused exposed-port and Service tests pass.
- [x] 2.3 Add tests proving `None` removes only the owned expose Service while fabric and alias
  Services remain reconciled, and verify the focused reconciliation tests pass.
- [x] 2.4 Add tests for ordinary ClusterIP-to-headless and headless-to-ordinary Service recreation,
  plus transitions to `None`, and verify the focused Service update tests pass.
- [x] 2.5 Update compiler assertions and fixtures so generated NodeProfiles carry only
  `exposeType`, including `None`, and verify compiler package tests pass.

## 3. Documentation And Examples

- [x] 3.1 Rewrite the service-exposure guide to describe all Service roles, all four exposure
  modes, the built-in LoadBalancer default, no-port behavior, and both Topology and direct
  NodeProfile configuration paths; verify internal links and examples are coherent on review.
- [x] 3.2 Replace user-facing `disableExpose` examples with `exposeType: None`, add the existing
  headless example to the exposure README table, and verify repository documentation and example
  searches contain no recommendation to use the removed field.
- [x] 3.3 Add prominent upgrade guidance requiring `disableExpose: true` to become
  `exposeType: None` before CRD/controller upgrade, explicitly warning about accidental fallback
  to LoadBalancer, and verify the migration command and before/after manifests are valid YAML.
- [x] 3.4 Correct API and conceptual wording that implies exposure always means LoadBalancer or
  that Config supplies an exposure default, and run `make check-docs`.

## 4. Validation

- [x] 4.1 Run `make test` and fix any non-cluster regression caused by the API and reconciliation
  changes.
- [x] 4.2 Run `make lint`, inspect formatter-generated changes, and verify only intended files are
  modified.
- [x] 4.3 Run `npx -y @fission-ai/openspec validate remove-disable-expose --strict` and confirm the
  proposal, design, delta specs, and completed task evidence satisfy strict validation.
