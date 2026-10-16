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
- Limitation: the entries are captured in memory during the first interposition pass. If that
  pass fails after the link is set down and before the entries are restored, a restarted
  sidecar observes the steady-state shape and does not restore them; the Pod must be
  recreated. Persisting the captured entries in sidecar-owned state, or re-asserting them from
  the steady-state path, would remove this ceiling.
