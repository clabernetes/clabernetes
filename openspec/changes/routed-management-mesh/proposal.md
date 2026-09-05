# Proposal: Routed Management Mesh

## Why

The management mesh is a head-end-replicated L2 domain: every Pod floods each ARP or broadcast
to every peer Pod, learns peer MACs from traffic, and rediscovers the full peer set through a
headless Service DNS lookup every second. All three costs grow with the namespace. Per Pod and
per second the DNS answer carries every peer address, so the aggregate DNS load grows with the
square of the Pod count, and a single DNS answer cannot carry more than roughly 4,000 records,
after which the reconciler treats missing peers as departed and the mesh silently partitions.
A boot wave of N devices floods N squared encapsulated broadcasts. None of this is needed to
give devices a shared management subnet.

## What Changes

- The mesh keeps its outward contract (one shared management subnet per namespace, peers
  reachable device-to-device by address on any protocol and port, a strictly Pod-local gateway)
  but is realized as a **routed** overlay: each Pod forwards peer-bound management traffic over
  unicast VXLAN with static forwarding state, so no frame is ever flooded and no MAC is learned.
- The in-Pod bridge, the gateway pair, and bridge port isolation are removed. The device leg is
  one veth pair terminating on the sidecar's router leg, which answers ARP for every remote peer
  with the gateway identity and routes the packet to the peer's tunnel endpoint.
- Peer discovery moves off DNS. The controller publishes the namespace **peer directory** with
  each node's management addresses and current Pod address, sharded across a fixed set of
  ConfigMaps so a membership change rewrites one small object, and the kubelet fans it out once
  per worker node. The sidecar reads the mounted directory and installs one neighbor and one
  forwarding entry per peer, converging on change and on a slow periodic resync.
- **BREAKING (internal contract)**: the interposition mesh contract drops the peer-discovery
  transport name; the namespace-scoped headless discovery Service and the mesh-member Pod label
  are removed. The peer directory ConfigMap is replaced by its shards. Pods realized by the
  previous flooding shape and Pods realized by the routed shape cannot exchange management
  traffic on one VNI; an upgrade converges a namespace as its Pods roll.
- Device broadcasts other than ARP (gratuitous ARP, LLDP on the management port, DHCP) no longer
  cross Pods. The management path also reports fragmentation-needed to a device that sends
  oversized packets, instead of dropping them silently.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `management-mesh`: the L2 domain is presented to devices but realized as routed unicast; peer
  state comes from the published directory instead of DNS discovery; broadcast semantics narrow
  to ARP; convergence bounds change from the reconciliation tick to directory propagation.
- `direct-connectivity`: the interposition realization changes shape (single synthetic pair,
  routed tunnel endpoint, proxy ARP), the mesh peer state is maintained from the mounted
  directory, and the management segment clamp moves to the routed forward path.
- `device-planning`: the interposition mesh contract carries the tunnel identifier and gateway
  identity only; the peer-discovery transport name is removed.

## Impact

- `internal/directruntime`: interposition realization (pair shape, routed VTEP, proxy ARP, static
  neighbor and forwarding entries, transport table routes), segment clamp family, peer directory
  reading and sharding helpers, hosts rendering source.
- `internal/deviceplan`: mesh contract field removal and validation.
- `controllers/node`: peer directory sharding with Pod addresses, removal of the mesh discovery
  Service, legacy object cleanup.
- `internal/directpod`: projected directory volume, mesh-member label removal.
- `constants`: Service name and label constants removed.
- Documentation: architecture, installation, lab operations, and MTU guides.
- No CRD or chart changes.
