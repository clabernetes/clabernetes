# Design: private device netns for greedy interfaces (ixia-c)

## Context

The direct runtime's sidecar owns transport/routing state that lives in the Pod
netns; #372 shows a class of devices that treat "every interface in this
netns" as owned. The codebase *already* tolerates device-leg renames/moves and
*already* moves one leg into another netns for host Links — so the primitives
exist. The missing piece is making isolation the *default* for a known greedy
kind without requiring a user opt-in.

## Goals / Non-goals

- Goals:
  - ixia-c (and `keysight_ixia-c` alias) works on the direct runtime with
    zero extra user fields, verified on the remote test cluster.
  - No regressions for existing kinds; isolation is inert when neither the
    profile nor the ixia-c family default enables it.
  - Keep the public API generic: one optional `networkIsolation` policy field on
    `NodeProfile` (mirrored on `Topology`), with kind knowledge confined to
    profile resolution rather than planning.

- Non-goals:
  - Generic per-node/per-topology "isolation: private/shared/host" mode —
    valuable but bloats the spec review; deferred as a follow-up if a second
    consumer arrives.
  - Full support for every Link flavor inside the isolated namespace on day 1;
    wire/host/same-pod paths are validated but not required to regress cleanly
    for ixia-c single-node bringup.

## Why profile + defaulting outside planning?

`openspec/specs/device-planning` forbids kind-named switches inside planning;
the codec must stay generic and produce uniform device plans. The fix respects
that: planning keeps emitting unchanged `DevicePlan`/`InterfacePlan`.

The knob lives one layer later — **realization policy on `NodeProfile`** —
which by definition carries reusable, explicitly referenced workload policy.
Topology compilation copies an explicit `topology.spec.deployment.networkIsolation`
value into emitted profiles exactly as it does for persistence today; omitted
values remain unset. `controllers/node/profile.ResolveProfile` is the single
place that applies the ixia-c family default when the resolved value is empty.
Explicit values always win, so Topology-emitted Nodes and imperative Nodes
share identical semantics and compilation never synthesizes policy.

## API addition

`apis/v1alpha1/nodeprofile.go:NodeProfileDeployment` gains optional `*bool`
`NetworkIsolation`; omitted stays unset. The same field is mirrored on
`apis/v1alpha1/topologyspec.go:Deployment` and an explicit value is carried
through `compiler.Input.Deployment` into emitted NodeProfiles. Omitted values
stay unset; `ResolveProfile` applies the ixia-c family default when the
resolved profile is absent or leaves the field unset.

## Architecture

### Netns lifecycle

1. A dedicated hostPath volume (`device-netns`, path
   `/var/run/clabernetes/device-netns`) is mounted at `/var/run/netns` in the
   `device-netns-init` init container (bidirectional propagation), the
   `clabwire` native sidecar (host-to-container), and every application
   container (host-to-container) — **only when** the resolved profile's
   `networkIsolation` is true. The init container is ordered immediately before
   `clabwire`, so the file exists before the sidecar opens it.
2. `device-netns-init` runs `node-runtime device-netns --path
   /var/run/netns/<podUID>.ns`. It unshares a new network namespace and
   bind-mounts the current thread's netns over the per-Pod file, then restores
   its thread and exits. The bind mount is propagated to the host path and is
   visible to the sibling containers.
3. The sidecar opens the file with `netns.GetFromPath` and binds a
   `netlink.NewHandleAt` to it. It moves the device-facing management and Link
   legs into that namespace and addresses them with the private handle; the
   transport, router, mesh VTEP, and sidecar Link legs remain in the Pod
   namespace.
4. Every application container launches through `node-runtime launch
   --deviceNetns <path>`; the launch boundary `setns`es the calling thread
   immediately before `unix.Exec`, so the image process starts in the private
   namespace.
5. On graceful Pod termination the sidecar detaches and removes its netns file.
   A hostPath is used instead of an emptyDir because kubelet's emptyDir teardown
   cannot unlink a bind-mounted netns file and would wedge Pod deletion.

Alternative considered: CRI-level `ShareProcessNamespace` tricks or a CNI that
exposes per-container netns — none available in the supported clusters.

### Pair placement

For isolation-enabled Pods only, the sidecar's `EnsureInterposition` changes
from `LinkAdd(veth)` wholly in Pod netns to:

```
veth(Pod) "mgmt0" <-> "c9sr0"
  -> LinkSetNsFd("mgmt0", devNsFd)
  -> in dev ns: rename mgmt0 to desired DeviceInterface if needed,
                addrReplace management CIDR, linkUp
  -> in Pod ns: c9sr0 already up, holds gateway + mesh membership
```

Same sequence for `EnsureFabricEndpoint`: `LinkAdd("eth1"<->"c9ss…")` then move
`"eth1"` into `devNsFd`. Host Links move the worker end to the worker namespace
as before and then move the device end into `devNsFd`.

Same-Pod and loopback Links are **not yet supported** inside an isolated
device namespace. `validateIsolatedPlan` rejects them with a clear planning
error instead of realizing a pair in the wrong namespace. This is the one
documented scope boundary; cross-Pod fabric and host Links are implemented.
When a Pod mixes isolated and non-isolated containers, the device owner's
containers share the private namespace; a same-Pod secondary that expects
`lo`-only sharing is covered by the same limitation.

### Launch boundary

`RunLaunchWithOperationsAndNetns` calls:

```go
operations.EnterDeviceNetns(devNsPath) // setns(CLONE_NEWNET)
operations.LimitOpenFiles(...)
operations.Exec(argv) // -> unix.Exec(...)
```

`EnterDeviceNetns` opens the bind-mounted file, locks the goroutine to its OS
thread, `setns`es the thread, and deliberately leaves the thread locked because
`Exec` immediately replaces the process. An empty path preserves legacy pods.
The `LaunchOperations` seam covers the new operation, and the renderer adds
`--deviceNetns` to every application launch command.

### Reconciler adjustments (narrow)

- `addressSyntheticDeviceLeg`: the private-netns equivalent of
  `addressDeviceLeg`, using a `netlink.Handle` bound to the device namespace.
  It addresses and raises only an untouched sidecar-owned leg; a device that
  adopted or renamed the leg owns it.
- `ensureDeviceDefaultRoute`: asserts the management default route inside the
  private namespace; the Pod namespace keeps the captured CNI default.
- `EnsureFabricEndpoint` / `EnsureHostInterface`: move the device-facing leg
  into the private namespace after creating the pair and raise it there. On
  later passes an absent Pod-namespace leg is accepted as already isolated.
- `validateIsolatedPlan`: fails closed for same-Pod and loopback Links, which
  are not yet placed inside the private namespace.
- `EnsureSysctl`: package-declared network sysctls follow the device into the
  private namespace. `/proc/sys/net` resolves against the calling thread's
  network namespace and has no netlink interface, so the write runs on a
  thread that enters the bound namespace and is then discarded; the sidecar's
  own Pod-namespace hardening baseline is unaffected because it uses a
  namespace-less operations value.
- `SweepTransportState` continues to own only Pod-namespace sidecar legs; the
  device legs leave the Pod namespace and cannot be reaped by the sweep.

## Alternatives Considered

- **Topology/CRD flag** `spec.networkIsolation` — shipped as the generic
  profile policy; the compiler mirrors explicit values and the resolver owns
  the ixia-c family default.
- **Detect greediness dynamically** (sample `RTM_GETLINK` in sidecar, observe
  bulk departure) — races with reconcile interval, fragile across versions.
- **Wrap ixia entrypoint in unshare(1)** — needs image entrypoint mutation
  via renderer anyway, diverges from containerlab's process tree assumptions.
- **emptyDir for the netns file** — kubelet cannot unlink the bind-mounted
  netns during emptyDir teardown, wedging Pod deletion. A dedicated hostPath
  plus sidecar cleanup is the shipped shape.

## Risks

- Moving legs into a bound netns requires privileged sidecar — already true
  for clabwire.
- Some kernels disallow moving a `NET_ADMIN`-restricted veth type; fallback is
  diagnostic surfacing the kernel denial as `NodeConditionConnectivity`.
- Same-pod secondaries that expect `lo`-only sharing may break; mitigation is
  deferral (documented limitation) and later promoting the isolation flag
  into plan metadata where topology grouping is normalized.

## Observability

- The sidecar logs `connectivity: device network namespace isolation is active`
  at startup.
- The interposition conditions record gains `DeviceNetworkIsolation=True` when
  isolation is active, so a readiness failure includes the isolation mode in
  its diagnostic.
- The real `ixia-c-one` e2e asserts the private netns inode, the absence of
  `c9s0`, the management address on `eth0`, `ConnectivityReady=True`, and a
  package-declared sysctl read back from inside the private namespace.
