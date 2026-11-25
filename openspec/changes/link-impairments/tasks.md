## 1. API

- [x] 1.1 Add `LinkEndpointSpec.netem` and `LinkNetemSpec` with containerlab's vocabulary and bounds, the 4m delay ceiling, and the host endpoint rule
- [x] 1.2 Regenerate deepcopy, CRDs, and OpenAPI (`make verify-generated`)

## 2. Compiler

- [x] 2.1 Parse the structured endpoint `netem` and the brief link `netem` list
- [x] 2.2 Report invalid impairments as `invalid-link-netem` diagnostics at their topology path
- [x] 2.3 Render the impairment onto the compiled Link endpoint

## 3. Planning

- [x] 3.1 Carry the impairment through the node controller's plan input as nanoseconds and percentages
- [x] 3.2 Add `Netem` to interface inputs and plans and validate it in the codec
- [x] 3.3 Keep impairment-only interface changes Live in the connectivity transition
- [x] 3.4 Place a wire endpoint's impairment on the peer as `PeerNetem` and keep `Netem` for local endpoints, with the codec rejecting either elsewhere

## 4. Connectivity sidecar

- [x] 4.1 Add the `EnsureNetem` link operation, converging the root qdisc in the interface's namespace under the sidecar's `c9:` handle
- [x] 4.2 Recreate the sidecar's own qdisc on change, so dropped rate and corruption do not persist
- [x] 4.3 Converge every endpoint on the cold pass and on new revisions, and re-assert declared impairments on every tick
- [x] 4.4 Add `EnsureFabricNetem`, programming a wire peer's impairment on the sidecar leg so devices that bypass their interface's qdisc (SR-SIM) are impaired

## 5. Validation

- [x] 5.1 Unit tests: compiler rendering and diagnostics, codec bounds, plan input, live transition, the sidecar loop, and netlink convergence in a private network namespace
- [x] 5.2 E2E `TestDirectLinkNetem`: qdisc on the peer's sidecar leg and none in the sender's Pod, RTT, live change without replacing either Pod, recabling to a third Node, removal, and admission rejection
- [x] 5.4 E2E `TestSRSimBootsAndReachesLinux`: a 100ms delay on the SR-SIM endpoint shows up in the round trip from the Linux peer
- [x] 5.3 `make lint`, `make test`, `make verify-generated`, `make check-docs`

## Validation evidence (2026-11-11, KinD via `make test-e2e-local`, local checkout installed)

- `TestDirectLinkNetem` passes: the device sees `qdisc netem c9: root ... delay 50ms  5ms` on
  eth1, round trips take at least 40ms, a delay change to 200ms is applied in the same Pod with
  no restarted container, removing the impairment brings back `noqueue`, and the API server
  rejects jitter without a delay.
- Basic topology suite: 2/2 pass. Direct runtime suite (the CI pattern): 15/16 pass; `TestIxiac`
  fails before creating anything because the machine has no ghcr.io credentials.
- Isolated device namespace (the same fixture with `networkIsolation: true`, checked by hand):
  the impairment is programmed in the private namespace, and a change and a removal converge
  within the kubelet's ConfigMap sync (61-80s) with no restarted container.

## Validation evidence (2026-11-25, KinD via `make test-e2e-local`, sidecar-leg placement)

- Before the change, on the SR-SIM e2e topology, a 100ms delay on the SR-SIM endpoint was
  installed on SR-SIM's interface but counted 0 packets, and the round trip from the Linux peer
  stayed at 1.1ms. The same delay on the peer's sidecar leg raised it to 101ms.
- `TestSRSimBootsAndReachesLinux` passes with the SR-SIM endpoint's 100ms delay present in every
  round trip from the Linux peer.
- `TestDirectLinkNetem` passes: lin1's impairment sits on lin2's sidecar leg and nowhere in lin1's
  Pod, a change to 200ms is applied without replacing or restarting either Pod, recabling the
  Link to lin3 moves the impairment to lin3's leg and removes lin2's, and removal clears it.
  `TestLinuxDataplaneDirect` and `TestDirectFrrDataplane` pass alongside it.
- Recabling by hand: a loopback Link with a 100ms impairment on endpoint A, recabled into a
  cross-Pod wire, ends with the impairment only on the new peer's sidecar leg (the loopback pair
  is removed with both ends, so no qdisc remains on the device interface) and a 100ms round trip.
