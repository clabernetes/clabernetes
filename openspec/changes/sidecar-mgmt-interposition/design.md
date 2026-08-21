# Design: Sidecar-Owned Connectivity, Daemonless

## Context

See `proposal.md` for motivation and the exploration document (Spike 1 + 2 results, task 1.2 evidence) for validated mechanics. This is a single all-or-nothing change: when it lands, the sidecar owns every piece of pod connectivity and `internal/hostendpoint` no longer exists. There is no mode, no fallback, and no phased migration; rollback is deploying the previous c9s version.

Constraints and load-bearing existing structure:

- The `device-connectivity` native sidecar (privileged, restartable init container, startup-probe-gated, `internal/directpod/renderer.go:2488-2512`) already runs before device containers and reconciles from three ConfigMaps (plan/input/revision) plus downward-API pod identity on a 1 s revision tick (`internal/directruntime/connectivity.go`).
- The plan vocabulary already carries everything fabric needs: `InterfacePlan{Connectivity, TunnelID, MTU, PeerTransport, PeerInterface, NamespaceOwnerID, LinkApplyMode, RequiredAtStart}` where `PeerTransport` is a stable per-node fabric Service DNS name (`controllers/node/planinput.go:196`), and `newLinkOperations` already takes a pluggable peer-address resolver (`links_linux.go:37-47`).
- The sidecar already receives an optional read-only worker network-namespace handle (`HostNetworkNamespacePath`, `connectivity.go:39-41`) used today for imported host-side endpoint fixups — the exact mechanism host Links need.
- Management allocation is controller-side (`compileDirectManagement`, `controllers/node/direct.go:1220-1332`) and flows as `ManagementInput` into the plan; `applyManagementInput` (`deviceplan/adapter.go:1566-1582`) feeds each kind's own containerlab config templates.
- The nftables translation backend, checksum-offload helper, and their tests landed in task group 1 and are validated against the hardest case (cEOS programming its own x_tables chains) — see `evidence/task-1.2-nftables-precedence.md`.
- `internal/deviceplan/kind_agnostic_test.go` AST-forbids kind-name dispatch across the direct runtime; all vendor variance must be plan data.

## Goals / Non-Goals

**Goals:**

- One owner: the connectivity sidecar realizes management interposition, cross-Pod fabric, and host Links; the host-endpoint daemon and every trace of it (package, chart, CLI, socket, mounts) are deleted.
- Containerlab-parity management addressing with no Pod-address identity: policy-allocated, or containerlab default-subnet-allocated when no policy exists; configuration rendered at plan time.
- All spike hardening as unconditional baseline; per-kind variance reduced to derived profile data.
- Conformance evidence per kind for the new owner, recorded with the change.

**Non-Goals:**

- Reducing the sidecar's `privileged: true` security context (follow-up; this change must not widen it).
- Direct off-cluster routing of the management prefix (Service/DNAT reachability only).
- IPv6 management translation (the replaced daemon loop was IPv4-only; IPv6 arrives with its own evidence).
- Multiple synthetic management legs per Pod: grouped Pods keep exactly today's containerlab semantic — one management identity per Pod namespace owner.

## Decisions

**D1 — No mode, no API change.** Interposition, sidecar fabric, and sidecar host Links are the only realization. `TopologySpec` and `NodeStatus` are untouched. The management policy keeps its meaning; its absence now means containerlab's default management subnet (`172.20.20.0/24`, gateway at the first usable address, mirroring containerlab defaults) allocated by `compileDirectManagement`. The Pod-address fallback and the runtime management-identity completion (`completeRuntimeManagement`'s Pod-identity synthesis) are removed — management renders fully at plan time, which deletes complexity from the preparer's two-render protocol rather than adding to it. Runtime completion keeps only Pod-resolver DNS discovery.

**D2 — Fabric terminates in-pod on the preserved underlay.** The exact failure that forced fabric into the host namespace — the device destroying the Pod's primary interface — is eliminated by interposition, so the original in-pod design becomes correct. The sidecar realizes each cross-Pod interface as a veth pair (device leg carries the declared name/MTU) stitched to an in-pod VXLAN VTEP (VNI = `TunnelID`, remote resolved from the `PeerTransport` Service name via the existing resolver seam) with tc mirred redirects, mirroring containerlab's proven stitched-vxlan model; slurpeeth runs as a sidecar-managed in-pod process exactly as the legacy launcher ran it. Same-worker and cross-worker links use the same mechanism — one path, no host-namespace patch special case. Peer rescheduling converges through the existing revision tick re-resolving the Service name.

**D3 — Host Links via the existing host-namespace handle, orphan-free by construction.** The sidecar creates the veth pair in the Pod namespace and moves the host end into the worker namespace through the read-only netns handle, naming it as declared. Because deleting either veth end deletes both, forced Pod deletion removes the worker-side end automatically — the daemon's orphan sweep has nothing left to sweep and is deleted with it. The handle is mounted only for topologies whose plans contain host Links.

