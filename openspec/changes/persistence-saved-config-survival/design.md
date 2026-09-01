# Design: persistence-saved-config-survival

## Context

See proposal.md for motivation. The mechanics that shape the approach:

- The preparation init container (`node-runtime prepare`, `internal/deviceplan/preparer.go`) re-runs on every Pod start. It regenerates all planned artifacts in scratch, verifies them byte-equivalent against the accepted plan, then publishes each one over the destination with an unconditional rename (`stageArtifactContent`). Nothing consults the destination's current content.
- Containerlab's own contract (`nodes/default_node.go GenerateConfig`, `nodes/srl/srl.go PostDeploy`) is: an existing config file in the lab directory wins; `enforce-startup-config` opts out. c9s renders in fresh scratch, so the file never pre-exists there and both behaviors are lost: full-file startup configs clobber saved state, and `enforce-startup-config` is a no-op.
- A per-Node digest record already lives at the artifact-volume node root (`runtime-artifacts.json`), outside every package-declared mount subpath, in the same trust domain as the staged bytes. Device containers mount only the kind-declared subpaths and cannot reach the node root.
- Device Pods carry no service-account token; nothing in a device Pod may need Kubernetes API access.
- PVCs are named after the Node and owner-referenced to it (`controllers/node/persistentvolumeclaim.go`), so Node deletion garbage-collects the claim.
- Whether the artifact volume is a PVC or an emptyDir is decided by the resolved profile at Pod render time (`internal/directpod/renderer.go`).

## Goals / Non-Goals

**Goals:**

- Containerlab conf-artifacts parity when persistence is enabled, independent of startup-config format.
- Keep the preparation integrity proof intact: every planned artifact is still regenerated and verified against the plan digest before any publication decision.
- Keep c9s kind-agnostic: no kind-specific knowledge about which file is "the config".
- Safe upgrade for existing persistent volumes that predate the staging ledger.

**Non-Goals:**

- Changing imported kind behavior (for example SR Linux PostDeploy short-circuiting its CLI overlay when a saved config exists; that is containerlab semantics and stays).
- Auto-save on Pod termination.
- Config export/backflow into ConfigMaps or Git.
- Multi-attach or shared claims; the claim stays one per Node, ReadWriteOnce.

## Decisions

### D1: Staging ledger, not kind knowledge

Preparation writes a per-Node `staging-ledger.json` beside `runtime-artifacts.json` at the node artifact root, recording for every published regular file and symlink its artifact path and published content digest (for divergent runtime-rendered files, the runtime digest). The publication rule per planned file on a persistent volume:

1. Destination missing: publish, record digest.
2. Destination digest equals the ledger digest: the device never touched it; publish (propagates plan changes and re-asserts metadata), update ledger.
3. Destination digest differs from the ledger digest: device-written; leave the file, keep the ledger entry, record the skip in preparation output.
4. Node definition declares `enforce-startup-config`, or a reset is being honored: publish unconditionally, update ledger.

Directory artifacts are always ensured (mode/ownership), never treated as device-modified. On non-persistent volumes the rule is unchanged (unconditional publish); the ledger is still written so behavior is uniform and cheap.

Alternative considered: mark "seed-class" artifacts at planning (files derived from startup config) and skip only those when present. Rejected: deriving that classification generically is guesswork per kind, and it reintroduces exactly the format-dependent asymmetry this change removes. The ledger rule needs no classification and is strictly closer to the containerlab contract.

