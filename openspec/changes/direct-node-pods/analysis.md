# Direct Node Pods — Full Analysis, Findings, and Suggestions

Analysis of the `direct-c9s` working tree (2026-08-19) against `direct-node-pods-goal.md` and
`openspec/changes/direct-node-pods/`. Sources: full code review of every new/changed subsystem,
`go build`, the unit-test suite, the live `c9s-direct-links` kind cluster (reused read-only plus one
task-scoped namespace, removed afterwards), and one live run of `e2e/topology/direct`.

## TL;DR

**The core architecture is right and demonstrably works** — a 4-node direct-mode lab with VXLAN
links is running in the `c9s-direct-links` cluster, device images are first-class containers, unit
tests pass, and the SR Linux "no forwarding repair needed" evidence is real. This was not a wasted
effort.

**But the concern about over-engineering is justified.** Roughly a quarter of the ~56k new lines is
defensive ceremony that guards against states the design already makes unreachable, plus dead code,
plus speculative generality for containerlab behaviors that don't exist (xattrs, provenance tags,
unused plan schema). And there are **real bugs** the ceremony didn't prevent: the repo's own direct
e2e test fails today, `make test-e2e-local` is broken, three cache-visibility bugs wedge or blind
the controller, `restrictedRBAC` is silently defeated, and completed planner Pods / NetworkPolicies
/ input ConfigMaps leak forever (verified live: 21 retained Pods and up to 24 input ConfigMaps per
Node after a few hours).

**Biggest process risk: everything is uncommitted.** ~15k diff lines plus ~50k lines of untracked
Go sit in the working tree with zero commits on the branch. Committing in reviewable slices should
happen before anything else.

**Honest progress:** tasks.md shows 64/85 done, but everything hard that remains is validation
(section 10: real vendor kinds), removal (11), and docs (12). All completed sections were validated
against `linux`-kind containers and one SR Linux boot only. The direct e2e fixture failing means
some checked boxes (4.x/9.x claims of working direct rendering end-to-end) are overstated.

---

## 1. Scale of the change

| Metric | Value |
| --- | --- |
| Tracked diff | 132 files, +6,296 / −8,759 |
| New `internal/` tree | 24,863 non-test + 14,776 test LOC (9 packages) |
| New/changed `controllers/node/` | 10,071 non-test + 6,737 test LOC |
| `go.mod` requires | 74 → ~250 modules (+173 indirect from containerlab) |
| Packages linked into the manager | 1,394 (incl. go-git, kind, docker client, minio, scrapligo, bubbletea) |
| Manager binary | 92 MB |
| Commits on branch | **0 — all work is uncommitted** |
| OpenSpec artifacts | design.md 40 KB, 11 spec files (31 KB), 85 tasks |
| Recorded validation evidence | 1 file (task 5.7, SR Linux direct management) |

## 2. What verifiably works

- `go build ./...` clean; **all unit tests pass** (`go test` excluding `e2e/`).
- Live `c9s-direct-links` cluster: 4-node direct lab (`network-multitool`) with cross-worker VXLAN
  links; device Pods are `2/2` (device + connectivity sidecar); host-endpoint DaemonSet running;
  Node status conditions (`PlanApplied`, `Prepared`, `ConnectivityReady`, `ContainersReady`) all
  coherent; plan ConfigMaps ~3 KB; planner Pods complete in ~1 s.
- The SR Linux evidence file (`openspec/.../evidence/task-5.7-srlinux-direct-management.md`) is a
  genuine, specific record: direct boot with the unmodified image, management IP, DNS, Service and
  external reachability all verified — correctly justifying deletion of the nested DNS-forwarding
  repair (four REMOVED requirements in `specs/runtime-dns-forwarding/`).
