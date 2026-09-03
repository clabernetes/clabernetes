## 1. Staging ledger and persistence-aware publication

- [x] 1.1 Add the per-Node staging ledger (read/write/update beside `runtime-artifacts.json`, acknowledged reset token included) in `internal/deviceplan`, with unit tests for missing, valid, and corrupt ledgers
- [x] 1.2 Implement the D1 publication rules in `Preparer.Prepare` for regular files and symlinks on persistent volumes (publish-if-missing, publish-if-untouched, preserve-if-device-modified, unconditional under enforce/reset), keeping plan verification unchanged
- [x] 1.3 Implement the D2 missing-ledger fallback (preserve differing files, establish ledger, report the condition) with unit tests covering the upgrade path
- [x] 1.4 Thread the `--persistent` flag from the Pod renderer through the `node-runtime prepare` CLI into the preparer, defaulting to today's unconditional behavior when absent
- [x] 1.5 Record skipped device-modified paths in preparation output and cover the skip reporting with unit tests

## 2. Enforce-startup-config

- [x] 2.1 Read `enforce-startup-config` from the node definition at planning, record it on the NodePlan, and reject enforce-without-startup-config with a structured invalid-input error; extend codec round-trip and validation tests
- [x] 2.2 Honor the plan flag in the publication rules and add preparer tests proving enforced re-staging overwrites device-modified files

## 3. Device-state reset

- [x] 3.1 Define the reset annotation, project it into the Pod template in the node controller, and surface acknowledgment via Node event and status; controller tests for annotate, replace, acknowledge, and idempotent re-reconcile
- [x] 3.2 Pass the reset token to `node-runtime prepare`, wipe the plan-owned artifact tree on token mismatch, stage fresh, and record the acknowledged token; preparer tests for first reset, repeated token, and fresh volume

## 4. Claim retention

- [x] 4.1 Add `reclaim` to the persistence types on NodeProfile and the Topology deployment block (`apis/v1alpha1`), run `make verify-generated`, and inspect regenerated CRDs, deepcopy, and OpenAPI output
- [x] 4.2 Implement Retain in `controllers/node/persistentvolumeclaim.go`: identity labels instead of owner reference, adoption of a compatible existing claim on Node creation, structured error on incompatibility; controller tests for delete/retain/adopt/incompatible paths

## 5. Save warning

- [x] 5.1 Record the persistence flag in the lifecycle input at render time and append the Save output warning for non-persistent volumes, with a unit test on the warning text

## 6. Validation and documentation

- [x] 6.1 Extend `e2e/topology/direct` with the saved-configuration survival matrix (CLI and full-file startup config, Pod delete and spec-change replacement, enforce, reset) and run it where a cluster is available
- [x] 6.2 Rewrite `docs/guides/persistence.md` to the new contract (saved wins, enforce, reset, retention, save warning) and update the CRD reference pages; run `make check-docs`
- [x] 6.3 Run `go test ./internal/deviceplan/... ./controllers/node/...`, then `make test` and `make lint`, and record results
