# Design: device-owned destination NAT before the interposition fallback

## Context

Netfilter grants a connection exactly one destination-NAT binding: the first dstnat-type hook
that programs a translation wins and later hooks are skipped for that flow. The interposition
table's dstnat chain hooked at priority −110, ahead of every x_tables/iptables-nft PREROUTING
chain (fixed at −100), so the sidecar's declared-port translation always consumed the binding.
In the Docker environment the device's NAT is the only in-namespace destination translation,
and containerlab's port publishing composes with it from a different namespace — a layering
the shared Pod namespace cannot reproduce. The change is already implemented and validated on
`direct-c9s` (commit d05f115); this change records the revised contract.

## Goals / Non-Goals

**Goals:**

- Docker-faithful destination translation: device-owned rules evaluate first, the sidecar's
  declared-port mapping is the fallback for flows the device leaves untranslated.
- No behavior change for devices that program no NAT of their own.
- Keep the sidecar's source-translation invariants (Pod-address masquerade, hairpin, inbound
  gateway translation) binding ahead of device-owned masquerades.

**Non-Goals:**

- Full two-namespace fidelity (evaluating both the sidecar's and the device's destination
  translation for one flow, e.g. via conntrack zones). One flow gets one binding; precedence
  selects whose.
- Compensating for device rules that are unmatchable in the shared namespace (for example
  ingress-interface-scoped rules written for Docker's eth0); those need image-side fixes.

## Decisions

- Hook the interposition dstnat chain at priority −90 (after −100), leaving srcnat at 90
  (before 100). Asymmetric on purpose: destination precedence follows Docker's device-first
  reality, while source precedence protects Pod-identity invariants that Kubernetes networking
  depends on.
- Alternative considered — keeping −110 and teaching the sidecar to skip flows the device
  wants: rejected, since the sidecar cannot enumerate device intent without inspecting and
  tracking device-owned rulesets.

## Risks / Trade-offs

- A device rule matching a declared port now shadows the sidecar's mapping. That is the Docker
  behavior, but a device whose translation target is unreachable in the Pod namespace turns a
  previously-refused connection into a device-dependent failure mode.
- Blanket device rules (for example an all-UDP DNAT) can capture inbound flows the sidecar or
  wire would otherwise receive first-packet-inbound; established outbound-first flows are
  unaffected. Observed acceptable in conformance testing across all package-backed kinds.