- Strong individual pieces: the `ocimetadata` resolver (bounded cache, credential redaction,
  digest re-verification), the recording-runtime boundary in `deviceplan` (fail-closed, panic
  recovery with redaction), the host-endpoint daemon's "re-derive from Kubernetes, annotate before
  mutating" protocol, the fail-closed Topology compiler with source-line diagnostics, the
  `upgrade-preflight` tool, and `clabverter/emitcrs_equivalence_test.go` (byte-equivalence between
  clabverter output, controller rendering, and canonical plan input).

## 3. What is broken right now (verified)

### P0 — user-visible failures

1. **The repo's own direct e2e test fails.** `go test ./e2e/topology/direct/` → timeout waiting
   for `deployment/srl1`; the Node wedges on
   `OCI metadata InvalidAuthentication for "<invalid>": image reference is invalid`.
   Root cause: `internal/ocimetadata` uses `name.StrictValidation` everywhere
   (`auth.go:142`, `resolver.go:219`), which rejects tag-less references. The fixture — and normal
   containerlab usage — writes `image: ghcr.io/nokia/srlinux`, which Docker/containerlab treat as
   `:latest`. So **direct mode cannot deploy any Node without an explicit tag**, the error is
   misclassified (auth, not validation), and the offending reference is redacted to `<invalid>`.
   Either default to `latest` (compatibility-correct) or reject at admission/compile time with a
   clear diagnostic — not deep in metadata resolution with an unusable message.
2. **`make test-e2e-local` is broken.** `.mk/e2e.mk:95-97` still passes
   `--set globalConfig.deployment.launcherImage=...` (+ pull policy, log level) — values this
   branch removed. Helm silently ignores them, `LAUNCHER_IMAGE` falls back to `dev-latest`, kind
   loaded a different tag → launcher Pods ImagePullBackOff. Also `e2e/topology/srsim` now sets
   `imagePull.pullSecrets`, which in nested mode (the default!) no longer reaches the inner dockerd
   at all.
3. **Probe-Secret cache wedge.** `manager/kubernetes.go:29` filters the whole cache on
   `c9s.run/app=<appName>`; the direct probe Secret (`controllers/node/directprobes.go:125`)
   doesn't carry that label but is read via the **cached** client (`directprobes.go:141`). First
   reconcile creates it; every one after gets NotFound → Create → AlreadyExists → hard error,
   forever. Any topology with an SSH-password probe wedges permanently. (Its GC lists via the same
   blind cache and collects nothing.)
4. **Dead payload watches.** `controllers/node/controller.go:203-215` registers ConfigMap/Secret
   watches so payload edits re-plan — but user payload objects carry no `c9s.run/app` label, so the
   filtered informer never sees them. **Editing a startup-config ConfigMap never triggers
   re-planning.** Certificate Secrets have the same problem for their owner watch
   (`certificate.go:158,255`).
5. **`restrictedRBAC` silently defeated.** In `charts/.../clusterrole.yaml` the new
   `pods/log`, `pods/exec`, `events` rules (lines ~108-131) are not gated on
   `restrictedRBAC.enabled`, unlike the networkpolicies rule right above them. Rendering with
   restricted mode on still grants **cluster-wide `create pods/exec`** — full cluster-admin
   escalation, defeating the feature's purpose. The namespaced duplicates in `restricted-rbac.yaml`
   show the intent; the cluster-wide copy just isn't wrapped. Add a chart test asserting the
   *negative*.

### P1 — operational defects

6. **Planner-artifact leak.** Planner Pods, image-discovery Pods, their per-attempt
   NetworkPolicies, plan-*input* ConfigMaps, and certificate bundle Secrets are **never deleted**
   (only plan CMs, connectivity CMs, and probe Secrets have GC). Verified live: per Node after a
   few hours of link churn — 7 planner + 14 images Pods, up to 24 input ConfigMaps. A 50-node lab
   with 10 plan changes ≈ ~1,000 dead Pods + 1,000 NetworkPolicies (which every CNI agent
   evaluates). No `ttlSecondsAfterFinished` — these are bare Pods, not Jobs.
7. **Failed planner Pods stick forever.** Content-addressed name + no deletion + no retry: a
   transient failure (OOM, eviction, 300 s deadline under load) is cached permanently; the only
   exits are editing the spec or manually deleting the Pod.
