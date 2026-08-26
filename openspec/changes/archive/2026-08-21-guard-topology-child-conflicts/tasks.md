## 1. API and generated artifacts

- [x] 1.1 Add an optional controller-owned `Error` string to `TopologyStatus` without changing child-resource naming or adding a prefix field.
- [x] 1.2 Regenerate OpenAPI, CRD, deepcopy, and related generated artifacts, then inspect the generated diff.

## 2. Conflict preflight

- [x] 2.1 Refactor Topology reconciliation to compile and render the complete NodeProfile, Link, and Node set before mutating child resources.
- [x] 2.2 Implement uncached namespace/name occupancy checks for every desired NodeProfile, Link, and Node, allowing only resources recognized as generated for the current Topology.
- [x] 2.3 Detect duplicate desired identities, sort all conflict entries deterministically, and format the namespace-aware error with the prescribed namespace and disambiguation guidance.
- [x] 2.4 Block all child reconciliation when conflicts exist, update Topology status without returning an `AlreadyExists` controller error, and requeue the blocked Topology.
- [x] 2.5 Clear the conflict error and resume normal dependency ordering, drift correction, pruning, and aggregate status reconciliation after a conflict-free preflight.

## 3. Tests and documentation

- [x] 3.1 Add tests covering Node, Link, and NodeProfile conflicts, including exact status formatting and deterministic ordering.
- [x] 3.2 Add tests proving conflicting Topologies create no partial child set, current-Topology children remain compatible, and resolved conflicts clear the error.
- [x] 3.3 Preserve and verify existing render-name and dependency-order behavior for conflict-free Topologies.
- [x] 3.4 Document the conflict status and the namespace/disambiguation remediation in the Topology concept or troubleshooting documentation.

## 4. Verification

- [x] 4.1 Run the focused Topology controller tests.
- [x] 4.2 Run generated-artifact verification and inspect all changes.
- [x] 4.3 Run the repository lint or general test target required by the touched Go and API files.
