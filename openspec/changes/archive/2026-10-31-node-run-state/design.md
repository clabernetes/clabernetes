# Design: Node Run State and Restart

## Context

See proposal.md for motivation. The Node controller renders one Deployment per primary Node;
`directDeploymentConforms` compares the rendered spec semantically, so any rendered difference
updates the Deployment, and a Pod template difference replaces the Pod (Recreate strategy).
A Link-only change can instead be applied live while the Pod is retained. The Topology
controller compiles Nodes and fully updates any emitted Node whose spec, labels, annotations,
or owner reference drift, keeping only the status. Startup admission paces Pod starts per
Topology (`rollout.batchSize`, through a hold annotation) and installation-wide (global batch
size, through an admission annotation) and waits for admitted Pods' sandboxes.

## Goals / Non-Goals

**Goals:** stop, start, and restart through the Node API only; reconciliation continues while
nodes are stopped; no new rights beyond `patch` on Nodes.

**Non-Goals:** a stopped state for Topologies as a whole; Topology status counters for stopped
Nodes; graceful device shutdown beyond the Pod's termination grace period.

## Decisions

- **`spec.runState` outside the containerlab definition.** It sits next to `profileRef`, so the
  planner input (built from the containerlab definition) never sees it. Alternative: an
  annotation. Rejected because a spec field is schema-validated, defaulted, versioned by
  generation, and visible in `kubectl get`.
- **Zero replicas, everything else unchanged.** The renderer only changes `replicas`, so the
  Deployment, its plan ConfigMaps, Services, and claims stay as they are, and a start restores
  the same Pod template. A stopped workload is always rendered from the current plan instead
  of retaining a cold plan for live Link changes, since there is no Pod to apply them to.
- **`Stopped=True` only when no Pod remains.** Terminating Pods count, so a client waiting for
  the condition knows the device processes are gone.
- **Restart token in a controller-owned template annotation.** `c9s.run/restart-tokens` holds the
  handled token per Node ID as JSON, like `c9s.run/device-state-resets`. A Node without the
  request annotation keeps the handled token from the current Deployment, so removing the
  annotation (or a Topology update dropping it) never changes the template. The raw request
  annotation is stripped from the template so only resolved tokens can roll the Pod.
  Alternative: record the handled token in Node status. Rejected because status can be lost or
  overwritten by stale writers, while the template is what actually decides the Pod.
- **`status.observedRestartToken` from the Pod.** The token comes from the observed Pod's
  annotation, so it equals the request exactly when a Pod created for it exists; together with
  readiness it signals completion.
- **Topology carry-over.** A dedicated Node conforms check normalizes `runState` to the existing
  value before comparing, and the carry-over hook copies `runState` and the two request
  annotations. The compiler output stays free of operational state.
- **Startup admission skips stopped groups.** A stopped Node bypasses the Node controller's hold
  and admission gates (it creates no Pod). The global admission controller withdraws a stopped
  group's admission so a start is paced again; the Topology batch keeps a stopped group's hold,
  excludes it from slots and readiness waits, and stops requeueing when only stopped groups
  hold.

## Risks / Trade-offs

- [A Topology with a stopped Node reports not ready/degraded] → documented; clients read the
  Node `Stopped` condition. A dedicated Topology counter can be added later without changing
  this contract.
- [Adding the `c9s.run/originalNodeName` Pod label changes Pod templates] → upgrades roll Pods
  anyway because the runtime image changes with the release.
- [Stopping and quickly starting before the admission controller withdraws admission skips
  pacing] → bounded to one Pod start; acceptable.

## Migration Plan

Additive API. Existing Nodes default to `Running`. Clients detect support by the presence of
`spec.runState` in the `nodes.c9s.run` CRD schema. Rollback to an older controller ignores the
field (stopped Nodes would start again) after the CRD is reverted.
