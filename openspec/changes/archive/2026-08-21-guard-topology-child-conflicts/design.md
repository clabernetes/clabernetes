## Context

The Topology controller compiles one high-level resource into NodeProfiles, Links, and
Nodes. It currently creates those resources in dependency order and discovers a name collision only
when the API server rejects one of the creates. The reconcile then returns the raw `AlreadyExists`
error, so the Topology has no actionable status and the manager repeatedly logs the same failure.

The generated child names are intentionally unchanged by this change. The existing `Naming` API
field remains outside this work, and no prefixing or name-disambiguation behavior is introduced.

## Goals / Non-Goals

**Goals:**

- Detect occupied generated Node, Link, and NodeProfile names before child reconciliation.
- Distinguish children already generated for the current Topology from unrelated occupants.
- Report all conflicts deterministically in bounded Topology status.
- Avoid partially applying a compiled topology when any child name is blocked.
- Retry blocked Topologies without treating an expected conflict as a controller error.
- Clear the conflict status and resume normal reconciliation after a conflict-free pass.

**Non-Goals:**

- Rejecting the Topology object at admission time. The Topology is accepted by the API server and
  reports the materialization failure through status.
- Prefixing, rewriting, hashing, or otherwise changing child names.
- Detecting collisions in runtime Deployments, Services, PVCs, or other resources created by the
  Node controller.
- Embedding a per-child conflict list in status.

## Decisions

### Render the desired child set before mutating resources

Compilation and rendering will produce the complete desired NodeProfile, Link, and Node
objects before child reconciliation mutates the namespace. A shared rendered
set will be passed to the preflight and subsequent reconciliation stages so conflict detection
examines exactly the objects that would be applied.

The resulting flow becomes:

```text
fetch Topology
  → compile and render children
  → preflight Node/Link/NodeProfile names
  → on conflict: update status and requeue
  → otherwise: profiles, links, nodes, aggregate status
```

### Use uncached reads and current-Topology ownership

Preflight will use the uncached API reader already available to the Topology reconciler. A missing
object is available for creation. An existing object is allowed only when it is recognized by the
same `generatedForTopology` ownership/label rules used by child reconciliation. Any other object
with the desired namespace and name is recorded as a conflict.

The check will also detect duplicate desired identities within the rendered set. Conflict entries
will use lowercase resource type names and sort by type, then name, so status and tests remain
stable across map iteration order.

### Store one bounded error string on Topology status

`TopologyStatus` will gain an optional controller-owned `Error` string. For one or more conflicts,
the value will be exactly:

```text
duplicate resources found in the <namespace> namespace: link/<name>, node/<name>, nodeprofile/<name>
create the topology in a different namespace or disambiguate node names.
```

The second line is included only as the fixed user guidance, while the first line contains the
complete sorted conflict list. Normal status reconciliation clears `Error`. The status update will
retain aggregate fields and must not grow with the number of children.

### Treat conflicts as a blocked reconcile, not an infrastructure failure

After successfully writing the conflict status, the controller will return a non-error requeue
result. This prevents expected name collisions from producing an `AlreadyExists` error loop while
still allowing the controller to retry after the blocking object is removed. Failures to read
children or update Topology status remain ordinary reconciliation errors.

## Risks / Trade-offs

- **Check-then-create race** → The preflight gives deterministic feedback for existing conflicts,
  but a resource can still appear between the check and create; creation errors remain guarded and
  are retried as normal Kubernetes races.
- **Existing ownership labels can be copied accidentally** → Reusing the current ownership
  recognition preserves upgrade/adoption behavior, while the required compiler labels reduce
  accidental adoption of unrelated objects.
- **Periodic retries add API reads while a namespace remains blocked** → Use one bounded requeue
  interval and emit the actionable status once rather than logging every failed create.
- **A conflict blocks otherwise valid children** → This is intentional: partial emission would
  leave a topology in a misleading and harder-to-recover state.
- **A status-only failure is not admission rejection** → Document that users observe and resolve the
  condition through `status.error`; admission validation is outside the controller change.

## Migration Plan

1. Add the optional `TopologyStatus.Error` API field and regenerate OpenAPI, CRD, and related
   generated artifacts.
2. Add the rendered-child preflight and conflict status path to the Topology controller.
3. Verify existing Topologies with no conflicts reconcile with unchanged child names and specs.
4. Deploy the controller and CRD update; existing status remains compatible because the new field is
   optional.
5. If rollback is needed, remove the controller behavior and retain the optional status field;
   existing child resources remain valid and retain their names.

## Open Questions

None for the agreed scope.
