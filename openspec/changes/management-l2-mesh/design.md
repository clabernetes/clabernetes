# Design: Management L2 Mesh

## Context

The daemonless interposition runtime (change `sidecar-mgmt-interposition`) leaves the management
subnet as per-Pod islands: peer management addresses are unreachable across Pods, only name-based
Service reachability works. Containerlab semantics — and real labs (hardcoded telemetry targets,
syslog collectors, ping) — require the management network to be one broadcast domain.

A plain-docker spike (evidence `evidence/spike-mesh-mechanics.md`) validated all mechanics on
2026-08-21, including a real SR Linux 25.10 adopting the new leg shape and bidirectional
hardcoded-address management traffic. Spike-established constraints that bind this design:

- The WSL2 kernel (and therefore an unknown fraction of cluster kernels) has no
  `NF_TABLES_BRIDGE`: gateway containment cannot rely on bridge netfilter. Bridge **port
  isolation** (core bridge feature) is the containment primitive.
- ARP flux is real: without `arp_ignore=1`, every same-namespace interface answers for the gateway
  and remote namespaces answer through bridge-self delivery. Three distinct MACs answered a single
  gateway ARP until scoped.
- A bridge inherits the lowest port MAC dynamically; the bridge MAC must be pinned explicitly or
  it churns with port changes.
- SR Linux adopts a bridged veth leg identically to a plain one (lease synthesis, `mgmt0.0`
  addressing, sshd reachability confirmed).

## Goals / Non-Goals

**Goals:**

- Peer management addresses reachable device-to-device across Pods, any protocol, both directions.
- Gateway behavior indistinguishable from today: local resolution, local egress, single ARP reply.
- Zero new node-resident components; the sidecar realizes everything Pod-locally.
- No API changes, no chart changes, no mode: interposed management is always meshed.

**Non-Goals:**

- IPv6 management mesh (IPv4-first like the rest of interposition; the L2 mesh incidentally
  carries any ethertype, but no IPv6-specific validation ships here).
- Broadcast/multicast application traffic guarantees beyond ARP (head-end replication carries
  them, but conformance validates ARP + unicast).
- Off-cluster routing of the management prefix (unchanged non-goal).

## Decisions

**D1 — In-Pod shape: pure-L2 bridge with three port classes.** The synthetic device pair stays
(`eth0` device leg ↔ pod-side leg), the gateway keeps today's `c9sr0` routing anchor but its peer
leg becomes a bridge port, and a management VTEP joins as the third port. The bridge carries no IP
and a pinned derived MAC. All L3 machinery — policy rules keyed on `iif c9sr0`, NAT, sysctls,
route re-assertion — is untouched in shape; only the L2 path between device and gateway now
traverses the bridge. Rationale: minimal disruption to the proven interposition logic; the bridge
is a pure additive L2 element.

**D2 — Gateway containment by port isolation, not netfilter.** The gateway leg port and the mesh
VTEP port are both marked isolated; the device leg port is not. Isolated ports cannot exchange
frames, so gateway↔mesh traffic is structurally impossible in either direction: a flooded gateway
ARP reaches remote Pods but can never reach a remote gateway port, and frames addressed to the
gateway MAC arriving from the mesh are dropped at the bridge. Port isolation is a core bridge
feature present on every supported kernel. The gateway MAC is deterministic across the topology
(derived from plan data) so even a hypothetical leak resolves to the same identity.

**D3 — ARP-flux scoping is unconditional baseline.** `arp_ignore=1` and IPv6-disable on every
sidecar-owned mesh element (bridge, gateway legs, VTEP, device pod-side leg) join the existing
sysctl baseline. The spike showed exactly-one gateway ARP reply with these set and three without.

**D4 — Mesh transport reuses the fabric substrate; peers are discovered, not planned.** The mesh
VTEP uses the same VXLAN service port as Link fabric, with head-end replication: one all-zeroes
FDB entry per peer Pod address, unicast learning enabled. The peer set is deliberately NOT plan
data — the live-revision machinery is interface-scoped, so a planned peer list would recreate
every Pod in the namespace on any Node addition or removal. Instead the controller maintains one
namespace-scoped headless Service (`publishNotReadyAddresses`, selecting all direct device Pods
via a dedicated Pod label) and the contract carries only that stable Service name; the sidecar
resolves it on the revision tick and reconciles FDB entries exactly (append new peers, delete
stale head-end entries and learned entries pointing at departed Pod addresses, excluding its own
Pod address). Node scale-out and scale-in therefore converge with zero Pod restarts. The mesh
tunnel ID is derived per namespace into the VNI range above the Link allocator's ceiling
(16,000,000), making Link collisions impossible by construction.

**D5 — Contract and input are additive plan data.** `ManagementInterposition` gains
`Mesh { TunnelID, GatewayMAC, PeerService }`; the controller emits it via `ManagementInput` next
to the existing `InboundPorts`, with the tunnel ID and deterministic gateway MAC derived from the
namespace (the namespace is the management L2 domain, matching the namespace-wide management
address allocation). Codec validation is additive; no schema-version bump. The mapper stays
kind-agnostic: mesh data is pure controller policy, no vendor variance exists.

**D6 — Everything else is deliberately unchanged.** Inbound Pod-address DNAT, outbound SNAT,
transport preservation, DNS, readiness conditions, and the fabric/host-link realizations do not
change. The mesh MTU follows the same underlay-minus-overhead clamp as fabric. Name-based and
address-based reachability coexist.

## Risks / Trade-offs

- [Head-end replication scales O(peers) per broadcast] → Management ARP volume is trivial; labs of
  hundreds of nodes flood one small frame per peer per ARP. Acceptable; unicast learns immediately.
- [A stray in-cluster sender could inject into the mesh VNI] → Same exposure class as Link fabric
  VNIs today; tunnel IDs share one allocator and namespace scoping. No regression.
- [Devices that bridge-loop their management leg] → The device leg is a normal access port;
  spanning tree stays off exactly as containerlab's management bridge runs. A misbehaving device
  can already disrupt only its own management plane; unchanged blast radius.
- [Kernels lacking bridge port isolation (pre-4.18)] → Below the runtime's kernel floor;
  realization fails closed with a precise readiness reason if the flag is rejected.
- [Duplicate gateway address across Pods] → By design; containment (D2) plus deterministic MAC
  (D5) make it unobservable. Validated in the spike with three concurrent gateways.

## Migration Plan

Single release: Pods recreate under the new plan revision and converge to the mesh; no daemon, no
chart, no API surface involved. Rollback is the previous c9s version (islands model returns).

## Open Questions

- None blocking. Whether slurpeeth-flavored topologies want a TCP-based mesh realization can be
  revisited if a cluster ever cannot carry VXLAN between Pods; the contract carries no transport
  flavor today on purpose.
