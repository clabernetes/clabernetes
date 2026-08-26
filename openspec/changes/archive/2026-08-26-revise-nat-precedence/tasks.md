# Tasks: revise NAT precedence

## 1. Implementation (already landed on direct-c9s)

- [x] 1.1 Hook the interposition dstnat chain at priority −90 (after device-owned x_tables/
      iptables-nft PREROUTING at −100), leaving srcnat at 90; document the asymmetry at the
      constants (`internal/directruntime/nat_linux.go`, commit d05f115).

## 2. Validation (completed live on k8s-vms, 2026-08-26)

- [x] 2.1 Unit: `go test ./internal/directruntime/` (priority constants exercised by
      `TestEnsureInterpositionNAT` table).
- [x] 2.2 Regression: `nokia_srlinux` smoke (no device NAT) — ping and Pod-IP SSH unchanged.
- [x] 2.3 Fixed classes: `cisco_n9kv` and `cisco_cat9kv` Pod-IP SSH authenticates (previously
      stalled after password submission); `mikrotik_ros` device DNAT reachable end to end with
      an unscoped rule.

## 3. Spec sync

- [x] 3.1 Sync the direct-connectivity delta into `openspec/specs/direct-connectivity/spec.md`
      and archive this change.
