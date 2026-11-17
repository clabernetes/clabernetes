# Proposal: Link Impairments

## Why

containerlab sets link impairments (delay, jitter, loss, rate limit, corruption) with
`containerlab tools netem set` on a running lab, and its topology is gaining a declarative
endpoint `netem` key ([srl-labs/containerlab#3595](https://github.com/srl-labs/containerlab/pull/3595)).
A c9s lab has no way to impair a link at all: the compiler rejects a brief link's `netem` as an
unsupported field and silently drops a structured endpoint's, and nothing in a device Pod
programs a qdisc. Labs that rely on impairments to emulate WAN links or test failure handling
therefore cannot move to c9s, and c9s cannot back containerlab's `tools netem` for the
clabernetes runtime.

## What Changes

- Each Link endpoint gains an optional `netem` in containerlab's vocabulary (`delay`, `jitter`,
  `loss`, `rate`, `corruption`), validated at admission with containerlab's bounds plus the
  delay ceiling the sidecar can program.
- The Topology compiler maps containerlab's endpoint `netem` and the brief link `netem` list
  onto the compiled Links, and reports invalid impairments as located diagnostics.
- The node controller carries the impairment through the device plan input and plan.
- The connectivity sidecar programs the impairment as the endpoint interface's root qdisc,
  re-asserts it every pass so it survives interface recreation, and removes only its own
  impairment when a Link stops declaring one.
- An impairment-only change is a Live connectivity transition whatever link-apply mode the
  device kind declares, so changing an impairment never restarts or recreates a device.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `link-lifecycle`: Link endpoints declare impairments, and changing one is always live.
- `direct-connectivity`: the connectivity sidecar programs and owns endpoint impairments.

## Impact

- API: `LinkEndpointSpec.netem` and the new `LinkNetemSpec` type, with regenerated deepcopy,
  CRDs, and OpenAPI. The field is optional; existing Links are unchanged.
- `compiler/`: link netem parsing, validation, and rendering.
- `controllers/node/planinput.go` and `internal/deviceplan`: `Netem` on interface inputs and
  plans, validated by the codec. Plans without impairments serialize exactly as before.
- `internal/directruntime`: impairment-only transitions stay Live, and a new `EnsureNetem`
  link operation converges qdiscs through netlink in the namespace the interface lives in.
- Docs: Link concepts and the containerlab differences page.
