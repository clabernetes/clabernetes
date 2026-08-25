# Design: retire-vxlan-semantics

## Context

See proposal.md for motivation. Constraints that shape the approach:

- The `c9s.run/v1alpha1` API group is unreleased, so the `tunnelID` → `wireID` status-field
  rename is free now and impossible later.
- The wire already validates datagram sources (`dropForeignSource`) and dispatches link IDs
  inside one receiving sidecar; the mesh is kernel VXLAN on UDP 14789 while the wire is
  userspace UDP on 14790. Both facts are what make the scoping and ceiling changes safe.
- Plan bytes are covered by compatibility invalidation digests; changing the plan schema
  (connectivity value, field name) refreshes `compatibility/containerlab/baseline.json` and
  invalidates recorded conformance evidence by design.
- e2e goldens must be regenerated on a `make test-e2e-local`-style cluster (chart installed
  with debug log levels) or they silently flip.

## Goals / Non-Goals

**Goals:**

- Namespace-scoped wire-ID allocation with no cluster-wide List on the Link reconcile path.
- Mesh VNI derivation decoupled from the Link-ID ceiling.
- No VXLAN vocabulary left on the wire path (API, plan, Services, errors, comments, sweep).
- Docs and specs that state the namespace as the management scope and carry honest wire claims
  (underlay floor, carrier wording, VLAN transparency backed by e2e).

**Non-Goals:**

- Merging the heartbeat and link-state messages: link-state alone proves liveness, but it is
  unchunked (5 bytes/link), so past roughly 230 links per Pod pair the fixed-size heartbeat is
  the more robust liveness signal. Both stay; the rationale is documented in the wire constants
  and chunking is noted as the upgrade path if per-pair link counts ever demand it.
- Replacing the hand-rolled sendmmsg/recvmmsg batch I/O with `golang.org/x/net/ipv4`: a
  post-merge benchmark task, not worth reopening the most-measured path now. The 64-bit ABI
  assumption gets an explicit comment.
- Renaming management-mesh VXLAN vocabulary: the mesh genuinely is kernel VXLAN.

## Decisions

- **Allocation input**: `ResolveDesiredTunnelID` becomes `ResolveDesiredWireID` and takes the
  namespace Link list the reconciler already fetches for endpoint-conflict detection; the
  uncached `apiReader` cluster List is deleted. Retention and lexical-tie-break semantics are
  unchanged, just namespace-scoped.
- **Ceiling**: the 16,000,000 ceiling stays as an arbitrary sane bound (`maxWireID`) and the
  CRD keeps `Maximum=16000000` — the wire carries 32-bit IDs, so the bound is policy, not
  protocol.
- **Mesh VNI**: derived per namespace over `1 .. 2^24-1` (never 0) from the same SHA-256
  namespace hash; the base/span constants above the Link ceiling are deleted.
- **Renames**: `Link.status.tunnelID` → `wireID`; plan `Connectivity` value `"vxlan"` →
  `"wire"` (`ConnectivityWire`); plan `InterfacePlan.TunnelID` → `WireID`; fabric Service
  suffix `-vx` → `-wire`; "VXLAN Link" errors → "wire Link". The management mesh keeps
  `TunnelID`/VXLAN names and port constant 14789 gains a management-scoped name.
- **Pre-wire VTEP sweep**: deleted (`fabricVTEPLinkType` and the vxlan-type case in
  `SweepTransportState`) — it migrates state only unreleased branch builds could have left.
- **VLAN transparency**: the kernel RX path strips the outer 802.1Q/802.1ad tag into skb
  metadata before packet taps run (`skb_vlan_untag`), so the wire capture was silently
  delivering every tagged frame untagged. Fix: `PACKET_AUXDATA` on the capture socket and
  in-band tag reinsertion (frames read at a 4-byte offset so only the MAC prefix shifts).
  Frame headroom grows from 18 to 22 bytes so the reassembly bound is never the operative
  constraint; the real ceiling is the legs' transmit budget of one in-band 802.1Q tag above
  MTU (kernel AF_PACKET/veth behavior, matching common single-tag NIC budgets) — so
  single-tagged frames cross at full link MTU and full-MTU QinQ needs +4 bytes of link MTU.
  Proven by an isolated wire-pump unit test (single tag + stacked-802.1Q QinQ) and a
  linux-kind e2e phase (kernel vlan and QinQ sub-interfaces, full-budget DF pings).
- **Claim wording**: "carrier can never disagree with the datapath" becomes "control and data
  share one socket and one path, so they cannot diverge in endpoint or route" everywhere the
  claim appears (wire.go, link-wire guide, PR body, explainer site). "Any cluster" gains the
  ~1250-byte underlay floor caveat.

## Risks / Trade-offs

- Wide mechanical churn (generated CRDs/clients, goldens, digests) lands in one series —
  mitigated by regenerating with the standard targets and reviewing the diff, and by CI's
  verify-generated and compatibility gates.
- The VLAN e2e may expose a real capture-path bug (tag stripping); that is the point of
  writing it before the claim ships. Fix shape is known and small.
- Namespace-scoped IDs mean different namespaces now commonly hold identical wire IDs; any
  observer assuming global uniqueness (none known in-tree) would be misled — the Link docs
  state namespace scope explicitly.
