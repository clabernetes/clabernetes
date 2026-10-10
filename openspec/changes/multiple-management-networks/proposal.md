# Proposal: Multiple Management Networks

## Why

Containerlab 0.80 can define several management networks in one lab and attach each node to one
of them with the node-level `mgmt-net` key, optionally with a `macvlan` driver on any of them.
The c9s direct runtime realizes exactly one cluster-agnostic management subnet per namespace: the
compiler carries one `ManagementPolicy` per NodeProfile, the controller allocates one address per
node from one pool, the interposition mesh realizes one synthetic segment per Pod (one gateway
leg, one tunnel VNI, one peer directory), and the imported kinds receive one `MgmtNet` per
planning pass.

Until a real multi-network plan exists, c9s treats the new surface as follows: a single-entry
`mgmt:` list is carried as-is, more than one management network fails compilation with a clear
error, the new Docker-only `mgmt` fields (`driver`, `ipam`, `macvlan-*`, `tailscale`) are
accepted and ignored with a warning, and node `mgmt-net` selections are accepted and ignored.
That keeps every containerlab 0.80 topology parseable today and gives this proposal a defined
contract to land into.

## What Changes

- Accept the containerlab 0.80 management surface: `mgmt` as a list of networks, node
  `mgmt-net` selection with the regular inheritance order, and the per-network `network` name.
- Give each declared management network its own address policy (subnets, gateways, ranges) and
  its own deterministic controller allocation, with a per-network peer directory membership.
- Project one Pod-side mesh segment per management network the Pod's nodes join: one synthetic
  device leg and gateway pair per network, one tunnel VNI per network, and per-network filter
  rules; Pods that join several networks carry several segments.
- Feed each imported kind exactly the `MgmtNet` its node selected, so kind defaults that read
  the management network (for example the Cumulus `DOCKER_NET_V4_ADDR` environment or the SR-SIM
  transparent-management route statements) describe the selected network.
- Keep `macvlan` and `tailscale` management drivers unsupported with a compile diagnostic:
  macvlan requires a host-parent interface the cluster does not provide, and tailscale sidecars
  need image-level lifecycle c9s does not own. The diagnostic explains the substitute (the
  shared management mesh) instead of implying the fields are unknown.
- Keep one management interface per Node (`spec.mgmt-ipv4`/`mgmt-ipv6` pinning stays
  per-address, not per-network) and keep `spec.disableManagement` meaning "no management at all".

## Capabilities

### Modified Capabilities

- `management-mesh`: one namespace may carry several management segments; peer identity,
  allocation, and interposition contracts become per-network.
- `node-profiles`: `spec.mgmt` becomes the policy for the namespace's default network, and a
  per-network policy list defines every additional network a Node can join.
- `topology-resource`: the compiler accepts the management list form, rejects unrepresentable
  drivers with structured diagnostics, and renders the per-network policies.
- `device-planning`: the plan input carries the selected network identity per Node, and the
  imported lifecycle receives the matching `MgmtNet`.

## Impact

- Public `v1alpha1` NodeProfile and Node schemas gain per-network management policy and
  membership fields; generated CRDs, OpenAPI, and clients must be regenerated.
- The management controller's allocation, peer directory, and interposition contracts change
  shape (per-network pools and mesh segments); the plan schema version bumps so every Node
  re-plans once on upgrade.
- E2E suites gain a multi-network scenario (a macvlan-free pair of subnets with per-network
  reachability checks).
- No change to exposure, Links, or persistence semantics.
