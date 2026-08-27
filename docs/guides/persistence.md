---
title: Persistent storage
description: Preserve network-node state across device Pod restarts with Kubernetes PVCs.
---

By default, device Pods use ephemeral storage: each node's plan-scoped artifact tree
(generated configuration and the directories its imported kind mounts from the lab directory)
lives in an `emptyDir` volume, so anything the device saved there is lost when its Pod is
replaced. Enabling persistence backs that artifact volume with a per-node
PersistentVolumeClaim (PVC), named after the Node, instead.

## Enabling persistence

On a Topology:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: persistent-lab
spec:
  deployment:
    persistence:
      enabled: true
  definition:
    containerlab: |
      name: persistent
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

This creates a 5Gi PVC (the default size) for each node using the cluster's default storage
class.

For directly authored Nodes, persistence lives on the NodeProfile the Node references:

```yaml
apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: persistent
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "10Gi"
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
  profileRef:
    name: persistent
```

## Claim size and storage class

`claimSize` and `storageClassName` control the claim:

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "20Gi"
      storageClassName: "fast-ssd"
```

5-10Gi covers SR Linux nodes; plan 10-20Gi for larger VM-backed nodes such as SR OS, and more
when devices log heavily. The storage class must support `ReadWriteOnce`; SSD-backed storage
gives noticeably better boot and commit times than network-attached storage.

## What is persisted

The PVC backs the node's plan-owned artifact volume, meaning the persistent device artifacts
the plan declares, not a containerlab working directory:

- **Generated files**: startup configuration and other artifacts the imported kind renders
- **Kind-mounted directories**: every path the kind mounts from its lab directory, including
  files the device itself writes there (i.e. saved configuration)

On every Pod start the preparation init container re-verifies and re-stages the package-planned
artifacts by path, mode, and digest; files the device wrote at other paths inside those mounted
directories are left in place. Anything the device writes outside the mounted paths lives in
the container filesystem and is never persisted.

## Limitations

- **A claim can only grow.** Once the PVC exists, a larger `claimSize` expands it (when the
  storage class supports expansion), but a smaller value is ignored. To shrink a claim, delete
  the topology and recreate it.
- **The storage class is immutable.** Changing it requires backing up the data, deleting the
  topology, and recreating it with the new class.
- **Removing the Node removes the PVC.** Each PVC is owner-referenced to its Node: deleting
  the Node (or removing it from a Topology, which prunes the emitted Node) garbage-collects
  the claim and its data. Disabling persistence on the effective profile also deletes the
  c9s-owned claim. Back up anything you need first.

## Checking PVC status

List PVCs for a topology, or for a single node:

```bash
kubectl get pvc -l c9s.run/topologyOwner=my-topology
kubectl get pvc -l c9s.run/topologyNode=srl1
kubectl describe pvc <pvc-name>
```

## Backup and restore

The persisted directories are mounted into the device container at the destinations the
imported kind declares. For example, SR Linux keeps its configuration under
`/etc/opt/srlinux`. Inspect the Pod's volume mounts to see which paths the PVC backs, then copy
them out of the device container:

```bash
# Get pod name
POD=$(kubectl get pod -l c9s.run/topologyNode=srl1 -o jsonpath='{.items[0].metadata.name}')

# See which container paths the PVC backs
kubectl describe pod $POD

# Copy a persisted directory out
kubectl cp $POD:/etc/opt/srlinux ./backup-srl1
```

Restore by copying the data back:

```bash
kubectl cp ./backup-srl1 $POD:/etc/opt/srlinux
```

## Troubleshooting

A PVC stuck in `Pending` usually means the storage class does not exist, no persistent volume
is available, or scheduling constraints cannot be met:

```bash
kubectl get storageclass
kubectl describe pvc <pvc-name>
```

If state does not survive Pod replacement, verify persistence is enabled on the Topology or on
the effective NodeProfile, and that the PVC is mounted:

```bash
kubectl get topology <name> -o yaml | grep -A5 persistence
kubectl get nodeprofile <name> -o yaml | grep -A5 persistence
kubectl describe pod <pod-name> | grep -A10 "Volumes"
```

## Related

- [Example: with-persistence.yaml](https://github.com/clabernetes/clabernetes/blob/main/examples/deployment/with-persistence.yaml)
- [NodeProfile CRD reference](/docs/crd/node-profile)
