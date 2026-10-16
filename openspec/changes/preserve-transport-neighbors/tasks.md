## 1. Implementation

- [x] 1.1 Capture permanent neighbor entries of the CNI-installed interface before it is set down for the rename
- [x] 1.2 Re-install the captured entries on the preserved interface after the rename

## 2. Validation

- [x] 2.1 Add a test that fails without the restore: a permanent neighbor on the CNI interface is present on the preserved interface after interposition
- [x] 2.2 `make lint`
- [x] 2.3 Verify on EKS with the Amazon VPC CNI: the 169.254.1.1 PERMANENT entry survives interposition, Pods leave Init, and cross-node Pod IP reachability works without manual repair

## Validation evidence (2026-10-16, EKS 1.36: 5 amd64 nodes, Amazon VPC CNI)

- Without the change (v0.10.0): `TestEnsureInterpositionConvergesIsolatedNamespace` with the
  new assertion fails; the preserved interface has no neighbor entries after the cold pass.
- With the change: the test passes, and `go test ./internal/directruntime/...` passes.
- On EKS with the change: a two-node topology placed on different nodes leaves Init within
  seconds, `ip neigh show` lists `169.254.1.1 dev c9s0 lladdr ... PERMANENT` without manual
  repair, and Pod IP to Pod IP ping across nodes succeeds with no loss.

## Validation evidence (2026-10-16, EKS 1.36: 5 amd64 nodes, Amazon VPC CNI)

- Without the change (v0.10.0): `TestEnsureInterpositionConvergesIsolatedNamespace` with the
  new assertion fails; the preserved interface has no neighbor entries after the cold pass.
- With the change: the test passes, and `go test ./internal/directruntime/...` passes.
- On EKS with the change: a two-node topology placed on different nodes leaves Init within
  seconds, `ip neigh show` lists `169.254.1.1 dev c9s0 lladdr ... PERMANENT` without manual
  repair, and Pod IP to Pod IP ping across nodes succeeds with no loss.
