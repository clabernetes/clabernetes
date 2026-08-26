# Tasks: retire-vxlan-semantics

## 1. Namespace-scoped wire identity

- [x] 1.1 Scope allocation to the namespace: `ResolveDesiredTunnelID` → `ResolveDesiredWireID`
      over the already-fetched namespace Link list; delete the uncached cluster-wide List in
      `controllers/link/reconcile.go`; update comments and the Accepted status message; rename
      `maxVXLANTunnelID` → `maxWireID`. Update allocator/reconciler unit tests.
- [x] 1.2 Decouple the mesh VNI from the Link ceiling: derive over the full 24-bit VNI space
      (never 0) in `controllers/node/direct.go`; delete the base/span constants; update tests.

## 2. Vocabulary sweep

- [x] 2.1 API: rename `Link.status.tunnelID` → `wireID` with wire-accurate docs
      (`apis/v1alpha1/link.go`); regenerate deepcopy, CRDs, assets, clients, openapi.
- [x] 2.2 Plan schema: `ConnectivityVXLAN "vxlan"` → `ConnectivityWire "wire"`,
      `InterfacePlan.TunnelID` → `WireID` (`internal/deviceplan`, `controllers/node`);
      keep mesh `TunnelID` (genuinely a VNI); update affected fixtures.
- [x] 2.3 Runtime: consume the renamed plan fields, "VXLAN Link" error → "wire Link", delete
      the pre-wire VTEP sweep (`fabricVTEPLinkType`), fix stale comments
      (`internal/directruntime`).
- [x] 2.4 Services: fabric Service suffix `-vx` → `-wire` (`controllers/node/service.go`),
      checking the DNS label length budget (no proactive check exists; the Kubernetes 63-char
      name validation stays the backstop, ceiling moves 60 → 58 chars of node name); rename
      the 14789 constant to a management-mesh name; regenerate unit and e2e goldens.

## 3. Honest claims

- [x] 3.1 Wire code: reword the "can never disagree" comment; bump frame headroom 18 → 22
      (QinQ); document the heartbeat/link-state rationale and the link-state size ceiling; add
      the 64-bit ABI comment on the mmsghdr layout.
- [x] 3.2 VLAN e2e: linux-kind endpoints exchange single- and double-tagged traffic across the
      wire; the capture path did strip tags (kernel RX moves the outer tag to skb metadata
      before packet taps) — fixed with PACKET_AUXDATA + in-band reinsertion, proven by an
      isolated wire-pump unit test.

- [x] 3.3 Management reachability for subnet-only devices: SNAT inbound DNAT'd flows to the
      Pod-local gateway (`ct status dnat` rule in the interposition table); found on the real
      cluster where SR OS answered on-subnet probes but black-holed LB/ClusterIP/Pod-IP
      clients (management VPRN has only the connected route). Spec requirement amended, docs
      and site updated, proven live by hand-rule before implementing.

## 4. Docs and specs

- [x] 4.1 Fix stale docs: `installation.mdx` (kernel VXLAN is for the management mesh; wire is
      plain UDP), `release-notes/0.9.mdx` (wire, namespace-scoped IDs, namespace-wide
      management), `guides/nokia-srsim.md`, CRD reference pages for `wireID`.
- [x] 4.2 Tighten `guides/link-wire.md`: carrier wording, underlay floor caveat, VLAN claim
      matching what e2e proves.
- [x] 4.3 Sync the three delta specs into `openspec/specs/`, aligning the management-mesh
      Purpose wording with the namespace scope.

## 5. Validation

- [x] 5.1 `make lint`, affected package tests, `make test`, `make test-race`,
      `make verify-generated`, `make check-docs`, compatibility gate.
- [x] 5.2 `make test-e2e-local` including the new VLAN e2e; regenerate goldens only on the
      e2e-deploy-style cluster. (12 tests pass, 4 env-gated skips; renamed `-wire` goldens
      verified against the freshly deployed build.)
- [x] 5.3 Real-cluster validation on the bare-metal cluster (`k8s-vms` context): deploy the
      branch build, run a representative topology, verify wire links, carrier, management
      reachability, and namespace-scoped IDs. (SR-SIM 26.7.R1 distributed + multitool
      cross-worker: wire ping, mgmt-mesh ping, SSH; multitool pair: 9500 DF, VLAN full-MTU DF,
      QinQ DF, carrier bounce; two namespaces simultaneously holding wire ID 1.)
- [ ] 5.4 Update the PR body and explainer site claims (cluster-wide → namespace, carrier
      wording, VNI-era rationale). (Site index.html edited locally in both languages; PR body
      update drafted — both held back until the branch commits are pushed.)
