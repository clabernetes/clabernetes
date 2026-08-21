# Task 6.1 — SR Linux daemonless validation on the kind cluster (2026-08-21)

Cluster: `c9s-direct-links` (kind, 1 control plane + 2 workers, kindnet CNI). Manager image
`daemonless-3` built from this working tree; chart upgraded in place from the daemon-era
release — **the DaemonSet was removed by the upgrade and no node-resident agent exists**.

Topology: two `nokia_srlinux` nodes (`ghcr.io/nokia/srlinux`) pinned to different workers,
management policy `ipv4-subnet: 172.80.80.0/24` with explicit `mgmt-ipv4` 172.80.80.11/12
(srl-telemetry-lab semantics), one vxlan Link `e1-1 ↔ e1-1` with a /31 in startup-config.

Results (all pass):

1. **Interposition in a real pod.** The sidecar preserved the CNI interface as `c9s0` (Pod IP
   and connected route intact), created the synthetic `eth0` with the plan-derived MAC, and SR
   Linux adopted it as `mgmt0` with `172.80.80.11/24` live on `mgmt0.0` in `srbase-mgmt`.
   Startup-probe gating held: device containers started only after connectivity readiness.
   Pods reached 2/2 in ~40 s.
2. **Transport survives the device's route strip.** SR Linux removed default routes from every
   table at boot (including the sidecar's transport table). The sidecar's re-assertion restores
   the transport default from the state-recorded gateway on the next tick; cluster DNS and all
   pod egress keep working with the main table stripped. Two defects found and fixed here:
   the netlink default-route filter (library returns `0.0.0.0/0`, not nil) and device-leg
   ownership marking; both are covered by the new isolated-namespace test
   `TestEnsureInterpositionConvergesIsolatedNamespace` (cold pass, idempotent steady pass,
   re-assertion after a simulated full route strip).
3. **Outbound translation.** SR Linux host-stack management traffic (source 172.80.80.11)
   reached the peer pod across workers and the internet through the nftables translation.
4. **Cross-worker fabric, daemonless.** `ping network-instance default` across the vxlan Link:
   0% loss between the SR Linux data planes over in-Pod stitched VTEPs (worker ↔ worker2),
   VNI from the Link allocation, peer resolved from the headless `-vx` Service.
5. **Management SSH** answers at the interposed address (`SSH-2.0-OpenSSH_10.0` at
   172.80.80.11:22). No inbound DNAT was programmed because the profile disables auto-expose
   and the image declares no ports — matching the derived (empty) inbound contract.
6. **Force-delete leaves zero worker residue.** `ip link | grep c9s` on the worker: 0 before,
   0 after `--force --grace-period=0`. The replacement pod converged 2/2 in 23 s, the peer's
   VTEP re-resolved to the new Pod IP on the reconcile tick, and the fabric ping passed again
   with 0% loss.

Conclusion: the daemonless architecture is validated end to end in Kubernetes for SR Linux —
management interposition, transport protection, translation, cross-worker fabric, replacement
convergence, and namespace-lifetime cleanup, with no daemon anywhere.
