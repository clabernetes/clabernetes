# Tasks 6.3/6.4 — Sidecar fabric, host links, and lifecycle on the kind cluster (2026-08-21)

Validated on `c9s-direct-links` with manager `daemonless-9` using a minimal two-node linux-kind
lab (cross-worker pinning, one vxlan Link, one host Link) plus the e2e direct suite. Two
product defects were found by these runs and fixed:

1. **Worker-namespace netlink handle.** The package-level netlink API caches its socket in the
   namespace of first use, so host-Link operations executed inside the worker namespace saw the
   Pod namespace. Host legs now move via the `WorkerPath()` handle and worker-side operations
   use a `netlink.NewHandle()` created inside the entered namespace. (The `EndpointNamespace`
   interface previously named the Pod namespace "target", which hid the inversion.)
2. **`act_csum` before mirred into VXLAN silently drops every frame** on current kernels: VTEP
   TX counters increment, no encapsulation is emitted, no error counters. The stitch now uses a
   mirred-only redirect, with checksum completeness guaranteed by disabling TX offload on both
   veth legs (verified by 50 KB TCP transfers with matching digests across the tunnel).
   A third instance of the LinkAdd-ignores-alias pitfall (host-Link Pod leg) was fixed the same
   way as the fabric and interposition legs.

Recorded results:

- **Cross-worker fabric**: ping 3/3 and a 50 KB TCP transfer with matching md5 between
  linux-kind Pods on different workers, over in-Pod stitched VTEPs (VNI from the Link
  allocation, peer via the headless `-vx` Service).
- **Host Link**: worker-side veth present and UP in the worker namespace with the sidecar
  ownership alias; Pod-side leg UP in the Pod.
- **Live Link removal**: deleting the Link projects an interface-only revision; the sidecar
  sweep removes the device leg, stitch leg, and VTEP (convergence bounded by kubelet ConfigMap
  propagation, ~2 min), without restarting Pods.
- **Forced deletion**: the worker-side veth died with the Pod namespace (replacement re-created
  it under a new MAC); no sweep or agent involved.
- **Transport-rule scoping**: the linux-kind runs exposed that a catch-all transport rule
  shadows a kernel-dataplane device's own data routes; rules are now scoped
  (`iif` router leg, `from` Pod address, `to` management subnet) and the sidecar resolver binds
  the Pod address. Kernel-dataplane kinds route their data interfaces from main again.

The e2e direct suite (`go test ./e2e/topology/direct/...`) is the regression harness for these
paths; the daemon-era restart step was removed from the recovery test as part of the daemonless
model.

## Final e2e result (manager `daemonless-11`)

```
--- PASS: TestMultiWorkerRecoveryDirect (219.65s)
--- PASS: TestLinuxDataplaneDirect (43.13s)
--- PASS: TestDirectPacketCaptureOperation (52.83s)
--- PASS: TestDirectSaveOperation (53.21s)
--- PASS: TestNodeLinkDirect (69.65s)
ok  github.com/clabernetes/clabernetes/e2e/topology/direct  289.314s
```

The recovery test covers every Link flavor plus live link change, partial update, forced Pod
deletion, and cross-worker rescheduling under the sidecar owner. Two further defects were found
and fixed on the way to green:

3. **Exact CNI route replay.** kindnet Pods carry no kernel connected prefix route — their
   routing is `subnet via gateway` plus a `/32 scope-link` gateway route. The transport rename
   originally restored only the default route while the kernel resurrected a connected prefix,
   which silently broke all same-node traffic (kindnet has no proxy-arp to answer on-link ARP).
   The sidecar now snapshots the complete CNI route set before the rename, replays it exactly
   (deleting the resurrected connected route), persists the snapshot, and mirrors it into the
   transport table. Locked in by the isolated-namespace test with kindnet-shaped fixtures.
4. **Digest-pinned spec is the authoritative image identity.** containerd reports the OCI index
   digest for content that entered its cache through a tag pull, so comparing the kubelet's
   reported imageID against the plan's platform-manifest digest produced cache-state-dependent
   false negatives. When the Pod spec reference is digest-pinned (always, when a digest is
   known), the runtime contract guarantees content and the reported identity only corroborates;
   the unpinned path still fails closed. Unit-locked in
   `TestObserveDirectContainersAcceptsIndexDigestWhenSpecIsPinned`.
