---
title: Persistent storage
description: Preserve network-node state across device Pod restarts with Kubernetes PVCs.
---

This guide explains how to configure persistent storage for Clabernetes topologies, ensuring node state survives pod replacement.

## Overview

By default, Clabernetes device Pods use ephemeral storage: each node's plan-scoped artifact tree
-- generated configuration and the directories its imported kind mounts from the lab directory
-- lives in an `emptyDir` volume, so anything the device saved there is lost when its Pod is
replaced. Enabling persistence backs that artifact volume with a per-node PersistentVolumeClaim
(PVC), named after the Node, instead.

## Enabling Persistence

### Basic Configuration

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

This creates a 5Gi PVC (default size) for each node using the cluster's default storage class.

### Custom Claim Size

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "20Gi"
```

**Size considerations:**

- SR Linux: 5-10Gi typically sufficient
- SR OS: 10-20Gi recommended for larger VMs
- Larger topologies with logging: Consider 20Gi+

### Specifying Storage Class

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "10Gi"
      storageClassName: "fast-ssd"
```

**Storage class recommendations:**

- Use `ReadWriteOnce` capable storage classes
- SSD-backed storage for better performance
- Avoid network-attached storage for latency-sensitive workloads

### Direct Node and NodeProfile Persistence

When authoring the primary API directly, persistence lives on the NodeProfile referenced by
each intended Node:

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

## What Gets Persisted

The PVC backs the node's plan-owned artifact volume, meaning the persistent device artifacts
the plan declares, not a containerlab working directory:

- **Generated files**: startup configuration and other artifacts the imported kind renders
- **Kind-mounted directories**: every path the kind mounts from its lab directory, including
  files the device itself writes there (i.e. saved configuration)

On every Pod start the preparation init container re-verifies and re-stages the package-planned
artifacts by path, mode, and digest; files the device wrote at other paths inside those mounted
directories are left in place. Anything the device writes outside the mounted paths lives in
the container filesystem and is never persisted.

## Important Limitations

### Claim Size Cannot Be Reduced

Once a PVC is created, its size can only be increased:

```yaml
# Initial
claimSize: "5Gi"

# Later - valid increase
claimSize: "10Gi"

# Invalid - cannot reduce
claimSize: "3Gi"  # Will be ignored
```

To use a smaller claim, delete the topology and recreate it.

### Storage Class Is Immutable

The storage class cannot be changed after PVC creation. To change storage class:

1. Backup any important data
2. Delete the topology
3. Recreate with new storage class

### Node Removal Removes the PVC

Each PVC is owner-referenced to its Node: deleting the Node (or removing it from a Topology,
which prunes the emitted Node) garbage-collects the claim and its data. Disabling persistence
on the effective profile also deletes the c9s-owned claim. Back up anything you need first.

## Use Cases

### Long-Running Development Labs

Preserve configuration changes across pod restarts:

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "10Gi"
```

### Production-Like Testing

Maintain state for realistic testing scenarios:

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "20Gi"
      storageClassName: "production-ssd"
```

### Training Environments

Allow students to continue from saved state:

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "5Gi"
```

## Checking PVC Status

List PVCs for a topology, or for a single node:

```bash
kubectl get pvc -l c9s.run/topologyOwner=my-topology
kubectl get pvc -l c9s.run/topologyNode=srl1
```

Check PVC details:

```bash
kubectl describe pvc <pvc-name>
```

## Backup and Restore

### Backing Up Node Data

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

### Restoring Data

```bash
# Copy data back
kubectl cp ./backup-srl1 $POD:/etc/opt/srlinux
```

## Troubleshooting

### PVC Stuck in Pending

Check storage class availability:

```bash
kubectl get storageclass
kubectl describe pvc <pvc-name>
```

Common causes:

- Storage class doesn't exist
- No available persistent volumes
- Node selector constraints

### Data Not Persisting

Verify persistence is enabled on the Topology or on the effective NodeProfile:

```bash
kubectl get topology <name> -o yaml | grep -A5 persistence
kubectl get nodeprofile <name> -o yaml | grep -A5 persistence
```

Check if PVC is mounted:

```bash
kubectl describe pod <pod-name> | grep -A10 "Volumes"
```

### Slow Performance

Consider:

- Using a faster storage class
- Reducing claim size to what's actually needed
- Using local storage for development

## Best Practices

1. **Size appropriately**: Start with recommended sizes, increase as needed
2. **Use appropriate storage class**: Match performance requirements
3. **Regular backups**: Persistence doesn't replace backups
4. **Clean up old PVCs**: Remove unused PVCs to free resources
5. **Monitor usage**: Watch for PVCs nearing capacity

## Related

- [Example: with-persistence.yaml](https://github.com/clabernetes/clabernetes/blob/main/examples/deployment/with-persistence.yaml)
- [CRD Reference: Persistence](/docs/crd/node-profile)
