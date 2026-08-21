# Tasks: Management L2 Mesh

## 1. Plan contract and input

- [x] 1.1 Extend `ManagementInterposition` with the `Mesh` contract (tunnel ID, deterministic
      gateway MAC, sorted peer transport names) and `ManagementInput` with the matching
      controller-allocated mesh data; additive codec validation and normalization; unit tests.
- [x] 1.2 Derive the mesh contract in the mapper for namespace-owning interposed Nodes only,
      kind-agnostically; unit tests including group-secondary exclusion.

## 2. Controller allocation

- [x] 2.1 Allocate the mesh tunnel ID per namespace from the same allocation space as Link tunnel
      IDs and emit it, the derived gateway MAC, and the peer transport-name set into
      `ManagementInput`; unit tests for allocation stability and peer-set correctness.

## 3. Sidecar realization

- [x] 3.1 Realize the bridge shape in `EnsureInterposition`: pure-L2 bridge with pinned MAC,
      gateway leg re-parented as an isolated bridge port, device pod-side leg as a normal port,
      and the mesh sysctl baseline (`arp_ignore=1`, IPv6 off) — idempotent and re-asserted on the
      revision tick.
- [x] 3.2 Realize the management VTEP as an isolated bridge port with head-end replication FDB
      toward every resolved peer, exact stale-entry removal, MTU clamped like fabric, and
      re-resolution on the revision tick; unit tests with the netns-isolated harness.
- [x] 3.3 Fail closed with a precise readiness reason when bridge port isolation or VTEP creation
      is rejected by the kernel.

## 4. Validation

- [x] 4.1 `make lint`, `make test`, compatibility verify; extend the isolated-netns interposition
      test to cover the bridge shape, gateway single-reply behavior, and FDB reconciliation.
- [x] 4.2 Kind-cluster conformance on the srl-telemetry-lab: hardcoded-address device-to-device
      management traffic (ping + TCP both directions, SR Linux and linux kinds), peer reschedule
      convergence, force-delete residue check, and the full existing lab still green
      (gnmic/prometheus/grafana/alloy/loki/traffic). Record evidence with the change.
