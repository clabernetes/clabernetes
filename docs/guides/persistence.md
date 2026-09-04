---
title: Persistent storage
description: Preserve saved device configuration and node state across device Pod restarts with Kubernetes PVCs.
---

This guide explains how Clabernetes persists device state, what wins when the declared startup
configuration and the device's saved configuration disagree, and how to reset a node back to its
startup configuration.

## Overview

By default, Clabernetes device Pods use ephemeral storage: each node's plan-scoped artifact tree
lives in an `emptyDir` volume, so every Pod start is a factory reset to exactly what the spec
declares. Enabling persistence backs that artifact volume with a per-node PersistentVolumeClaim
(PVC), named after the Node.

With persistence enabled, Clabernetes follows the same contract as containerlab's
[configuration artifacts](https://containerlab.dev/manual/conf-artifacts/): the startup
configuration seeds the node once, and from then on the configuration the device saved wins. A
Pod restart, an eviction, or an unrelated Topology change never silently reverts saved work.

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

This creates a 512Mi PVC (default size) for each node using the cluster's default storage class.

### Custom Claim Size

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "20Gi"
```

**Size considerations:**

- The 512Mi default is intended for saved configurations and small generated artifacts
- Increase the claim size when a device writes larger logs, checkpoints, or other state
- The storage class may round the request up to its provider's minimum volume size

### Specifying Storage Class

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "10Gi"
      storageClassName: "fast-ssd"
```

The storage class must support `ReadWriteOnce` volumes.

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

## What Persists, and What Wins

The PVC backs the node's plan-owned artifact volume: the generated startup configuration and
every directory the imported kind mounts from its lab directory, including files the device
itself writes there, such as its saved configuration.

On every Pod start, preparation regenerates and verifies the planned artifacts, then decides
per file what to publish:

- **A file the device has modified since it was last staged is left in place.** Saving your
  configuration on the node (for example `tools system configuration save` on SR Linux) makes
  that saved state the boot state of every later Pod, regardless of whether the startup
  configuration was CLI-format or a full config file.
- **A file the device never touched follows the spec.** Updated topology files, certificates,
  and repo files keep propagating.
- **Everything the device wrote at unplanned paths** inside the persisted directories (SSH host
  keys, checkpoints, logs) is left in place, as before.

Preparation records what it staged in a small ledger beside the artifacts and reports every
preserved file in the preparation container's log.

A consequence, identical to containerlab: once a saved configuration exists, editing the
`startup-config` in your Topology or Node no longer changes the running node. Use
`enforce-startup-config` or a device-state reset (both below) when you want the declared
configuration to win again.

## Enforcing the Startup Configuration

Setting containerlab's `enforce-startup-config` on a node makes the declared startup
configuration win on every Pod start, overwriting saved changes at the planned paths:

```yaml
srl1:
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
  startup-config: golden.json
  enforce-startup-config: true
```

Declaring `enforce-startup-config` without a `startup-config` is rejected before any workload
is created.

## Resetting a Node to Its Startup Configuration

To throw away one node's persistent state and re-seed it from the declared configuration,
annotate the Node with an opaque token of your choice:

```bash
kubectl annotate node.c9s.run srl1 c9s.run/device-state-reset=$(date +%s) --overwrite
```

The Pod is replaced; its preparation wipes the node's plan-owned artifact tree and stages
everything fresh. Each token value is honored exactly once, so the annotation can stay in
place. Progress is visible on the Node as the `DeviceStateReset` condition, which becomes
`True` once the replacement Pod finished preparation, and as the matching events.

The reset affects only the annotated Node. Removing the annotation later is safe; it replaces
the Pod once more without wiping anything.

## Keeping Claims Past Node Deletion

By default each PVC is owner-referenced to its Node: deleting the Node (or removing it from a
Topology, which prunes the emitted Node) garbage-collects the claim and its data. Set
`reclaim: Retain` to keep claims alive past Node deletion:

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      reclaim: Retain
```

With retention, deleting and recreating a Topology behaves like `containerlab destroy` and
`deploy` without `--cleanup`: each recreated Node adopts its retained claim by name and boots
from the preserved state. Adoption refuses a claim whose storage class does not match the
declared one instead of silently mounting it.

Retained claims are never deleted by Clabernetes. Delete them by hand when a lab is truly gone:

```bash
kubectl delete pvc srl1
```

## Saving Configuration

Save on the device with its own command, for example `tools system configuration save` on SR
Linux. The saved file lands in a persisted directory and becomes the boot state of every later
Pod. Without persistence, a saved configuration is lost when the Pod is replaced.

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

To use a smaller claim, delete the Node (or its Topology) and recreate it.

### Storage Class Is Immutable

The storage class cannot be changed after PVC creation. To change storage class:

1. Back up any important data
2. Delete the Node (or its Topology), and delete a retained claim as well
3. Recreate with the new storage class

### Node Removal Removes the Claim Unless Retained

With the default `Delete` reclaim policy, deleting the Node garbage-collects the claim and its
data, and disabling persistence on the effective profile deletes the c9s-owned claim. Retained
claims survive both. Back up anything you need first.

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

### A Spec Change Does Not Reach the Node

That is the saved-configuration-wins contract at work: a saved configuration shadows startup
config updates. Set `enforce-startup-config: true` or run a device-state reset to make the
declared configuration win.

## Related

- [Example: with-persistence.yaml](https://github.com/clabernetes/clabernetes/blob/main/examples/deployment/with-persistence.yaml)
- [CRD Reference: Persistence](/docs/crd/node-profile)
- [Containerlab: configuration artifacts](https://containerlab.dev/manual/conf-artifacts/)
