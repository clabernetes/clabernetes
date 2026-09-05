# Design: Routed Management Mesh

## Context

See proposal.md for motivation. The current realization (change `management-l2-mesh`) is a
three-port bridge per Pod (device leg, isolated gateway leg, isolated VXLAN VTEP), head-end
replication FDB entries toward every peer Pod, MAC learning, and a per-tick DNS lookup of a
namespace-scoped headless Service. Constraints that shape the replacement:

- Kubernetes CNIs do not deliver Pod multicast across nodes, so replication has to stay unicast.
- The device contract must not change: the device sees the same interface name, MAC behavior,
  management address, connected subnet route, and Pod-local gateway. Peers must stay reachable
  by management address on any protocol and port, in both directions, with source identity
  intact.
- Every Pod already mounts a namespace-scoped peer directory ConfigMap that the kubelet syncs
  once per worker node; the sidecar already re-asserts hosts entries from it every tick.
- The wire format should stay VXLAN on UDP 14789: the transport filter accepts, NetworkPolicy
  guidance, and the MTU derivation all key on it.
- Device MACs are not knowable in advance (several kinds rewrite their management MAC), so the
  design must not depend on learning or pinning device MACs.

## Goals / Non-Goals

**Goals:**

- Constant per-packet cost and no flood: no zero-MAC head-end entries, no learning, no ARP
  across Pods.
- Per-Pod state of one route plus one neighbor entry and one forwarding entry per peer.
- Peer state distributed once per worker node per change, with no DNS in the path and no
  message-size ceiling.
- Path MTU behavior at least as good as today on every worker layout, including cross-worker.

**Non-Goals:**

- Broadcast or multicast application traffic across Pods (only ARP is emulated).
- Sub-second convergence after a peer Pod reschedule (bounded by directory propagation).
- IPv6 management conformance beyond parity with the current optional IPv6 leg addressing.

## Decisions

**D1 — Routed overlay with a synthetic per-peer link-layer identity.** Each Pod's VTEP is a
routed interface with learning off and a MAC derived from the Pod's own management IPv4
address. For each peer the sidecar installs a permanent neighbor entry (peer management address
to the peer's derived MAC) and a forwarding entry (that MAC to the peer's Pod address). The
inner frame therefore arrives addressed to the receiving VTEP's own MAC and enters the IP stack
as a host packet, which routes it to the device through the router leg. No peer device MAC is
ever needed. Alternatives: static FDB toward device MACs (needs MAC knowledge; rejected), NAT
to Pod addresses (loses source identity and collides with sidecar transports on the same Pod
address; rejected), GENEVE or IPIP over UDP (changes the wire format and every port-dependent
contract for no gain; rejected).

**D2 — Proxy ARP on the router leg instead of L2 adjacency.** The device keeps its connected
subnet route and ARPs for peers. The router leg answers for any address whose route leaves
through the VTEP, with the gateway MAC and zero proxy delay. Unknown addresses still route to
the VTEP, fail neighbor resolution there, and yield host-unreachable instead of a silent drop.
The bridge, gateway leg, and port isolation disappear; the device leg is one veth pair whose
far end is the router leg.

**D3 — Peer state from the published directory, not DNS.** The controller already renders a
namespace peer directory; it now also carries each node's Pod address, sourced from the Pod
cache by the direct-node UID annotation. The sidecar reads the mounted directory, derives the
peer MACs, and converges neighbor, forwarding, and NDP-proxy entries when the directory changes
and on a slow periodic resync. The headless discovery Service and the mesh-member label are
removed. Alternatives: an EndpointSlice informer per sidecar (one API watch per Pod; rejected as
the API-server fan-out is per Pod instead of per node), a node-resident agent (new privileged
component; rejected).

**D4 — Fixed shard count for the directory.** The directory is split by a stable hash of the
node name across a fixed number of ConfigMaps projected into one volume directory. A fixed
count keeps the Pod template static (the projected volume lists every shard, all optional), so
growth never recreates Pods; a membership or Pod-address change rewrites one shard. The legacy
single ConfigMap is removed by the controller.

**D5 — Segment clamp on the routed forward path.** The TCP MSS clamp moves from the bridge
family to an `inet` forward chain matching ingress on the router leg and the VTEP. With L3
forwarding the kernel also emits fragmentation-needed for oversized DF packets, so devices doing
path MTU discovery adapt on their own; the clamp remains for devices whose port size is fixed.

**D6 — Transport table shape.** The sidecar-owned policy table carries the Pod's own management
address as a host route via the router leg and the management subnet via the VTEP. The device
rule (ingress on the router leg) and the own-address rule already select this table, so peer
traffic from the device and inbound traffic from the VTEP both resolve without touching the main
table a device may rewrite.

