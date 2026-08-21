# Sidecar-Owned Connectivity: Remove the Host-Endpoint Daemon

## Why

Direct-node connectivity currently splits ownership between the pod and a privileged `hostNetwork` DaemonSet: the daemon realizes the management loop, the cross-pod fabric, and host links, forcing every worker to run root-privileged node-wide machinery and giving connectivity a node-scoped failure domain. Spikes 1 and 2 (recorded in the exploration document; reproduced against unmodified SR Linux 25.10, SR-SIM 25.10.R1/26.7.R1, and cEOS 4.33.1F, with the nftables translation backend validated end to end in task 1.2 evidence) proved the cXDP pattern: a pod-local connectivity sidecar can interpose a synthetic management interface carrying the topology-policy management address while fully preserving the Pod's CNI identity — and a preserved underlay is exactly what the original in-pod fabric design lacked. This change goes all the way in one step: the sidecar becomes the only owner of pod connectivity — management, fabric, and host links — and the host-endpoint daemon is deleted from the tree. No modes, no fallback, no phased migration.

## What Changes

- **BREAKING**: The host-endpoint daemon is removed entirely: the `internal/hostendpoint` package, its DaemonSet chart template, its CLI subcommand, its unix socket and the sidecar's socket mount all disappear. No compatibility path is kept.
- The connectivity sidecar interposes management before any device container starts: the CNI interface is preserved under a sidecar-owned name, a synthetic device-leg interface carries the controller-allocated management address, and a sidecar-owned policy-routing table plus nftables translation table (hook priorities ahead of x_tables) realize transport protection, outbound SNAT for both traffic shapes, and declared-port DNAT.
- **BREAKING**: The Pod-address-as-management-identity fallback is removed. Every direct node's management identity is controller-allocated; topologies without a management policy allocate from containerlab's default management subnet with containerlab's gateway convention, restoring exact containerlab addressing semantics. Management configuration renders at plan time through each kind's own containerlab templates.
- Cross-pod fabric (vxlan and slurpeeth) is realized by the sidecar inside the pod on the preserved underlay, using the existing plan vocabulary (interface plans with Service-name peer transports and tunnel IDs). The device-facing link interfaces and their tunnel plumbing live and die with the pod network namespace.
- Host links are realized by the sidecar through its existing read-only host network-namespace mount: veth pairs with one end placed in the worker namespace. Because a veth pair dies with either end's namespace, forced pod deletion leaves no host residue, eliminating the daemon's orphan sweep by construction.
- Device plans carry a derived, vendor-neutral interposition profile (device-leg name, MAC, gateway inputs) sourced from the pinned containerlab registry via `deviceplan`; all spike-derived hardening (checksum offload, forwarding scoping, `accept_local`, chain-priority translation, state re-assertion) is unconditional baseline behavior with no vendor conditionals anywhere in c9s.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `direct-connectivity`: management identity is realized only by sidecar interposition (daemon loop requirement removed); fabric and host-link realization moves from the worker daemon to the pod sidecar; cleanup guarantees become namespace-lifetime guarantees; the "kind-opaque management loop" language is replaced.
- `device-planning`: plans carry a derived vendor-neutral management-interposition profile from the pinned containerlab registry, and management inputs are always fully allocated at plan time (containerlab default-subnet semantics when no policy is declared).
- `direct-runtime-conformance`: the compatibility evidence matrix extends to interposition, sidecar fabric, and host links per supported kind; release claims follow that evidence.

## Impact

- `internal/hostendpoint/` — deleted, with its tests.
- `charts/clabernetes/templates/host-endpoint-daemonset.yaml` and related chart values/RBAC — deleted; golden chart fixtures regenerate.
- `cmd/clabernetes/cli` — the hidden `host-endpoint-daemon` subcommand is removed.
- `internal/directruntime/` — interposition stage, in-pod fabric realization, host-link realization through the host-namespace mount, nftables translation (landed in task group 1), re-assertion, and readiness conditions.
- `internal/deviceplan/` — interposition profile derivation, default-management-subnet completion, codec additions.
- `internal/directpod/` — connectivity sidecar loses the daemon socket mount; host-namespace mount becomes standard for topologies that need it.
- `controllers/node/` — default management-subnet allocation; no daemon-related projections.
- No API schema changes: `TopologySpec` and `NodeStatus` are untouched; the management policy keeps its meaning with a containerlab-parity default.
- `docs/` — architecture and management documentation rewritten for the daemonless model.
- e2e and unit suites — daemon expectations replaced with sidecar expectations; multi-worker recovery keeps its coverage against the new owner.
