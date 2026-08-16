## Why

Containerlab component nodes now work through the baseline implementation from PR #296, but
review identified correctness gaps before the behavior is treated as complete: conflicting shared
payloads are silently resolved by first-wins ordering, component namespace references are not fully
validated, and the reported `components: []` SR-SIM topology is not covered by automated tests.
The endpoint parser and documentation also need boundary-case coverage and cleanup before merging
the change with the current `main` branch.

## What Changes

- Validate component discovery as a complete namespace graph: require one owner, verify dependent
  namespace references, and fail safely on malformed or inconsistent Docker metadata.
- Reject conflicting grouped payload attachments that target the same normalized destination with
  different sources or file modes, while retaining one mount for identical shared content.
- Add regression coverage for the original SR-SIM `components: []` topology, relative and absolute
  license paths, component readiness, owner-based application probes, and Docker discovery.
- Harden explicit `veth` endpoint handling: accept brief and structured forms, reject malformed or
  empty endpoints consistently, and remove the stale unreachable `veth` diagnostic branch.
- Add parser, compiler, Deployment-rendering, and launcher tests for malformed endpoints, duplicate
  mounts, conflicting payloads, and component failure states.
- Update architecture, SR-SIM, and launcher documentation to describe the validated ownership and
  shared-payload rules, and resolve the documentation-spec merge with the current containerd
  registry-hosts requirements from `main`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `node-lifecycle`: Component discovery must validate namespace ownership, and grouped shared
  payload destinations must not silently combine different content.
- `topology-resource`: Explicit `veth` endpoint inputs must have consistently validated,
  representable node/interface values.
- `documentation-site`: Documentation must describe component ownership, shared-payload
  validation, endpoint boundaries, and the supported SR-SIM topology forms.

## Impact

The change affects launcher Docker inspection and readiness, grouped Node Deployment volume
rendering, Containerlab endpoint parsing and compilation, focused and integration tests, and the
architecture/SR-SIM documentation. It introduces no CRD schema, API version, or dependency
changes. Invalid ambiguous configurations that were previously accepted with first-wins behavior
will instead fail visibly.