**D7 — Kernel-held addresses cross the device leg.** A single-namespace device (Linux kinds,
SONiC, vrnetlab containers) leaves the management address on the device leg, where the pod
kernel terminates it. Inbound traffic for such an address from the mesh tunnel endpoint or from
the Kubernetes transport is steered into the transport table ahead of the local lookup, so it
crosses the synthetic pair and arrives on the device leg exactly where a shared segment would
have delivered it: interface-scoped device rules keep seeing it (vrnetlab forwards management
ports to its virtual machine from that leg). To express this the sidecar re-homes the kernel's
local lookup from priority 0 to a priority right behind those ingress rules, which also makes
the shape immune to devices that move the local lookup themselves (SONiC re-inserts it at
1001, which otherwise loops replies back into the transport table). Locally originated and
device-leg traffic never matches the ingress rules and cannot loop. The router leg carries the
gateway address without a kernel prefix route so it never competes with the device leg's
connected route for kernel-originated replies. Early demux is disabled in the Pod namespace:
it would hand a packet to an existing local socket before the routing decision, and the
forward path then drops a socket-owned packet silently, which would swallow every reply into a
kernel-originated management connection on the tunnel endpoint. Inbound Pod-address
translated flows carry the gateway as their client identity on every device (the device
answers over its connected route, as under Docker port publishing). For a kernel-held address
the gateway is also a local address, and a device that forwards management ports to a nested
guest (vrnetlab) returns the guest's reply through the pod kernel's forwarding path, where it
would be delivered locally and never meet the translation state on the sidecar legs. The
sidecar therefore hairpins gateway-bound traffic of a kernel-held address across the pair: a
rule ahead of the local lookup selects the transport table, which carries the gateway as a
host route via the device leg, unless the packet entered on the router leg, where it is
delivered locally. The reply re-enters on the router leg, in the sidecar's zone, and the
translation reverses there.

**D8 — The sidecar legs track connections in their own conntrack zone.** A packet the sidecar
routes between its legs and the device leg crosses netfilter twice in one namespace. In one
zone the first crossing confirms the connection and a device's own translation bound to its
leg can no longer bind. The bridged shape never had this problem because bridged frames
skipped netfilter until the device leg. The sidecar legs and locally originated traffic use
zone 1; the device leg and everything else stay in the default zone. The sidecar's own
translations and their replies stay consistent within its zone.

**D9 — IPv6 management is best effort per Pod.** IPv6 mesh state (rules, routes, neighbor and
proxy entries) is installed only while the router leg actually carries the IPv6 gateway. A
device that disables IPv6 in the shared namespace (EOS) leaves the IPv4 mesh untouched instead
of failing the sidecar closed. The device leg's IPv6 address is assigned before boot only, like
the IPv4 one: a device that takes the addresses into its own stack keeps ownership.

**D10 — Readiness over HTTP and paced re-assertion.** The kubelet's exec readiness probe ran
the runtime binary every second in every Pod, which was the dominant per-Pod cost. The sidecar
now answers startup and readiness probes over HTTP on the Pod address (TCP 14791, kept
reachable through the transport filter), and re-asserts its owned namespace state on every
tick only while the device boots, then every ten seconds or when the peer directory changes.
Sysctl writes compare before writing.

**D11 — Imported packages see the management subnets.** The planning runtime presents the
containerlab management network with the subnets derived from the allocated addresses, not only
the gateways; vrnetlab-based kinds generate their boot configuration from
`DOCKER_NET_V4_ADDR`/`DOCKER_NET_V6_ADDR` and fail to boot without them.

**D12 — The direct watchdog pass is paced by readiness.** Every Node re-runs its direct
pipeline on a timer as a backstop for a dropped Pod event and for edits of referenced payload
objects the watches cannot see. Each pass reads the Node, its probe and entropy Secrets, and
its plan ConfigMaps through the API server (uncached, by design, to avoid create loops on
objects outside the label-filtered cache), so at one pass per minute the manager's cost at
rest grows by one pass per second per sixty Nodes. A Node that is still converging keeps the
minute; a ready Node backs off to five minutes with a minute of jitter so a deploy wave does
not keep hitting the API server in step. Edits of a referenced payload object that only the
watchdog can see therefore take up to five minutes to apply on a ready Node.

**D13 — The device leg's administrative state belongs to the device after creation.** The
sidecar brings the device leg up once, when it creates the pair before the device starts, and
never again. SR Linux takes the leg down, renames it to `mgmt0`, and brings it back up while
it boots, and the kernel refuses to rename an interface that is up; a re-assertion pass that
set the leg up in that window (one pass per second while the device boots) broke the rename,
after which the pod kernel answered for the address itself and the device's management
plane never came up while readiness stayed green.

## Risks / Trade-offs

- [Directory propagation lag after a Pod reschedule, up to the kubelet sync period] → the
  rescheduled device needs comparable time to boot; the periodic resync and the kubelet's
  watch-based cache keep the bound at roughly the sync period, documented as such.
- [Old and new realizations cannot interoperate during an upgrade] → the runtime image change
  rolls every Pod; documented as a per-namespace cutover.
- [A device relying on gratuitous ARP or LLDP across Pods] → documented limitation; ARP request
  and reply semantics are preserved, which is what management stacks depend on.
- [Two Pods for one node during an unusual rollout overlap] → the directory carries the newest
  non-terminating Pod; the Recreate strategy makes overlap rare and transient.
- [nftables `inet` forward hook unavailable on a minimal kernel] → same tolerance as today: the
  clamp is skipped with the same unsupported-error classification.

## Migration Plan

1. Deploy the manager image; the runtime image change rolls every device Pod.
2. The controller creates the directory shards, deletes the legacy directory ConfigMap and the
   discovery Service on the next namespace reconcile.
3. Rollback is the reverse image change; the previous controller recreates the Service and the
   single ConfigMap on its next reconcile.
