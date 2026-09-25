## Context

See `proposal.md` for motivation and the delta spec for the behavioral contract.

The direct Node reconciler compiles every group member's management input (pinned or allocated
from the namespace management policy), plans the device, and only after planning succeeds renders
the per-member fabric, alias, and expose Services. The plan whose management identity the running
Pod realizes is `statusPlan`: the new plan, or the previously applied plan when connectivity
reconciliation retains the current Pod. The same plan feeds `status.directManagement` after the
Services are reconciled.

Expose Service rendering currently receives the Node, primary name, resolved profile, and exposed
ports. The LoadBalancer address is derived only from `node.Spec.MgmtIPv4` or `node.Spec.MgmtIPv6`,
and conformance already compares `Service.spec.loadBalancerIP`, so a changed request converges
through an ordinary Service update.

Allocated addresses are a deterministic function of the namespace Node set and each Node's UID.
They stay stable across Pod restarts and rescheduling, and change when a Node is recreated.

## Goals / Non-Goals

**Goals:**

- Request exactly the address the device's management interface is configured with, including
  allocated addresses, without per-Node pinning.
- Converge the Service in the same reconcile that realizes the plan, with no interval where an
  opted-in Service exists without its requested address.

**Non-Goals:**

- Reporting whether the LoadBalancer provider honored the request. The existing
  `status.exposedPorts.loadBalancerAddress` already shows the assigned address.
- Provider-specific request annotations (MetalLB, Cilium, Calico). The options keep using
  `spec.loadBalancerIP`.
- Making device-initiated traffic leave with the management address, preserving client source
  addresses through the management translation, or routing the management subnet natively.
- Choosing management subnets or LoadBalancer pools automatically.

## Decisions

### 1. Widen the existing options instead of adding new ones

The pinned-only behavior was a constraint of the launcher runtime, where containerlab assigned
unpinned addresses inside the launcher Pod and the controller could not observe them. The direct
runtime allocates every address in the controller, so the options can now do what their names
say. For an unpinned Node, the current behavior is a silent no-op that hides the misconfiguration.

Alternative considered: new `useAllocatedMgmtIpv4Address` and `useAllocatedMgmtIpv6Address`
fields, leaving the existing options unchanged. That keeps one more pair of near-duplicate fields
indefinitely, and no user would choose the pinned-only variant for a new manifest. The API is
`v1alpha1` and 0.9 already carries breaking changes with documented migrations, so the change
ships as a documented behavior change instead.

### 2. Read the address from the applied plan, not from Node status or the compiled input

The reconciler maps `statusPlan.Management` by Node ID and converts each entry with the same
function that fills `status.directManagement`, then passes the member's value to expose Service
rendering. The requested address and the reported address therefore cannot diverge. Pinned
addresses reach the plan through the same path, so the Node spec is no longer consulted.

Alternatives considered:

- Reading `node.Status.DirectManagement`: status is written after the Services in the same
  reconcile, so a new Node's Service would first be created without an address and updated one
  reconcile later. Providers would assign a random address in between.
- Using the compiled management input: when the current Pod is retained, the running device still
  realizes the previously applied plan, which can differ from freshly compiled input.

### 3. Keep family selection compatible

Selection keeps today's order: the IPv4 family when `useNodeMgmtIpv4Address` is enabled,
otherwise the IPv6 family. A missing address still renders no request, matching the existing
fallback to provider assignment, and an unparsable address still logs a warning.

## Risks / Trade-offs

- [Some Nodes are pinned and the pool covers only the pinned addresses] → The unpinned Nodes'
  Services now stay pending. The release notes describe the migration: pin those Nodes, or set
  `ipv4-range` so allocation stays inside the pool.
- [The realized address is outside the provider's pool] → The Service stays pending. The guide
  states that the management subnet or allocation range must fall inside the provider's pool.
- [Several namespaces use the default management subnet] → Their Services request identical
  addresses. The guide calls for a distinct management subnet per namespace.
- [A recreated Node receives a new allocated address] → Its Service requests the new address and
  the provider re-assigns it. The guide recommends pinning where addresses must survive
  recreation.
- [The provider ignores `spec.loadBalancerIP`] → The assigned address differs from the request.
  `spec.loadBalancerIP` is deprecated upstream, although MetalLB, Cilium LB IPAM, and kube-vip
  honor it. The guide notes this and points to `status.exposedPorts.loadBalancerAddress` for
  comparison.

## Migration Plan

Topologies that pin every Node, or do not enable the options, render identical Services.
Topologies that enable the options with unpinned Nodes follow the release-note migration before
upgrading. Rolling back restores the pinned-only behavior on the next reconcile.