**D4 — Daemon deletion inventory.** Removed outright: `internal/hostendpoint/` (daemon, client, wire types, Linux operations, state, tests), `charts/clabernetes/templates/host-endpoint-daemonset.yaml` plus its values/RBAC references and golden fixtures, the hidden `device-runtime host-endpoint-daemon` CLI subcommand, the daemon socket hostPath volume and mount in the renderer, `reconcileDaemonEndpoints`/`desiredManagementEndpoint` and the `HostEndpointReconciler` seam in the sidecar. Upgrade hygiene: fabric VTEPs and management-loop state created by a previous version's daemon on existing workers are documented for one-time manual cleanup (worker reboot or `ip link` deletion); pod-side legs die with pod recreation during the upgrade.

**D5 — Plan carries interposition as data on the existing management slot.** `ManagementInterfaceSelector` gains `Interposed`; `ManagementPlan` gains an additive `Interposition` struct: device-leg interface name (derived from the evaluated containerlab node config, defaulting to containerlab's primary-interface contract `eth0`), device-leg MAC (when the kind sets one, e.g. cEOS's `00:1c:73` prefix), gateway addresses, declared inbound ports, and cluster transport CIDRs. No schema-version bump; codec validation is additive. The reserved `ActionManagementForwarding` action stays unused.

**D6 — Everything the spikes found is unconditional baseline.** Policy-table ownership of Kubernetes transport (dedicated table, `iif <router-leg>` + `to <cluster CIDRs>` rules), both NAT shapes installed simultaneously, TX checksum offload disabled on both synthetic legs, `forwarding=0` on the device leg, `accept_local=1` on the router leg, and re-assertion on the revision tick. No per-kind flags: each item is required by some kind and harmless for the rest.

**D7 — Translation backend: nftables with hook-priority precedence (validated).** Dedicated `c9s-interposition` table, dstnat at −110 and srcnat at 90, atomic rebuild per reconcile, behind the `NATOperations` seam. Task 1.2 evidence shows precedence over device-programmed x_tables chains without touching them, and that no filter intervention is needed (cEOS's own FORWARD chain terminally accepts non-loopback forwarding).

**D8 — Readiness and observability.** New readiness conditions (`CNIUnderlayPreserved`, `ManagementTranslationReady`, plus fabric readiness folded into the existing markers) gate the startup probe exactly as today; `validateManagementPodTransportOverlap` continues to fail closed on management/Pod-CIDR overlap.

**D9 — Checksum offload via the `SIOCETHTOOL` helper** already landed in task 1.3; no new dependency.

## Risks / Trade-offs

- [All-or-nothing removes the in-release rollback] → Deliberate per the change owner: rollback is the previous c9s version. The conformance matrix (SR Linux, SR-SIM 25/26, cEOS validated; VM kinds pending) is recorded evidence, and unvalidated kinds are documented as such rather than gated behind a mode.
- [VM-backed kinds are unvalidated for interposition] → They receive the same synthetic `eth0` contract containerlab gives them; conformance runs will validate before any compatibility claim. Documented as unvalidated until then.
- [In-pod VTEP requires the CNI to carry VXLAN-in-UDP between Pod IPs] → This is exactly the legacy launcher's proven transport; kindnet/Calico/Cilium carry it. MTU derivation reuses the plan's MTU fields; conformance covers cross-worker traffic.
- [A CNI plugin may react badly to the primary-interface rename] → kindnet/Calico/Cilium do not re-inspect by name post-setup; kind-cluster validation runs first; failure is fail-closed before device start.
- [Same-namespace devices can still disrupt their own management plane] → The sidecar re-asserts only sidecar-owned state; device-owned breakage surfaces as unreadiness with a precise reason.
- [Upgraded workers keep stale daemon-era host state] → One-time documented cleanup; new installs are unaffected. The chart no longer ships anything that could recreate it.
- [Kernel without nftables support] → Detected at sidecar start; connectivity reports unready with a backend diagnostic. Kind and managed-cluster default kernels ship nftables.

## Migration Plan

1. Single release cut: chart upgrade deletes the DaemonSet; Pods recreate under the new renderer (no daemon socket mount) and converge under sidecar ownership.
2. Documented one-time cleanup for workers that ran the previous daemon (stale fabric VTEP interfaces).
3. Rollback: deploy the previous c9s version (its chart restores the daemon; its pods ignore sidecar-era state, which dies with pod recreation).

## Open Questions

- Cluster transport CIDR discovery source (operator Config vs kubeadm/node discovery) — the plan field accepts either; decidable during implementation without spec impact.
- Which upstream containerlab contributions (declarative interposition facts, SR OS gateway-route template input) are accepted upstream versus staying in the deviceplan compatibility layer — tracked in conformance evidence either way.
