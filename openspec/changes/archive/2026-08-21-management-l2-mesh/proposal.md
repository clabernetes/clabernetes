# Proposal: Management L2 Mesh

## Why

Interposed management currently gives every direct Pod a private island of the topology's
management subnet: a device's management address exists only inside its own Pod, and inter-node
management traffic works exclusively through name-based Services (ClusterIP + DNAT). Containerlab
semantics say the management network is one flat broadcast domain — labs that hardcode peer
management addresses (gnmic targets by IP, syslog/RADIUS/TACACS server IPs, NMS inventories
exported from containerlab), or that simply ping a peer's management address, silently fail
cross-Pod. The srl-telemetry-lab conformance run surfaced this as the one remaining parity gap of
the daemonless runtime.

A plain-docker spike (2026-08-21, evidence recorded with this change) validated the mechanics that
close the gap: an in-Pod bridge stitching the device leg, the gateway leg, and a VXLAN VTEP with
head-end replication gives devices real L2 adjacency on the management subnet — peer ARP resolves
the peer device's actual MAC, ping and TCP run device-to-device, and SR Linux adopts the bridged
leg exactly like the plain veth. Two kernel behaviors were identified and solved without bridge
netfilter (which the WSL2 kernel, and potentially other cluster kernels, do not ship): gateway
containment via bridge port isolation, and ARP-flux suppression via `arp_ignore=1`.

## What Changes

- The interposition realization gains a management L2 mesh: each interposed Pod bridges its
  synthetic device leg, its gateway leg, and a management VTEP (head-end replication FDB entries
  toward every peer Pod, discovered through one stable namespace-scoped headless Service and
  refreshed on the revision tick, so Node scale-out and scale-in never restart unaffected Pods). The management subnet becomes a real shared broadcast domain across the
  topology, exactly as containerlab presents it.
- The gateway keeps its own veth pair (today's router-leg shape) and is L2-isolated from the mesh
  via bridge port isolation; the gateway MAC and bridge MAC become deterministic, derived from plan
  data. `arp_ignore=1` and IPv6-disable on the mesh elements are unconditional baseline sysctls.
- The deviceplan interposition contract carries the mesh as data: mesh tunnel ID, the
  peer-discovery Service name, and the derived MACs. The controller derives the mesh tunnel ID per
  namespace above the Link allocation ceiling and owns the namespace-scoped peer-discovery Service.
- Inbound Service/DNAT reachability, outbound SNAT egress, and DNS behavior are unchanged; the mesh
  only carries intra-topology management traffic. There is no mode and no fallback: interposed
  management is always meshed.

## Capabilities

### New Capabilities

- `management-mesh`: the flat L2 management domain across a topology's direct Pods — bridge
  realization, gateway containment, peer FDB maintenance, and the plan contract that carries it.

### Modified Capabilities

- `direct-connectivity`: the sidecar's interposition realization changes shape (bridge between
  device leg, gateway leg, and management VTEP) and gains mesh reconciliation on the revision tick.
- `device-planning`: the interposition contract gains mesh fields (tunnel ID, peer-discovery name, derived MACs);
  management input carries the controller's mesh allocation.

## Impact

- `internal/deviceplan`: contract + input schema (additive), codec validation, mapper derivation.
- `controllers/node`: mesh tunnel-ID allocation, peer emission into `ManagementInput`.
- `internal/directruntime`: interposition realization (bridge, VTEP, FDB, sysctls, isolation),
  revision-tick peer refresh, sweep of mesh interfaces.
- No API (CRD) changes; no chart changes. Existing topologies converge to the mesh on Pod
  recreation after upgrade.
