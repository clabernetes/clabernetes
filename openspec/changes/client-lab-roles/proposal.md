# Proposal: Client Lab Roles

## Why

The chart ships RBAC for the manager only. Every client that lets people work with labs
(containerlab's c9s runtime, clab-api-server acting as its users) has to invent its own role,
and in practice hands out `edit` or broader rights, including writes to Deployments and Pods
in privileged lab namespaces. With Node run state and restart requests, a lab can be operated
entirely through c9s resources plus Pod streams, so a narrow, named role is possible.

## What Changes

- The chart installs two ClusterRoles meant to be bound per namespace with a RoleBinding:
  `clabernetes-lab-viewer` (read Topologies, Nodes, Links, NodeProfiles, Pods, Pod logs, Events,
  Services, ConfigMaps, and Pod metrics) and `clabernetes-lab-editor` (the viewer rules plus
  writes to c9s resources and ConfigMaps, and `pods/exec` and `pods/portforward`). Names follow
  the chart's `appName`.
- Neither role grants `apps`, Pod create/update/delete, Secrets, Namespaces, or RBAC objects, and
  neither aggregates into the built-in `view`/`edit` roles.
- `labRoles.enabled` (default `true`) turns them off.
- A "Lab access" guide documents binding and states what the roles isolate (users from each
  other) and what they do not (the worker from a lab, since lab Pods are privileged).

## Capabilities

### New Capabilities

- `client-lab-roles`: namespace-bindable roles for lab users and their isolation contract.

### Modified Capabilities

None.

## Impact

- `charts/clabernetes/templates/lab-roles.yaml`, values and schema, chart golden files.
- Clients bind `clabernetes-lab-editor` by name; clab-api-server's provisioner needs `bind` on it.
