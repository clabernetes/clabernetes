# Task 12.5/12.6 — multi-worker acceptance and vendor scenarios (2026-08-20)

Environment: kind cluster `c9s-direct-links` (control plane + 2 workers, Kubernetes v1.36.1),
manager image `clabernetes-manager:direct-vocab-1` built from this branch (task 13.3 vocabulary
included), release `c9s-direct-links` upgraded in place with the branch chart and regenerated
CRDs applied. All device images pulled by the kubelet from their registries (no side-loading:
the digest gate correctly rejects `kind load`ed images because docker-save re-encodes the
manifest, which is recorded here as verified fail-closed behavior — the readiness condition
named the exact cause: "kubelet image identity differs from the accepted device plan").

## 12.5 — multi-worker recovery suite (`TestMultiWorkerRecoveryDirect`)

New task-scoped suite at `e2e/topology/direct/recovery_test.go`: one namespace carrying every
Link flavor (cross-worker vxlan, cross-worker slurpeeth-flavored, loopback, same-Pod grouped,
host), worker-pinned profiles, then partial update, forced Pod deletion, rescheduling onto the
peer worker, manager restart, and host-endpoint DaemonSet restart, with cross-worker ping
re-proven after each disruption and host-side interface cleanup verified after namespace
deletion.

- Result: **PASS in 382s** — all four Nodes ready with every flavor realized (Link statuses
  accepted, tunnel IDs 1 and 2 allocated for the cross-worker pair), cross-worker ping proven
  on both connectivity flavors before and after every disruption, the partial update rolled
  only lin2, the rescheduled lin1 landed on the peer worker and its wire re-converged through
  the same-worker patch path, the manager and all host-endpoint daemon Pods restarted without
  breaking traffic, and after namespace deletion the host Link's worker-side interface was
  swept from every worker.
- Fixture findings while authoring the suite (both correct fail-closed behavior):
  - a same-Pod Link whose two endpoints share one interface name fails planning with
    "planned interfaces ... use the same Linux name";
  - a grouped secondary running the same image as its primary loses the listen-port race in
    the shared namespace (sshd/nginx) and crash-loops — shared-namespace semantics, resolved
    in the fixture with an inert entrypoint override.

## 12.5 — standard direct suites (rerun on this build)

- `TestNodeLinkDirect` (SR Linux boot, embedded startup config, dataplane, live rewire
  without Pod roll, worker artifact collection): **PASS in 66s**
- `TestLinuxDataplaneDirect`: **PASS in 44s**
- `TestDirectSaveOperation` / `TestDirectPacketCaptureOperation`: **PASS in 50s / 53s**
- `e2e/topology/basic` (Topology entry path, SR Linux DNS from the management namespace):
  **PASS in 154s** (`TestContainerlabBasic` 72s)
- `e2e/clabverter`: **PASS in 41s**

## 12.6 — vendor boot/dataplane scenarios

- SR OS / SR-SIM 26.7.R1 (`ghcr.io/clab-labs/nokia_srsim:26.7.R1`, SR OS 26 license),
  component chassis (A + slot 1), startup-config, datapath to linux peer: **PASS in 212s**
- SR OS / SR-SIM 25.7.R1 (`ghcr.io/clab-labs/nokia_srsim:25.7.R1`, SR OS 25 license):
  **PASS in 124s** — same component-chassis scenario as 26.7.R1
- Arista cEOS 4.33.1F (`ghcr.io/clab-labs/ceos:4.33.1F`): **PASS in 173s**
- Cisco XR vrnetlab (`ghcr.io/clab-labs/vr-xrv:6.3.1`): **PASS in 257s** — console
  startup-config applied through the image's own bootstrap, management SSH answered on the
  Pod address, dataplane to the linux peer
- Juniper vQFX vrnetlab (`ghcr.io/clab-labs/vr-vqfx:20.2R1.10`): **PASS in 347s**

All vendor runs used kubelet registry pulls with test-created `imagePullSecrets`; two
environment defects were diagnosed and fixed on the cluster along the way (stale containerd
image records and a stale kubelet image cache left by historical `kind load` side-loading —
both infrastructure state, not c9s behavior; the c9s digest gate surfaced them precisely).

## Follow-up: SR-SIM classic chassis and link-change behavior (2026-08-21)

Post-acceptance validation driven by clabverter-entry mixed-chassis labs (SR-2s two line
cards + SR-1-92s distributed + SR-1 integrated + three linux endpoints; all five end-to-end
dataplane paths passed, including forwarding across the distributed chassis fabric between
separate line-card containers). Findings:

- **Classic SR-7 is broken in the SR-SIM images, not in c9s.** With the exact
  Nokia-documented pairing (`iom5-e` + `me6-100gb-qsfp28`, typed components), the CPM commits
  the configuration cleanly but the card simulator binary rejects the card at boot
  ("TiMOS card type 1 (Unknown), Hw card type 146 (burger_r1) is not supported") and never
  attaches its data port — identically on 26.7.R1 and 25.7.R1, contradicting Nokia's own
  supported-hardware appendix. Upstream report material; the modern platforms (SR-2s family)
  work as documented.
- **Live link changes on SR-SIM work — the earlier failure attribution was wrong.**
  Controlled A/B on the same host and image (26.7.R1, SR-1-92s distributed): plain
  containerlab (`clab apply`) hot-attaches both a live-added and a live-recreated data veth
  with dataplane passing, and the direct runtime matches it exactly — live-added Link into a
  running chassis: **PASS with zero device restarts**; deleted-and-recreated Link (new UID,
  cross-worker VXLAN, MTU 1450): **PASS** after the daemon replumbs both legs. The
  mixed-chassis incident originally blamed on SR-SIM live handling decomposes into the
  neighboring broken SR-7, a linux peer's flushed boot-time `exec` address (its veth was
  legitimately recreated by a Topology-driven Link replacement), and ARP convergence. The
  imported kind's live declaration stands. Retained findings: declared `restart` is honored
  exactly by c9s (same Pod, restartCounts increment) but is pathological for multi-container
  chassis (SR OS exits 255 on SIGTERM, kubelet backs off exponentially, cards crash-loop
  until the CPM returns — 5+ minutes); declared `recreate` measured 87s for a Node change and
  41s for a Link removal, matching cold-boot behavior (38-90s across all runs).

Verified against the live cluster after the last suite:

- Every e2e harness deleted its task namespace; `kubectl get topologies,nodes,links,
  launcherprofiles -A` (c9s.run group) returns nothing anywhere in the cluster.
- The `st` telemetry-lab namespace from the task-13.4 reference validation (its lab resources
  were already removed after evidence was recorded) was deleted; no task-scoped namespace
  remains.
- No c9s-owned host-namespace interfaces remain on any worker (only `lo`, the node uplink, and
  CNI pod veths are present); the recovery suite additionally asserted the host-Link interface
  sweep directly.
- Retained deliberately (not task-scoped): the `c9s-direct-links` Helm release itself, its
  regenerated CRDs, and the locally built `clabernetes-manager:direct-vocab-1` image on the
  kind nodes — they are the cluster's c9s installation, not test residue. No other diagnostics
  were retained.
- Final verification on the branch at this state: `make test`, `make test-race` (affected
  packages re-raced after the lint refactor), `make lint`, `make verify-generated` (includes
  the containerlab baseline/registry verification with fresh invalidation digests),
  `make check-docs`, and `make build-docs` all pass.
