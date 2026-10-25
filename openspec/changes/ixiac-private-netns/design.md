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
  - No regressions for existing kinds; change is invisible when kind != ixia.
  - Minimal public-API churn: no CRD field, no topology knob in v1.

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

`apis/v1alpha1/nodeprofile.go:NodeProfileDeployment` gains optional enum
`NetworkIsolation` (`Disabled`/`Enabled`; omitted stays unset). The same field
is mirrored on `apis/v1alpha1/topologyspec.go:Deployment` and an explicit value
is carried through `compiler.Input.Deployment` into emitted NodeProfiles.
Omitted values stay unset; `ResolveProfile` applies the ixia-c family default
when the resolved profile is absent or leaves the field empty.

## Architecture

### Netns lifecycle

1. A dedicated emptyDir volume (`device-netns`) is mounted at `/var/run/netns`
   in both an init container `device-netns-init` (first in
   `spec.initContainers`) and the `clabwire` sidecar + every application
   container — **only when** the resolved profile's `networkIsolation == Enabled`.
2. The init writes nothing; it only ensures the shared directory exists before
   the sidecar starts. The sidecar owns creation:

   ```go
   // sidecar startup, before EnsureInterposition:
   // - if <stateDir>/device-netns/<podUID>.ns absent, create via netns.New()
   //   and bind-mount to file (like iproute2's /var/run/netns/<name>).
   // - hold open FD and publish path via env/file for launch boundary.
   ```

   Persisting to a *file* (not just holding an FD) is necessary because
   kubelet recreates containers individually — FD alone would not survive a
   device container restart while the sidecar stays up.

3. On shutdown, `SweepTransportState` + container termination naturally closes
   the handle; file cleanup is best-effort like other state under
   `connectivityStatePath`.

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
`"eth1"` into `devNsFd`. Local/Loopback pairs follow suit when both ends
belong to an isolated owner (single-Pod ixia-c has only one owner, so no
ambiguity).

Host-Link already follows `LinkSetNsFd(..., hostFd)` at `fabric_linux.go:575`
into `HostNetworkNamespacePath`; isolated case reuses that path with a
different target fd. When a pod mixes isolated + non-isolated containers
(e.g., ixia-c secondary via `network-mode: container:<ixia-primary>`),
`namespaceOwnerForContainer` keeps them together today — the isolated owner is
the primary's container id, so its secondaries naturally land in the same dev
netns; document as intended, verify via existing `validateMappedDefinition`
tests expanded for secondary.

### Launch boundary

`internal/directruntime/launch.go:135-139` ends in:

```go
operations.Exec(argv) // -> unix.Exec(...)
```

Insert `operations.EnterDeviceNetns(devNsPath) // setns(CLONE_NEWNET)` just
before `LimitOpenFiles`/`Exec`, guarded by `devNsPath != ""`. The operation
returns nil for legacy pods (no file). Implementation is
`setns(open(devNsPath), CLONE_NEWNET)` cloned from
`endpoint_namespace_linux.go:93-143`'s thread-locked Execute pattern; reuse
that helper rather than duplicating error handling.
`linuxLaunchOperations` already defines the seam; add to
`LaunchOperations` interface (breaking only the direct-runtime package, covered
by renderer tests).

### Reconciler adjustments (narrow)

- `addressDeviceLeg`: tolerate absent device leg due to being in another ns
  (already tolerated as "moved or renamed leg is the device's to own" at L711
  with `nilerr`). Expand comment to note isolated-ns cause.
- `deviceLegLink`/`EnsureVethPair` lookup: if direct `LinkByName` misses for
  isolated owner, consult `PeerIndex` resolution first before declaring loss.
- `SweepTransportState`: must not reap a pair whose Pod half died solely
  because its peer was moved; peers of isolated veths die only with the
  bound file, matching host-link semantics (`Pod-owned connectivity state shall
  be bounded by the Pod network namespace lifetime`).

## Alternatives Considered

- **Topology/CRD flag** `spec.networkIsolation` — correct long-term shape
  once two kinds need isolation differently; for now adds schema + cel +
  generated-client churn for one value.
- **Detect greediness dynamically** (sample `RTM_GETLINK` in sidecar, observe
  bulk departure) — races with reconcile interval, fragile across versions.
- **Wrap ixia entrypoint in unshare(1)** — needs image entrypoint mutation
  via renderer anyway, diverges from containerlab's process tree assumptions.

## Risks

- Moving legs into a bound netns requires privileged sidecar — already true
  for clabwire.
- Some kernels disallow moving a `NET_ADMIN`-restricted veth type; fallback is
  diagnostic surfacing the kernel denial as `NodeConditionConnectivity`.
- Same-pod secondaries that expect `lo`-only sharing may break; mitigation is
  deferral (documented limitation) and later promoting the isolation flag
  into plan metadata where topology grouping is normalized.

## Observability

- Condition `ManagementConnectivityReady=False`/message notes
  `"device isolated in private netns (kind=keysight_ixia-c-one)"` on cold pass;
  readiness path unchanged otherwise.
- Event `NetworkIsolated` emitted on first successful cross-namespace move.