8. **Plan results live only in Pod logs.** The controller re-reads planner/discovery stdout via
   `GetLogs` on **every** reconcile; kubelet log rotation/eviction permanently bricks an otherwise
   healthy Node with no recovery path. The log framing is also fragile: no leading newline before
   the `C9S_DEVICE_PLAN_V1:` frame, and any >1 MB log line anywhere in the stream fails the whole
   decode (`internal/deviceplan/worker.go:161,281`).
9. **No reconcile fast path.** `reconcileDirect` (`direct.go:64-559`, one ~500-line function) runs
   the entire pipeline — 2 namespace-wide Lists, payload reads, registry metadata, 2+ `GetLogs` —
   on every reconcile even when nothing changed, with `MaxConcurrentReconciles: 1` serializing the
   whole cluster. A `Config` change enqueues every Node everywhere. And OCI resolution has **no
   HTTP or context deadline** (`http.DefaultTransport`, no response-header timeout): one hung
   registry blocks the single worker indefinitely.
10. **Host-endpoint RPC storm.** The sidecar calls the host daemon every 1 s tick; each request
    triggers ~`6 + 3×endpoints` uncached, unpaginated LIST calls against the API server
    (`internal/hostendpoint/daemon.go`, `state.go`). With P pods per worker that is
    `P × (6+3E)` LISTs **per second**. Worst scalability defect found.
11. **Sidecar fail-fast blast radius.** Any error in the connectivity poll loop terminates the
    sidecar (`connectivity.go:1287`); a DaemonSet rollout or API blip crash-loops the sidecar of
    every direct Pod on the node simultaneously, wiping readiness. Only peer-unavailable is treated
    as retryable.
12. **Live-update mechanism silently degrades to Pod recreation.** `directConnectivityRevision`
    swallows six error classes into "recreate the Deployment" (`directconnectivity.go:320-364`) —
    a transient cache miss causes exactly the device restart the feature exists to avoid.

### P2 — latent bugs and rough edges

13. `mapper.go:652`: `maps.Clone(nil)` + map write → planner panic (no `invokeImported` recovery
    on this path) for any kind that builds a fresh `NodeConfig` for component containers.
14. Unbounded `os.ReadFile` in the artifact scan, `CopyToContainer`, and preparer — an imported
    hook writing a big file OOM-kills the planner with an opaque diagnostic.
15. Entropy: reads/mutates the session map without the mutex (`entropy.go:104-112`) — a goroutine
    spawned by an imported hook touching `crypto/rand` is a map race; and keying the DRBG on
    `inputDigest` re-rolls every node's MAC on any unrelated input change, rolling all Pods.
16. `LabDir` differs between planning (`/clabernetes/plan/<id>`) and post-deploy replay
    (`artifactRoot/node-<digest>`); any recorded exec/copy embedding it fails the replay invariant
    with an unactionable "operation stream differs" error. Only 6 `NodeConfig` fields are
    path-rewritten; `Exec`, `Labels`, `License` can leak scratch paths into the plan.
17. Slurpeeth: fds closed before `wait.Wait()` (use-after-close/fd-reuse race,
    `slurpeeth_transport_linux.go:586-594`); one flapping peer restarts the whole child and blips
    all other slurpeeth links on the Pod.
18. Host-endpoint daemon accepts the SCM_RIGHTS netns fd unverified — one lab Pod's privileged
    sidecar can claim another Pod's identity and steal its host link (contained to c9s workloads,
    still worth an inode check).
19. Variable shadowing at `direct.go:459` leaves the outer `connectivityRevisionConfigMap` nil on
    the cold path — latent nil-deref for any future change in `reconcileDirectLinkRestart`.
20. Pseudo-node kinds (`bridge`, `ovs-bridge`, `host`) rejection was deleted from the Topology
    compiler with no replacement — they now compile into real Node objects.
