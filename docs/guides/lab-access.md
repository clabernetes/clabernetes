---
title: Lab access
description: Bind the clabernetes-lab-viewer and clabernetes-lab-editor roles per namespace, and what they do and do not isolate.
---

The chart ships two ClusterRoles for the people and services that work with labs, as opposed to
the manager's own roles. Bind them per lab namespace with a RoleBinding. They are not aggregated
into the built-in `view` and `edit` roles, so lab access is always granted explicitly.

| ClusterRole | Grants |
| --- | --- |
| `clabernetes-lab-viewer` | get, list, and watch on Topologies, Nodes, Links, NodeProfiles, Pods, Events, Services, ConfigMaps, and PersistentVolumeClaims; Pod logs; get on the lab Namespace itself; Pod metrics (`metrics.k8s.io`) |
| `clabernetes-lab-editor` | everything the viewer has, plus create, update, patch, and delete on Topologies, Nodes, Links, NodeProfiles, and ConfigMaps, delete on PersistentVolumeClaims, plus `pods/exec` and `pods/portforward` |

Neither role grants anything in the `apps` group, Pod create, update, or delete, Secrets,
Namespace writes or any other Namespace, or RBAC objects. An editor deploys, changes, and destroys labs through c9s
resources, and stops, starts, and restarts nodes by patching their Node, see
[Stopping, starting, and restarting a Node](/docs/guides/lab-operations#stopping-starting-and-restarting-a-node).
The controllers create the Deployments, Pods, Services, and claims from there. Deleting the
claims of saved node state (`containerlab destroy --cleanup`) is the only claim write an editor has. `pods/exec` and
`pods/portforward` carry both `create` and `get`, because WebSocket clients open these streams
with a GET request, which API servers before Kubernetes 1.35 authorize as `get`.

The names follow the chart's `appName` value (`clabernetes` by default). Set
`labRoles.enabled: false` to install without them.

## Binding a user to a lab namespace

```bash
kubectl create namespace lab-alice
kubectl label namespace lab-alice pod-security.kubernetes.io/enforce=privileged
kubectl create rolebinding alice-lab-editor --namespace lab-alice \
  --clusterrole clabernetes-lab-editor --user alice
```

The same works for a group (`--group`) or a ServiceAccount
(`--serviceaccount <namespace>:<name>`). Use `clabernetes-lab-viewer` for read-only access.
Check what a binding allows with `kubectl auth can-i`:

```bash
kubectl auth can-i patch nodes.c9s.run --namespace lab-alice --as alice      # yes
kubectl auth can-i create pods/exec --namespace lab-alice --as alice         # yes
kubectl auth can-i update deployments.apps --namespace lab-alice --as alice  # no
kubectl auth can-i patch nodes.c9s.run --namespace lab-bob --as alice        # no
```

Creating the namespace is a provisioning step for an administrator or a provisioning service.
There is no cluster-wide variant of these roles for end users: RBAC cannot limit namespace
creation by name, so a user who may create namespaces may create any namespace. Bind the roles
cluster-wide with a ClusterRoleBinding only for administrators who should see and manage every
lab.

## What the roles isolate, and what they do not

**Users from each other.** A user can only read and change labs in namespaces they are bound in.
Another user's Topologies, Nodes, Pods, and logs are out of reach, and every change is made under
the user's own identity, so the Kubernetes audit log names the person.

**Not the worker from the lab.** Lab namespaces run with the `privileged` Pod Security level.
Device containers and the `clabwire` sidecar run privileged, and RBAC cannot limit `pods/exec` to
one container of a Pod. Whoever can deploy a lab or exec into its Pods can therefore reach the
worker node the Pod runs on, and from there everything that runs on that worker. This is the same
trust level as membership in `clab_admins` on a containerlab Docker host.

On a cluster shared with other workloads, keep labs on dedicated workers:

1. Taint the lab workers, for example `kubectl taint nodes worker-7 c9s.run/lab=true:NoSchedule`,
   and label them `c9s.run/lab=true`.
2. Give lab Pods the matching toleration and a node selector through a
   [NodeProfile](/docs/concepts/node-profiles) or the Topology's `deployment.scheduling`.
3. Keep other tenants' workloads, and cluster components that hold credentials, off those
   workers.

## Related

- [Operating a lab](/docs/guides/lab-operations)
- [Installation](/docs/installation)
