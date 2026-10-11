# Design: Multiple Management Networks

## Context

Containerlab 0.80 makes `mgmt` a list and lets a node select its network with `mgmt-net`. c9s
pinned 0.80 while realizing exactly one management network per namespace, so this change must
define how several management domains fit the direct runtime before implementing anything.

## Goals / Non-Goals

- Goals: parse and carry the full containerlab 0.80 management vocabulary; support N networks
  per namespace with per-network addressing and peer reachability; keep the imported kind
  contract unchanged (one `MgmtNet` per planning pass).
- Non-Goals: macvlan management networks (require a host interface), tailscale management
  integration (sidecar lifecycle c9s does not own), and per-network external-access semantics
  (exposure is Kubernetes Service policy in c9s).

## Decisions

### D1. One mesh segment per management network

The interposition mesh is segment-shaped today: one device leg (`mgmt0`), one router leg with the
gateway identity, one tunnel VNI, one VTEP, and one filter chain. Multiple networks repeat that
segment per network on every Pod that joins them, each with its own deterministic gateway MAC
and VNI derived from the network's identity, and each with its own per-network peer directory
membership. The sidecar contract gains a list of segments; the cold-start interface set, route
assertion, and filter tables become per-segment.

### D2. The Node carries its network, not the network's policy

The compiler flattens `mgmt-net` through the regular inheritance order and emits the node's
selected network name on the Node (a per-node field the Node CRD owns). Per-network policy
(subnets, gateways, ranges) lives on the NodeProfile, mirroring today's `spec.mgmt` shape for
the namespace's default network plus a named list for additional networks. The controller
resolves every Node's policy from the selected profile at plan time, so a Node never stores
another profile's policy.

### D3. Allocation becomes a per-network pool walk

`compileNamespaceManagementIdentities` walks one pool today. Multiple networks introduce
independent pools, each honoring pinned addresses within its own subnet and rejecting
collisions only within that network. Two nodes in different networks may share an address, which
also means the peer directory and the mesh neighbor/forwarding entries must be keyed per
network, not per address.

### D4. The imported lifecycle sees the selected network only

`Adapter.Evaluate` builds one `WithMgmtNet(...)` today. Per node, the adapter passes the
`MgmtNet` of the node's selected network (with its subnets, gateways, and `DriverOpts`) so
kind-owned management defaults (`DOCKER_NET_V4_ADDR`, transparent-management routes, and the
`envNokiaSros*` address templates) describe the network the lab author intended. The plan's
management input carries the network identity so post-deploy replay rehydrates the same facts.

### D5. Unsupported drivers fail compilation with a substitution note

`driver: macvlan` and `mgmt.tailscale` declare Docker-host mechanics c9s cannot realize. The
compiler rejects them with a diagnostic that names the substitute (the shared management mesh)
and the accepted address-policy fields, mirroring how `runtime` and `stages` are rejected today.

### D6. Names stay per-network

Node names, aliases, and chassis component names resolve per network today through one peer
directory. With several networks a name can exist in every network the node joins; the peer
directory entry carries a per-network address map instead of one address, and DNS/`/etc/hosts`
rendering picks the network the requester's interface belongs to when it can, else the default
network.

## Risks / Trade-offs

- Multiple segments per Pod multiply the sidecar's kernel state (one device leg, VTEP, gateway
  leg, and filter chain per network) and its per-tick reconciliation cost; sharded peer
  directories and per-network filtering keep the growth linear in the networks a Pod actually
  joins.
- Sharing an address between networks is legal in containerlab but confuses tooling that treats
  a management address as globally unique; the peer directory contract must keep networks
  distinguishable to stay honest about that.
- The plan schema version bumps once, re-planning every Node on upgrade; the interposition
  contract change makes Pods realized before and after the upgrade unable to exchange management
  traffic until they roll, which the proposal calls out as the acceptance cost.

## Open Questions

- Should the default network for tools/sidecar containers follow containerlab's rule (the first
  declared network) or stay the namespace's existing single policy for compatibility?
- How should `inboundPorts` interposition pick a network when a Node joins several?