21. Nested mode — still the **default** (`deviceRuntimeMode: nested`) — was silently degraded: no
    pull secrets, no insecure registries, no proxy env, config stubbed to constants
    (`config/get.go:86-155`), while the rewritten docs (`image-pull.md`, `nokia-srsim.md`)
    describe direct-only behavior. Ship-as-is would break existing private-registry users on
    upgrade with no diagnostic.
22. Device container names are UID-derived monsters
    (`device-e2539637-9e31-...-primary-9bbfd21f4f`), making `kubectl exec/logs -c` unusable and
    contradicting design.md's "deterministic names derived from logical Node identity". Use the
    Node name (+ component suffix) — it is already a DNS label and unique within the Pod.
23. `Node.status.conditions[LinkLifecycleAction]` is an event wearing a condition costume; and
    `Link.status.error` duplicates `conditions[Accepted].message` in an API being broken anyway.

## 4. Over-engineering assessment

### The core is sound — keep it

The load-bearing chain is: **record/replay adapter over the imported containerlab module →
runtime-neutral plan → isolated planner execution → renderer → connectivity sidecar → host-endpoint
daemon for host-owned state**. Each element has a real justification (imported hooks have side
effects and must not run in the manager; srl/sros genuinely write random MACs into generated files,
so deterministic entropy replay is *necessary*, not paranoia; native sidecars are the correct
Kubernetes floor; host links genuinely outlive Pods). The alternatives table in design.md is honest.

### The ceremony — cut list (≈3,500–4,500 LOC deletable without touching the goal)

| What | Where | LOC | Why it can go |
| --- | --- | --- | --- |
| Dead transport-namespace broker | `internal/directruntime/transport_namespace*.go` | 451 | Zero callers, incl. a bespoke fd-passing protocol |
| xattr recording/verification | `deviceplan` (types/codec/mapper/preparer/artifact_metadata) | ~250 | No containerlab kind uses xattrs; replace with fail-closed rejection, drop `FOWNER` |
| Provenance `SourceReference` layer | `adapter.go:824-864`, `preparer.go:192` | ~40 + a full extra FS scan+hash pass | Nothing reads it |
| Compatibility-matrix ceremony | `internal/compatibility` + `baseline.json` | ~1,700 → ~300 | Keep the module-identity gate, `versionReferences` sweep, and registry-driven tests; drop the 33-row hand-maintained behavior matrix (already stale/lying), the ~200-line AST mini-interpreter (deviceplan already imports the real registry), `RegistryDigest`, `registrySourceSHA256` |
| Dead plan schema | `types.go`/`codec.go`/`renderer.go` | 150–200 | `FileSourceEmpty`, `ActionSysctl`, `ActionRenameInterface`, `ActionManagementForwarding`, `PhasePostStop`, `VolumeConfigMap/Secret/Persistent`, unused Healthcheck/Resource fields — validated, never produced |
| Entropy Secret + reconciler | `controllers/node/entropy.go` + Secret per Node | ~130 + 1 API object/Node | Keep the DRBG replay; derive the seed from the Node UID, drop the Secret, drop `inputDigest` from the key |
| Certificate self-verification | `certificate.go:273-372` | ~100 | Re-parsing X.509 and re-verifying signatures of an immutable content-addressed Secret every reconcile proves nothing; longer term: cert-manager |
| Throwaway renders for names | `planner.go:344-384` (renders a full Pod + NetworkPolicy to compute a 12-char name, twice per attempt type per reconcile) | ~40 + 4 renders/reconcile | Hash the actual inputs |
| `normalizePlannerPodSchedulingState` + exact DeepEqual | `planner.go:288-334` | ~40 | Use `DeepDerivative` like `directDeploymentConforms` already does |
| Five `RunConnectivity*` entry points, `RunPacketCapture`, `CompileTopologyWithOptions`, dead VXLAN-drift branch, unreachable owner-prefix check, dup sysctl check | directruntime / topology | ~250 | Test seams exported from `internal/`, dead APIs, unreachable branches |
| Redundant validation ceremony | controllers/node (17-condition ConfigMap re-validation, 4 ownership predicates, 12-clause nil-check preamble, re-validating self-produced data) | ~1,500–2,000 | Guards against an attacker who can write ConfigMaps — who can also write the Deployment; collapse to one `ownedBy()` |
| 4–6× re-normalization per lifecycle call | `codec.go`/`readiness.go`/`postdeploy.go` | perf | Normalize once, assert idempotence — this is on the readiness-probe hot path |
| Log broker as a hard dependency | `logbroker.go` + RBAC + token + dialer | ~370 | Exists for one kind's (sros) *best-effort debug* `StreamLogs`; upstream tolerates its absence, c9s makes it fail-closed (broker failure kills the sidecar → Pod never ready). Make it lazy and non-fatal, key targets by container name not index (any injected mesh sidecar currently breaks it) |
| `CLAB_INTFS` in the mapper | `mapper.go:652-653` | 2 | Duplicates (and clobbers) what `default_node.go:173` already sets — literally the vendor knowledge the design forbids |
| Network seal + NET_ADMIN + netlink in planner | `network_seal_linux.go`, `url_payload.go` | ~45 | Legitimate goal, over-clever mechanism; a separate fetcher Pod with deny-all planner achieves it with zero code |