Alternative considered: skip any existing file (containerlab's literal rule). Rejected: spec updates would then never propagate even for files the device provably never modified (topology.yml, repo files, certificates), which breaks reconciliation of declared intent.

### D2: Missing ledger preserves, never clobbers

A persistent volume holding prior artifacts but no readable ledger (upgrade from a pre-ledger release, or a corrupted ledger) is treated conservatively: planned files whose content differs from the plan are treated as device-modified and preserved; matching files establish ledger entries. Data loss is strictly worse than a stale spec, and the very next preparation run has a full ledger. The condition is reported in preparation output.

### D3: Persistence signal is a render-time flag

The renderer knows whether the artifact volume is a PVC. It passes `--persistent` to the prepare init container. Preparation never inspects volume plumbing itself.

### D4: `enforce-startup-config` is planned data

The planner reads `enforce-startup-config` from the node definition and records it on the NodePlan. Planning rejects `enforce-startup-config` without a startup configuration with a structured invalid-input error, mirroring containerlab's `ErrNoStartupConfig`, before any workload exists. Preparation consults only the plan flag. Enforce applies to all planned files of that node: in practice the only device-modified planned files are startup-config-derived, and non-modified files re-publish anyway under D1 rule 2, so this matches containerlab's effective behavior without classifying artifacts.

### D5: Reset rides the ordinary Pod replacement path

Reset is requested with a Node annotation (`c9s.run/device-state-reset: <opaque token>`). The node controller projects the token into the Pod template as an annotation, which replaces the Pod through the existing rollout machinery. The prepare init container receives the token (`--reset-token`), compares it with the last-acknowledged token stored in the staging ledger, and on mismatch wipes the node's plan-owned artifact tree, stages everything fresh, and records the token as acknowledged. The controller reports the acknowledged token via an event and a status field once the new Pod is prepared.

Rationale: the wipe happens exactly where staging already owns the filesystem, needs no new privileged actor, is idempotent (same token never wipes twice), and Pod replacement comes for free from the template change. Alternative considered: controller deletes and recreates the PVC. Rejected: racy against the running Pod, loses retention interplay, and requires the controller to sequence volume detach.

### D6: Retention decouples claim lifetime with adoption on recreate

`persistence.reclaim: Delete | Retain` (default `Delete`) is added to the persistence policy on NodeProfile and the Topology deployment block. `Delete` keeps today's owner reference. `Retain` omits the Node owner reference and instead labels the claim with its Node identity. On Node creation, the controller adopts an existing claim with the matching name and identity labels if the storage class matches and the requested size is not larger; incompatibility is a structured reconcile error, not silent recreation. Orphan cleanup stays a user action (`kubectl delete pvc`), and the persistence guide documents it.

Alternative considered: Kubernetes `StatefulSet`-style retain semantics via `persistentVolumeReclaimPolicy`. Rejected: that governs PV-level behavior below the claim, not claim survival across Node deletion.

### D7: Save warning from plan context

The Save runner already holds the plan and runtime input inside the Pod. The renderer records the persistence flag in the lifecycle input; when Save completes on a non-persistent volume, the runner appends a warning line naming the Node. No Kubernetes API access is involved.

## Implementation Notes

Two findings from cluster validation refined the design without changing it:

- The reset wipe initially failed on device-owned directories the preparation identity cannot
  read (preparation drops DAC_OVERRIDE and keeps only CHOWN and FOWNER; SR Linux writes
  `0700 srlinux:srlinux` directories). `removeAllForced` re-owns and re-opens such directories
  with exactly those retained capabilities before removal.
- `RunPostDeploy` initializes the target Node's imported implementation against the real
  artifact lab directory, and kinds may rewrite planned files there on every post-start (SR
  Linux regenerates `topology.yml`). From the second boot such files legitimately count as
  lab-directory state and are preserved, mirroring containerlab's own trust model where the
  running lab owns its directory; the preservation is reported. A lifecycle File action whose
  source was preserved is accepted through the ledger's preserved set instead of the plan
  digest, matching containerlab copying current lab-directory content.

## Risks / Trade-offs

- [Device rewrites a file byte-identically] The ledger sees no difference and treats it as untouched; a later plan change overwrites it. Acceptable: content-identical means no user work is lost.
- [Stale saved config hides spec updates] With a saved config present, updated startup configuration in the Topology is intentionally ignored (containerlab parity) and users may not notice. Mitigation: preparation records the skip; the persistence guide documents enforce and reset as the propagation paths.
- [Ledger corruption] Falls back to D2 preserve-first behavior; the worst case is a stale-but-saved lab, never lost work.
- [Retained claim drifts from a changed plan] A recreated Node with a very different definition may boot from state seeded by the old plan. Mitigation: adoption requires identity-label match; the guide tells users reset re-seeds after definition changes.
- [Reset token misuse] A stale annotation re-applied later could wipe unexpectedly. Mitigation: acknowledged tokens are persisted in the ledger and honored once; the controller surfaces acknowledgment so tooling can clear the annotation.

## Migration Plan

1. Ledger writing and D1 publication rules ship together in the prepare/runtime binary; manager and device-Pod images roll as one release, as today.
2. Existing persistent volumes hit D2 on first boot after upgrade: saved configs that today would be clobbered on the next restart become preserved. No user action needed.
3. API additions (`reclaim`) are optional with compatible defaults; CRDs regenerate via `make verify-generated`.
4. Rollback: reverting the release restores unconditional staging; ledgers are inert extra files that old preparers ignore.

## Open Questions

- Whether the persistence guide should recommend a conventional annotation value for reset (timestamp) or leave the token fully opaque; documentation choice only.
