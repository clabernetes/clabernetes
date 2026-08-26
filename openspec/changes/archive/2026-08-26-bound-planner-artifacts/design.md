## Context

The direct Node controller invokes image-discovery and device-planning workers through immutable,
content-addressed Kubernetes objects. A worker attempt can create a NetworkPolicy, Pod, input
ConfigMap, and, after completion, an immutable output ConfigMap named after the worker identity.
The output survives Pod deletion so a repeated reconcile can use the result without rerunning the
worker.

The controller also needs to preserve the package-owned image roles returned by discovery. A
fresh topology declaration contains image references but does not contain those roles, so the
normal first discovery input deliberately clears them. Repeating that first round on every
reconcile adds registry and worker traffic even when the accepted workload has not changed.

## Decisions

### 1. Make the current reconcile define the retention set

Each direct reconcile builds a `keepWorkerArtifacts` set while it advances discovery and planning.
It records:

- the Pod and input ConfigMap for a pending attempt, because that attempt has no durable output;
- the persisted output ConfigMap and input ConfigMap for each successful attempt in the active
  bounded discovery chain, because a later reconcile may need each cached result to continue
  convergence; the completed worker Pod is removed after its output is persisted, and the final
  input may also be mounted by the accepted workload; and
- the accepted workload's cold input when connectivity reconciliation retains the existing Pod.

After the direct reconcile reaches the cleanup boundary, an owner- and label-scoped sweep removes
every worker Pod, NetworkPolicy, input ConfigMap, and output ConfigMap not in that set. The active
convergence chain is bounded by the discovery-round limit; once a later reconcile starts from the
validated cold input, the old chain is no longer needed and becomes eligible for collection.
Cleanup is idempotent and ignores already-absent objects. It checks Node ownership before deletion
so a same-named or similarly labeled artifact from another Node cannot be adopted or removed.

Completed worker Pods and their default-deny NetworkPolicies are still deleted promptly after
their output has been persisted. The sweep remains necessary for interrupted reconciles,
superseded discovery rounds, stale inputs, and artifacts created by older releases.

### 2. Use the accepted cold input only as a validated discovery optimization

The default discovery seed remains the topology-declared image list with `Role` cleared. If an
owned Deployment exists, the controller reads the plan and input ConfigMaps referenced by that
Deployment and validates the cold input before reusing it:

1. the Deployment and referenced artifacts are owned by the current Node UID;
2. every declared topology image reference is represented for the same logical Node in the cold
   input;
3. compiling the current base request with the cold image list and cold discovery-derived
   certificates succeeds; and
4. the compiled digest exactly matches the cold input digest.

Only then are the cold image entries, including package-discovered roles, and cold certificates
used as the discovery starting point. Any failed check falls back to the role-free topology seed.
The optimization never adopts a foreign Deployment, changes topology intent, or treats a partial
cold read as authoritative.

### 3. Keep the strict and optional workload lookups separate

The normal direct workload path continues to reject a same-named Deployment owned by another Node.
The cold-seed lookup uses an optional helper that treats a foreign Deployment as unavailable,
because the optimization should not turn an unrelated ownership conflict into a failed reconcile.
This helper does not write, adopt, or mutate the foreign object.

## Alternatives Considered

- **Retain every attempt referenced during reconciliation:** rejected because each discovery round
  creates a new content-addressed identity and permanently retains superseded cluster objects.
- **Delete every completed worker output immediately:** rejected because the next reconcile would
  rerun discovery and planning instead of using the durable result.
- **Always reuse the cold input:** rejected because stale or partially valid cold state could hide
  a topology or planner-input change.
- **Always use the role-free seed:** simpler and safe, but it adds a redundant discovery round and
  registry traffic on steady reconciles.
- **Use resource age or name prefixes for cleanup:** rejected because names do not establish
  ownership and age-based deletion can remove an active or unrelated artifact.

## Failure Handling

If cold artifacts cannot be read, decoded, owned, or digest-validated, discovery falls back to
the normal seed. If cleanup fails, reconciliation reports the cleanup error and a later reconcile
can retry it; successful planning and workload state are not invalidated by an already-absent
stale artifact.
