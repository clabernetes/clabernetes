# Direct runtime remediation and SR Linux boot/dataplane e2e (2026-08-19)

Environment: kind cluster `c9s-direct-links` (control plane + 2 workers, Kubernetes v1.36.1),
manager image built from this branch (`clabernetes-manager:direct-fixes-2` / `direct-fixes-3`),
`manager.deviceRuntimeMode=direct`, host-endpoint DaemonSet running on all nodes.

## Defects fixed and verified against the live cluster

1. **Tag-less image references** (`ghcr.io/nokia/srlinux`) were rejected by
   `internal/ocimetadata` (`name.StrictValidation`) with a misclassified
   `InvalidAuthentication` diagnostic whose reference was redacted to `<invalid>`. Fixed to
   Docker `latest` semantics; a follow-up fix makes resolved metadata echo the requested
   reference verbatim, because planning matches declared/discovered references by exact string.
   Before: `e2e/topology/direct` timed out with zero workloads. After: both SR Linux Nodes
   reach `readiness=ready`.
2. **Worker artifact leak**: planner/image-discovery Pods, their NetworkPolicies, and planner
   input ConfigMaps were never deleted. Observed live before the fix: 21 completed worker Pods
   and up to 24 input ConfigMaps per Node in `direct-links-e2e` after a few hours of link
   churn. After rolling the fixed manager, the owner-scoped sweep removed all of them within
   one watchdog pass, and new attempts delete their Pod and NetworkPolicy as soon as the worker
   record is persisted to its output ConfigMap.
3. **Worker records survive log loss**: results previously lived only in Pod logs and were
   re-read via the API server on every reconcile; log rotation permanently wedged a healthy
   Node. Records now persist in immutable owner-referenced ConfigMaps named like the worker
   Pod; structured failures persist as a negative cache, and terminal Pods without a usable
   record are deleted for backoff retry.
4. Cache-visibility fixes (probe/certificate Secret labels, API-reader probe reads, unlabeled
   payload ConfigMap/Secret caching with data stripped), restricted-RBAC gating of cluster-wide
   `pods/exec`/`pods/log`/`events`, resolver HTTP deadlines, e2e Helm values, worker log
   framing, slurpeeth shutdown ordering, entropy reader serialization, a nil connectivity
   revision variable shadow, and removal of the mapper's imported `CLAB_INTFS` override.

## e2e observations (task-scoped namespace, removed after each run)

`go test ./e2e/topology/direct/` with `C9S_E2E_DEVICE_RUNTIME_MODE=direct`:

- `TestNodeLinkDirect` **PASS in 104s**: two `nokia_srlinux` Nodes (unmodified
  `ghcr.io/nokia/srlinux`, no explicit tag) planned through isolated workers, booted as direct
  device Pods (`2/2`, preparation init + connectivity sidecar + device container running the
  actual SR Linux image), cross-worker vxlan Link realized.
- Completed planning workers were collected (zero worker Pods remained once Nodes were ready).
- Link rewire (`srl2:e1-1 -> e1-2`) applied as a connectivity revision **without rolling the
  device Pods** (same Pod names before and after; plan digest advanced).
- The suite was subsequently extended with inline SR Linux startup configs and a dataplane
  assertion (`ip netns exec srbase-default ping` across the vxlan Link from inside the device
  container); see the test for the current scope and CI for its latest result.

Unit suite: all packages pass; `-race` passes for `controllers/node`, `internal/deviceplan`,
`internal/directruntime`, `internal/ocimetadata`. `make verify-generated` and `make check-docs`
pass. `make lint` still fails on pre-existing branch-wide style debt (~4k findings concentrated
in `internal/`), tracked under task 12.4.

## Still open from this round

- `e2e/clabverter` in direct mode (includes a host Link) — under investigation in this session;
  results recorded separately if resolved.
- Steady-state host-endpoint RPCs are paced to 30s re-assertion (immediate on cold start,
  revision change, or failure); the daemon-side listing cost per request is unchanged.
