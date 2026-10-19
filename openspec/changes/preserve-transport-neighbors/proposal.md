# Proposal: Preserve Transport Neighbors

## Why

Sidecar interposition renames the CNI-installed interface, which requires setting the link
down. The kernel flushes the link's neighbor entries on link down, including permanent ones,
and bringing the link back up does not restore them. Some CNI plugins reach the Pod gateway
only through a permanent neighbor entry: the Amazon VPC CNI routes the Pod default through the
link-local gateway 169.254.1.1 and installs that gateway as a PERMANENT neighbor on the Pod
interface. After interposition the entry is gone, the gateway is not re-resolved, and the Pod
loses default egress and cross-node reachability. On EKS every direct-runtime Pod stays in
Init.

The current requirement preserves the interface's addresses and routes only, so it permits
this loss even though it also requires Kubernetes transport to remain functional.

## What Changes

- Interposition captures the permanent neighbor entries of the CNI-installed interface before
  the rename and re-installs them on the preserved interface after it.
- The captured entries are recorded in sidecar-owned state before the flush, and a failed first
  pass re-installs them on the same interface before returning, so a retried pass or a restarted
  sidecar recovers them instead of observing the steady-state shape and skipping the restore.
- The recorded entries are re-asserted from that record on every later pass, so a device that
  flushes the shared neighbor table after boot does not strand the Pod without its gateway.
- The underlay-preservation requirement names permanent neighbor entries alongside addresses
  and routes, and gains a scenario for a CNI that reaches its gateway through a static
  neighbor.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `direct-connectivity`: the preserved CNI interface keeps its permanent neighbor entries.

## Impact

- `internal/directruntime/interposition_linux.go` and a new
  `internal/directruntime/transport_neighbors_linux.go`. No API, CRD, or chart changes.
- The captured entries are recorded in sidecar-owned state under the state directory before the
  flush, and the steady-state path re-asserts them from that record on every pass, so the
  failure window of the first pass no longer strands the Pod without its gateway.
- IPv6 neighbor entries are restored best effort: a device that disables IPv6 in the namespace
  makes them un-restorable, and the IPv4 transport must keep working exactly as before rather
  than fail closed over IPv6 state.
