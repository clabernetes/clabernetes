# Tasks

## 1. Config API and bootstrap compatibility

- [x] 1.1 Add exact-kind and image-regexp resource maps to `ConfigDeployment` with complete API comments and OpenAPI-compatible value semantics; run `make verify-generated` and inspect only the expected generated changes.
- [x] 1.2 Extend bootstrap ConfigMap parsing plus fresh, merge, opt-in, and overwrite handling for both maps; add fresh/existing/nil/non-nil/empty/user-key preservation tests and pass `go test ./config/...`.
- [x] 1.3 Extend Helm values schema, ConfigMap rendering, and customized/default chart fixtures for the new maps while retaining nil-map upgrade behavior; pass the relevant chart test packages and inspect rendered ConfigMaps.

## 2. Global resource resolution

- [x] 2.1 Implement one global resolver that deep-copies the selected image, kind, generic, empty, or nil policy and never shares mutable ResourceRequirements state; pass table-driven copy/fallback/empty-map tests in `go test ./config/...`.
- [x] 2.2 Implement full-source-reference RE2 matching and exact declared-kind lookup, including source tags, no digest or auxiliary-image matching, and alias independence; pass matching tests in `go test ./config/...`.
- [x] 2.3 Reject invalid and multiply matching image expressions with deterministic sorted diagnostics, while allowing profile callers to bypass global resolution; pass failure and bypass tests in the resolver and Node packages.
- [x] 2.4 Extend the Config manager and fake manager to expose the resolver without losing their copy/concurrency guarantees; pass `go test ./config/...` and `go test -race ./config/...`.

## 3. Workload realization and reconciliation

- [x] 3.1 Resolve explicit-profile versus global precedence for each normalized plan primary and replace the single renderer resource option with per-primary-container policy; pass profile-priority, whole-block, partial-overlay, and empty-block tests in `go test ./controllers/node/... ./internal/directpod/...`.
- [x] 3.2 Verify mixed-kind/image grouped workloads resolve independently while non-primary/internal components retain imported requirements; pass grouped and component render tests plus an SR-SIM plan/render regression that establishes primary-card scope.
- [x] 3.3 Watch the singleton Config and enqueue all primary Nodes on add/update/delete without changing explicit profile selection; pass event-routing and global-value reconciliation tests in `go test ./controllers/node/...`.
- [x] 3.4 Run `go test -race ./controllers/node/... ./internal/directpod/...` and confirm invalid global matching configuration cannot mutate an existing Deployment.

## 4. Supported-kind request recommendations

- [x] 4.1 Audit native-container NOS requirements against the pinned containerlab sources/docs, add positive request-only defaults and aliases to Helm values, and record each value's provenance and baseline limitations in `docs/guides/resource-management.md`.
- [x] 4.2 Audit VM-backed kinds' documented/default vCPU and RAM settings, add requests with explicit QEMU/helper memory headroom and no limits, and document variant tuning plus source provenance.
- [x] 4.3 Review generic/lightweight and non-NOS registry kinds, add appropriate small requests or explicit documented exclusions only where direct workload realization is unsupported, and verify every inclusion/exclusion has a rationale.
- [x] 4.4 Add registry-driven coverage validation against parsed shipped values for every supported kind/alias, positive CPU/memory requests, and absent limits; pass the chart/default-data tests and verify a synthetic new-kind case fails without an entry or exclusion.

## 5. User-facing resource model documentation

- [x] 5.1 Rewrite the global resource guide sections with complete Config, image-regexp, kind, NodeProfile, and Topology examples; document the full precedence diagram, whole-block selection, empty blocks, exact aliases, full-reference matching, and ambiguity errors, then pass `make check-docs`.
- [x] 5.2 Document fresh-install versus upgrade opt-in behavior, overwrite cautions, VM request-versus-limit semantics, per-primary/component scope, and large-variant examples; reconcile the SR-SIM guide's primary-card/every-card wording with the new regression evidence and pass `make check-docs`.
- [x] 5.3 Update Config installation/reference prose and issue #385 with the final API and resolution choices, then verify all documented YAML is valid and links to the new defaults guidance.

## 6. Integration validation

- [x] 6.1 Run `make test`, `make verify-generated`, `make lint`, and `make check-docs` from the repository root; inspect all formatter/generator changes and record any relevant skipped checks rather than treating skipped e2e coverage as executed.

## 7. Cluster resource validation

- [x] 7.1 Add exact-kind e2e coverage that asserts SRL's shipped memory default in Deployment and running Pod specs, raises it above the default through global Config, and verifies convergence; pass the focused direct-runtime e2e test.
- [x] 7.2 Run the default CI e2e matrix through the root Make targets against the selected existing cluster and record pass/skip results plus any unavailable prerequisites.

## 8. CI failure remediation

- [x] 8.1 Reproduce the generated-format failure with Go 1.26.10, make `fmt` work in linked worktrees, and pass Go 1.26.10 `make verify-generated` and `make lint` against the corrected formatter and generated output.
- [x] 8.2 Add a regression for a schedulable SR-SIM default of 1 CPU/4Gi, update provenance and fixtures, pass `go test ./charts/clabernetes/tests/...`, and run licensed SR-SIM e2e with that kind default.
- [x] 8.3 Commit and push the CI remediation without rewriting history, then verify the GitHub lint and SR-SIM checks complete successfully.