### The root cause

Most ceremony traces back to two absolutist requirements in the goal doc:

1. *"Prevent every conceivable side effect / tampering, and verify everything twice."* The
   architecture already makes most of these states unreachable (immutable content-addressed
   objects, owner UIDs, isolated workers). Verifying your own immutable output on every pass adds
   code, latency, and new failure modes — the six real cache/GC/e2e bugs above all lived *outside*
   the verified zone.
2. *"Generic support for anything containerlab could ever do."* xattrs, provenance, dead action
   types, and the log broker are capabilities no kind in the pinned 0.78.0 registry needs (or needs
   only as best-effort debug). The invariant "no kind-named dispatch in c9s" is the right one to
   keep; "implement every hypothetical generic operation up front" is not — fail closed with a
   structured `ErrorUnsupported` and add capabilities when a real kind records them.

Suggestion: amend the goal/design to state explicitly that **unsupported generic operations fail
closed with structured diagnostics and are implemented on demand** — that is already the spirit of
the dependency-bump gate, and it deletes the speculative half of the code.

## 5. Operational cost of the planner-Pod pipeline

Per standalone Node, cold start: SA + RoleBinding + entropy Secret + 2 input CMs + 2 NetworkPolicies
+ image-discovery Pod(s) (up to 8 rounds) + planner Pod + CA/bundle/probe Secrets + connectivity CM
+ plan CM + Deployment + 2 Services (+PVC) ≈ **13–15 API objects and ~6 reconciles gated on two
sequential Pod lifecycles** (realistically 1–3 minutes before a device container starts). Every
plan-input change repeats the Pod/CM/NetPol creation with new content-addressed names and leaves
the old ones behind.

The isolation rationale for out-of-manager planning is genuinely sound (imported Go code can call
the OS directly). But the current shape is more expensive than it needs to be:

- **Use `Job` + `ttlSecondsAfterFinished`** instead of bare Pods — free GC, free retry/backoff
  (fixes leaks #6 and stuck failures #7).
- **Persist worker results as an owned ConfigMap** (worker writes it, or manager writes it once
  after first successful log read) — fixes durability #8 and enables a real fast path.
- **One long-lived deny-all NetworkPolicy per namespace** selecting a planner label instead of one
  per attempt.
- **Fast path**: compare a cheap input identity (generations + resourceVersions) against
  `status.planDigest` before running the pipeline; skip to status/service reconciliation when
  unchanged.
- **Merge image discovery into planning** where possible — discovery exists to learn image/cert
  requirements before metadata resolution, but up to 8 extra Pod lifecycles per Node is a heavy
  price; one worker invocation could emit both.
- **Split the containerlab-importing planner into its own binary/image.** Today the manager — the
  component with cluster-wide `pods/exec` — links all 1,394 packages (92 MB). The planner worker is
  the only thing that needs the containerlab module; a separate binary shrinks the manager's attack
  surface and image dramatically.

## 6. Security posture

Good: planner sandbox (no SA token, deny-all netpol, read-only rootfs, drop-ALL, seccomp,
deadline), secret-byte exclusion from plans/inputs (with negative tests), preparation-only CA key,
credential redaction in ocimetadata, host-endpoint daemon re-derivation protocol, no `hostPID`
anywhere.

Needs fixing beyond the RBAC gate (#5): the connectivity sidecar is `privileged` **and** mounts
`/proc/1/ns` (the whole directory, and unconditionally — the gating predicate is always true).
Read-only does not stop `setns()`; that sidecar is host-root-equivalent in every direct Pod. Narrow
the mount to `/proc/1/ns/net`, and longer term route host-namespace operations through the
host-endpoint daemon so the per-Pod sidecar can drop `privileged` entirely (the daemon already has
the right validation protocol). Also: host-endpoint DaemonSet tolerates everything and lands on
every node including control planes; the dead core-`nodes` RBAC grant can go.

## 7. Recommended plan

### Step 0 — preserve the work

Commit the current state in reviewable slices on `direct-c9s` (e.g. ① containerlab import +
compatibility gate, ② deviceplan, ③ ocimetadata, ④ directpod/directruntime/hostendpoint,
⑤ controllers/node direct mode, ⑥ API/chart/preflight migration, ⑦ topology/clabverter, ⑧ docs +
openspec). Nothing else matters if this tree is lost, and no one can review a 56k-line blob.

### Step 1 — make the existing claim true (fix P0/P1)

1. Tag-less image handling (`ocimetadata` StrictValidation) + fix the misclassified/redacted
   diagnostic; make `e2e/topology/direct` pass.
2. `.mk/e2e.mk` helm values → `manager.launcherImage`; decide the srsim/nested pull-secret story.
3. Cache visibility: `ByObject` entries (or `c9s.run/app` labels) for probe/cert Secrets and
   payload watches; add a regression test that a payload edit re-plans.
4. Gate `pods/exec|pods/log|events` under `restrictedRBAC` + negative chart test.
5. Jobs + TTL for workers, result ConfigMap, per-namespace NetworkPolicy, input-CM/cert-bundle GC,
   reconcile fast path, `RequeueAfter` on pending states, HTTP timeouts on the resolver.
6. The small hard bugs: `direct.go:459` shadowing, `mapper.go:652` nil map, worker log framing,
   slurpeeth shutdown order, entropy mutex.

### Step 2 — shrink (the "over-engineered" answer)

Work the cut list in §4 top-down (~3,500–4,500 LOC, several API objects and capabilities removed,
no goal semantics lost). Also: split `reconcileDirect` into named phases, collapse the ownership
predicates, raise the sidecar poll interval, make the log broker lazy/non-fatal, fix container
names, flatten `LauncherProfileDeployment`, camelCase the new `ManagementPolicy` JSON tags and the
`direct-workload` label *now* while the API break is free.

### Step 3 — only then resume the task list

Section 10 (real vendor kinds — SR OS components, cEOS interface fixups, vrnetlab/KVM) is where the
architecture will actually be proven or falsified; prove **one VM-backed kind early** before more
infrastructure polish, since `/dev/kvm`, hugepages and tap wiring are the likeliest place a generic
gap hides. Keep the evidence-file discipline from task 5.7 — it worked.

### Worth reconsidering at the design level

- **Nested mode's twilight state**: it is the shipped default but silently lost private-registry,
  proxy, and configurability. Either keep it functional until the cut, or make direct the default
  now behind the documented migration — the current halfway state is the worst of both.
- **Planning latency/UX**: 1–3 min and 13–15 objects per Node is a real UX regression vs the
  launcher; the Step-1 changes (result CM + fast path + merged discovery) get most of it back.
- **The openspec behavior matrix** (baseline.json `behaviors`) duplicates tasks.md in a format
  nothing verifies and is already stale — fold it into the docs generator or delete it.
- The `versionReferences` gate points at `openspec/changes/direct-node-pods/design.md`, which gets
  archived — that build gate will break on archive.

## 8. Cluster/test-environment notes

- `c9s-direct-links` (3-node kind, manager `direct-conformance-20260819-7`): the active direct-mode
  test cluster. I ran `e2e/topology/direct` against it (failed, see P0-1), reproduced the failure in
  a task-scoped `c9s-analysis-direct` namespace, and deleted that namespace afterwards. The
  pre-existing `direct-links-e2e` namespace still holds the leaked planner/images Pods and input
  CMs — useful as live evidence of finding #6 before you fix GC.
- `try-c9s` (1-node kind, 7 days old): runs an old **nested** manager (`pr290-review-fixes`) plus a
  leftover `e2e-topology-basic-hnvjsrlh` namespace (25 h). Nothing on this branch needs it; safe to
  delete or recycle via `make try-c9s` once the launcher-image values fix (P0-2) lands.
- Checks run: `go build ./...` ✅, unit tests (all packages except `e2e/`) ✅,
  `e2e/topology/direct` ❌ (P0-1). Not run: `make lint`, `make verify-generated`, `make test-race`,
  image builds, `make test-e2e-local` (known broken, P0-2).

## 9. Remediation status (updated 2026-08-19, same day, follow-up session)

Step 0 and Step 1 of §7 are done; the work now lives in reviewable commits on `direct-c9s`:

- **Committed in 9 slices** (deps/compat gate, deviceplan, ocimetadata, direct runtime,
  API migration, node controller, topology/clabverter, charts/docs, openspec artifacts),
  followed by fix commits.
- **P0/P1 fixed and verified live**: tag-less image handling plus verbatim reference identity
  through metadata resolution; `.mk/e2e.mk` Helm values plus an `E2E_DEVICE_RUNTIME_MODE` knob
  and mode-gated suites; probe/cert Secret cache visibility (labels + API-reader reads);
  payload ConfigMap/Secret caching with data stripped so payload edits re-plan; restricted-RBAC
  gating of cluster-wide exec/log/events with a negative chart test; worker records persisted
  in output ConfigMaps (log-rotation-proof, negative-cache for deterministic failures,
  delete-and-retry for transient ones); prompt worker Pod/NetworkPolicy deletion plus an
  owner-scoped sweep (verified: the 84 leaked Pods in `direct-links-e2e` were collected within
  one pass); resolver HTTP deadlines; 60s direct-mode watchdog requeue; worker log framing;
  slurpeeth shutdown order; entropy read serialization; the `direct.go` shadowing bug; the
  mapper's `CLAB_INTFS` clobber; the dead transport-namespace broker (−451 LOC); steady-state
  host-endpoint RPC pacing (30s re-assertion); direct-mode auto-expose parity with the nested
  default port set (found by the clabverter e2e, which exercises a host link in direct mode).
- **Validated**: full unit suite, `-race` on the four changed packages, `make verify-generated`,
  `make check-docs`, and the rewritten `e2e/topology/direct` against the live cluster — two
  unmodified SR Linux images boot as direct device Pods, dataplane ping crosses the vxlan link,
  planning workers are collected, and a live rewire lands without a Pod roll (evidence:
  `evidence/direct-runtime-remediation-e2e.md`).
- **Still open**: `make lint` (branch-wide pre-existing style debt, task 12.4), the §4 cut list
  beyond the items above (xattr recording, provenance layer, compatibility-matrix shrink,
  certificate self-verification, entropy Secret, reconcile fast path, log-broker gating,
  container-name UX, `/proc/1/ns` narrowing), vendor conformance beyond SR Linux (tasks
  10.3–10.9), nested-runtime removal (11.x), and the remaining docs/acceptance work (12.x).
