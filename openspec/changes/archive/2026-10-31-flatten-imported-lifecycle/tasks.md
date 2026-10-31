## 1. Coverage guard (no behavior change)

- [x] 1.1 Add a reflect-based inventory in `internal/deviceplan` that enumerates every method of
      `nodes.Node`, `nodes.NodeOverwrites`, and any other imported interface the boundary
      type-asserts, keyed by interface and method name
- [x] 1.2 Commit the classification table (`exercised`, `refused`, `unused`) with a reason and
      affected kinds for every non-`exercised` entry, starting with the currently uninvoked
      `PreDestroy`
- [x] 1.3 Add verification that fails on an unclassified method and on an `exercised` entry the
      adapter no longer invokes, in the style of `internal/deviceplan/kind_agnostic_test.go`
- [x] 1.4 Report the classifications from `internal/conformance` and mark kinds that depend on a
      `refused` method as incompatible for that behavior
- [x] 1.5 Record the inventory in the change directory as the work list for sections 2 and 3

## 2. Plan-borne artifact content

- [x] 2.1 Add the plan-artifact source to `internal/deviceplan/types.go` (path, mode, ownership,
      xattrs, digest, bytes reference) and declare a per-plan total size bound
- [x] 2.2 Capture generated artifact bytes and metadata during planning in
      `internal/deviceplan/adapter.go`, replacing digest-only `GeneratedArtifact` metadata
- [x] 2.3 Persist plan artifacts from the controller as digest-named, owner-referenced objects,
      Secret-backed for certificate/key material, and garbage-collect superseded artifacts
- [x] 2.4 Resolve resolver DNS in the manager (cacheable), add it to the planning input, and drop
      `RuntimePodDNSServers`, `completeRuntimeManagement`, and `renderRuntimeManagementArtifacts`
- [x] 2.5 Delete `FileSourceGenerator` handling and the in-Pod evaluation path from
      `internal/deviceplan/preparer.go`: registry, `evaluateInWorkspace`, entropy sessions, runtime
      re-render, and the `runtime-artifact-digests` record
- [x] 2.6 Bump `SchemaVersion`, update `internal/deviceplan/codec.go` and plan validation, and keep
      the existing staging-ledger and device-modified-file scenarios passing
- [x] 2.7 Add tests: plan bytes are byte-identical across repeated plans, size bound rejects a plan,
      sensitive artifacts never render as ConfigMap payloads, prepared content equals plan content

## 3. Remove in-Pod deployment replay

- [x] 3.1 Replace the replay-then-verify constructor with a construction recorder that hands the Node
      graph to the live runtime, and delete the deployment-stream verification
      (`verifyReplayedDeploymentOperations`, `sameReplayedDeploymentOperation`,
      `replayedDeploymentOperation`, `deploymentReplayError`, `verifyReplayedContainers`,
      `verifyReplayedReadinessInventory`)
- [x] 3.2 Prove the hook boundary no longer drift-checks the accepted plan, and keep the
      plan-append/post-deploy coverage test
- [x] 3.3 Record why hook flattening is out of scope: observation-driven control flow (srl `Ready`),
      direct device I/O in 43 kind packages, and the deny-all planning worker egress
- [x] 3.4 Update the change's specs and design to the re-scoped contract
- [x] 3.5 Run `go test ./internal/... ./controllers/...`, `make test`, `make test-race`, `make lint`,
      and `make verify-generated`
- [x] 3.6 Run `make test-e2e` against the selected cluster and record passed/failed/skipped counts
## 4. Confirm the remaining imported surface is required

- [x] 4.1 Keep `importedApplicationRuntime`, the runtime facade, and the runtime-CLI shim: the
      observation-dependent hook boundary still needs them
- [x] 4.2 Confirm no non-test file uses the removed replay helpers or the retired alternate-digest
      record
- [x] 4.3 Re-check the coverage inventory after group 3, so no method is classified by a call site
      that no longer exists

## 5. Validation and documentation

- [x] 5.1 Run `go test ./internal/... ./controllers/...`, `make test`, and `make test-race`
- [x] 5.2 Run `make lint` and inspect formatter changes; run `make verify-generated`
- [x] 5.3 Run `make test-e2e-local` for the suites enabled in `.github/workflows/e2e.yaml` and record
      passed/failed/skipped counts, including whether SR-SIM ran
- [x] 5.4 Update `docs/architecture.mdx`, `docs/concepts/containerlab-differences.md`, and
      `docs/guides/persistence.md` to describe plan-borne generation and the no-replay hook boundary
- [x] 5.5 Run `make check-docs` and `make build-docs`
