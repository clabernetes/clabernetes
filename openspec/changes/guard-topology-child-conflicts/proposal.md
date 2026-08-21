## Why

Two Topology resources in the same namespace can compile to identically named Node, Link, or LauncherProfile resources. Kubernetes rejects the later child creation, but the Topology currently provides no actionable status feedback and reconciliation repeatedly retries the failing create. This change makes the collision visible and directs users to isolate the Topology in another namespace or disambiguate its node names.

## What Changes

- Preflight the names of every Node, Link, and LauncherProfile the Topology would emit before creating or updating any child resources.
- Treat an existing child already generated and owned by the current Topology as compatible; report other occupants as conflicts.
- Add a bounded Topology status error containing the namespace and deterministic `type/name` conflict list.
- Use the message format:
  `duplicate resources found in the <namespace> namespace: node/<name>, link/<name>, launcherprofile/<name>`
  followed by `create the topology in a different namespace or disambiguate node names.`
- Skip child reconciliation while conflicts remain and clear the error after a later conflict-free reconcile.
- Do not add or change child-resource naming or introduce a name-prefix flag.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `topology-resource`: Require deterministic child-resource conflict detection and actionable error reporting in Topology status before child reconciliation proceeds.

## Impact

- Topology API status gains a controller-owned error field.
- Topology reconciliation and status handling change in `controllers/topology`.
- Rendering and child ownership checks are reused to identify the emitted Node, Link, and LauncherProfile set.
- Topology API generated OpenAPI/CRD artifacts, tests, and user documentation require regeneration or updates.
