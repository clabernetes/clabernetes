---
title: Persistent storage
description: Preserve network-node state across launcher pod restarts with Kubernetes PVCs.
---

This guide explains how to configure persistent storage for Clabernetes topologies, ensuring node state survives pod restarts.

## Overview

By default, Clabernetes launcher pods use ephemeral storage. When a pod restarts, all changes (configurations, logs, artifacts) are lost. Enabling persistence creates a PersistentVolumeClaim (PVC) for each node's containerlab working directory.

## Enabling Persistence

### Basic Configuration

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
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

## What Gets Persisted

The persistent volume is mounted at the containerlab working directory, which includes:

- **Configuration files**: Running and saved configurations
- **Log files**: System and application logs
- **Certificates**: Generated TLS certificates
- **State files**: Routing tables, learned data
- **Lab artifacts**: Any files created during operation

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

### Node Removal Keeps PVC

When a node is removed from the topology, its PVC is retained. To clean up:

```bash
kubectl delete pvc -l clabernetes/topology=<topology-name>
```

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

List PVCs for a topology:

```bash
kubectl get pvc -l clabernetes/topology=my-topology
```

Check PVC details:

```bash
kubectl describe pvc <pvc-name>
```

## Backup and Restore

### Backing Up Node Data

```bash
# Get pod name
POD=$(kubectl get pod -l clabernetes/topologyNode=srl1 -o name)

# Copy data out
kubectl cp $POD:/clabernetes ./backup-srl1
```

### Restoring Data

```bash
# Copy data back
kubectl cp ./backup-srl1 $POD:/clabernetes
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

Verify persistence is enabled:

```bash
kubectl get topology <name> -o yaml | grep -A5 persistence
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
- [CRD Reference: Persistence](/docs/crd-reference#persistence)
