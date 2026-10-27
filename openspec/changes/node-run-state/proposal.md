# Proposal: Node Run State and Restart

## Why

Clients such as containerlab stop a single node by labelling the Topology and the Node with
`c9s.run/ignoreReconcile` and scaling the device Deployment to zero, and restart it by editing
the Deployment's Pod template. That freezes the whole lab while one node is stopped (an applied
topology change is not reconciled), needs write access to `apps/deployments` in a privileged
namespace, and fights two controllers: the Topology controller overwrites drifted Nodes and the
Node controller reverts replica drift. Stop, start, and restart need to be operations the
controllers own.

Clients also cannot map a device Pod back to the containerlab node name without reading the
Node, because Pods drop every `c9s.run/` label except the topology node label.

## What Changes

- `Node.spec.runState` (`Running` | `Stopped`, default `Running`). `Stopped` scales the Node's
  workload to zero while the Node, its Deployment, Services, PersistentVolumeClaim, and Links
  stay; `Running` restores one Pod. The run state never enters the device plan.
- Node condition `Stopped` (`True` once no Pod remains, `False` with reason `Stopping` or
  `Running`), and a `RunState` printer column. A stopped Node is `notready`.
- Annotation `c9s.run/restart-requested=<token>`: the controller replaces the workload Pod once
  per token that differs from the last handled one, recorded in a controller-owned Pod template
  annotation. `status.observedRestartToken` reports the token the current Pod was started with.
- The Topology compiler never sets `runState`; the Topology controller's drift check ignores it
  and drift updates carry it over, together with pending `c9s.run/restart-requested` and
  `c9s.run/device-state-reset` requests. Topology and Node reconciliation continue while Nodes
  are stopped, so an applied change updates a stopped Node, which stays stopped.
- Startup admission (Topology `rollout.batchSize` and the global batch size) neither counts nor
  waits for stopped groups; starting a stopped group again waits for admission like a new
  workload.
- Device Pods carry the Node's `c9s.run/originalNodeName` label next to
  `c9s.run/topologyNode`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `node-lifecycle`: Nodes gain an operational run state, a restart request, and Pod labels that
  identify the containerlab node.

## Impact

- API: additive `spec.runState`, `status.observedRestartToken`, condition `Stopped`; CRD, OpenAPI,
  and printer columns regenerated. Existing Nodes default to `Running` and keep their Pods.
- Controllers: `controllers/node` (rendering, status, startup admission gate),
  `controllers/topology` (drift check, carry-over, startup batches), `internal/directpod`
  (replicas and restart token annotation).
- Upgrading rolls Pods of Nodes that carry `c9s.run/originalNodeName`, because the Pod template
  gains the label; upgrades already roll Pods through the new runtime image.
- Clients need only `patch` on `nodes.c9s.run` to stop, start, and restart a node.
