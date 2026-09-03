## Why

With persistence enabled, a device Pod replacement silently reverts saved device configuration whenever the startup configuration is materialized as a plan-owned file (for example a full SR Linux JSON config): the preparation init container force-restages every planned artifact on every Pod start, overwriting the file the device saved. The same lab with a CLI-format startup configuration keeps saved state but silently ignores later startup-config updates. Which behavior a user gets depends on the format of their startup configuration, neither is controllable, and both diverge from containerlab's documented conf-artifacts contract (saved configuration wins on redeploy; `enforce-startup-config` opts out). Verified live on SR Linux: a config change committed and saved through the Save lifecycle survived a Pod restart with a CLI startup config, and was reverted to the startup config with a JSON startup config.

## What Changes

- Preparation gains persistence-aware staging: on a persistent artifact volume, a planned generated file that the device has modified since it was last staged is left in place instead of being overwritten. Staging decisions use recorded staging digests, keeping the behavior kind-agnostic. Infrastructure artifacts the device never wrote (topology files, certificates, repo files) continue to re-stage so spec updates still propagate.
- The containerlab `enforce-startup-config` node property becomes functional: when set, startup-config-derived artifacts re-stage on every Pod start (today's unconditional behavior becomes the opt-in).
- A device-state reset operation lets a user return one Node's persistent state to a freshly seeded startup configuration without deleting the Node resource or its claim.
- Persistence policy gains a claim retention setting so a PersistentVolumeClaim can outlive its Node, making Topology delete plus recreate equivalent to `containerlab destroy` plus `deploy` without `--cleanup`.
- Running the Save lifecycle against a Node without persistence enabled produces a visible warning that the saved configuration will not survive Pod replacement.
- Ephemeral behavior (persistence disabled) is unchanged: every Pod start reproduces the declared spec exactly.
- The persistence guide is corrected to describe the actual contract.

## Capabilities

### New Capabilities

- `device-state-persistence`: the contract for device-written state on persistent artifact volumes across Pod replacement: seed-once staging, device-written files win, `enforce-startup-config` re-seed opt-in, explicit device-state reset, claim retention, and the save-without-persistence warning.

### Modified Capabilities

- `device-planning`: preparation reproduction becomes persistence-aware; regenerated artifacts are still verified byte-equivalent against the plan, but publication onto a persistent volume must not overwrite files whose current digest differs from the digest recorded at their last staging.
- `node-profiles`: the persistence policy gains a claim retention setting and its resolution documents seed and enforce semantics.
- `direct-runtime-conformance`: conformance coverage must include saved-configuration survival across Pod restart, Pod replacement, and spec change, for both CLI-format and full-file startup configurations.

## Impact

- `internal/deviceplan/preparer.go` (staging policy, staged-digest ledger), `internal/deviceplan/runtime_recorder.go` and related digest recording, `internal/deviceplan/save.go` (warning path).
- `controllers/node/persistentvolumeclaim.go` and `apis/v1alpha1` persistence types (retention setting), with regenerated CRDs, deepcopy, and OpenAPI output via `make verify-generated`.
- `controllers/node` reset handling and events.
- `docs/guides/persistence.md` and the CRD reference pages.
- e2e coverage under `e2e/topology/direct/`.
- No breaking API change: existing Topologies and NodeProfiles keep their schema; the default behavior change (saved configuration surviving Pod replacement) applies only when persistence is enabled and matches the documented intent of that feature.
