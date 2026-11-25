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

## 4. Connectivity sidecar

- [x] 4.1 Add the `EnsureNetem` link operation, converging the root qdisc in the interface's namespace under the sidecar's `c9:` handle
- [x] 4.2 Recreate the sidecar's own qdisc on change, so dropped rate and corruption do not persist
- [x] 4.3 Converge every endpoint on the cold pass and on new revisions, and re-assert declared impairments on every tick

## 5. Validation

- [x] 5.1 Unit tests: compiler rendering and diagnostics, codec bounds, plan input, live transition, the sidecar loop, and netlink convergence in a private network namespace
- [x] 5.2 E2E `TestDirectLinkNetem`: qdisc and RTT on the device, live change without Pod replacement, removal, and admission rejection
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
