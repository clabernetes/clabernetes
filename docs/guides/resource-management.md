---
title: Resources and scheduling
description: Configure device Pod resources, node selection, tolerations, and affinity.
---

This guide explains how to configure resource limits, requests, node scheduling, and tolerations for Clabernetes topologies.

## Overview

Clabernetes allows fine-grained control over:

- **Resource requests/limits**: CPU and memory for device Pods
- **Node selectors**: Control which Kubernetes nodes run your topology
- **Tolerations**: Run on tainted nodes

## Resource Configuration

### Per-Topology Resources

Set resources at the topology level:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: with-resources
spec:
  deployment:
    resources:
      default:  # Applied to all nodes
        requests:
          memory: "2Gi"
          cpu: "1"
        limits:
          memory: "4Gi"
          cpu: "2"
```

### Per-Node Resources

Override resources for specific nodes:

```yaml
spec:
  deployment:
    resources:
      default:
        requests:
          memory: "2Gi"
          cpu: "1"
      # High-resource node
      core-router:
        requests:
          memory: "16Gi"
          cpu: "8"
        limits:
          memory: "32Gi"
          cpu: "16"
      # Minimal resource node
      host:
        requests:
          memory: "512Mi"
          cpu: "250m"
```

For a Topology, the compiler emits one shared NodeProfile for the default policy and a
complete dedicated NodeProfile only for a Node whose resource policy differs. Every emitted
Node receives an explicit `profileRef`; profiles do not inherit from one another.

### Direct Node and NodeProfile Resources

When authoring the primary API directly, put resource policy on a NodeProfile and reference
it explicitly from each intended Node. Profile resources apply to each logical Node's primary
application container; component containers keep the requirements their imported plan declares:

```yaml
apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: high-capacity
spec:
  resources:
    requests:
      memory: "16Gi"
      cpu: "8"
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: core-router
spec:
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
  profileRef:
    name: high-capacity
```

The reference is same-namespace and singular. NodeProfiles are never selected by labels or
merged by priority.

### Global Resources (Config CRD)

Set default resources globally:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Config
metadata:
  name: clabernetes
spec:
  deployment:
    resourcesDefault:
      requests:
        memory: "2Gi"
        cpu: "1"
```

There are no per-kind defaults. Use a NodeProfile when a group of nodes needs different sizing.

### Containerlab `cpu` and `memory`

A node definition may size its own container with containerlab vocabulary: `cpu` (vcpus) and
`memory` (i.e. `1Gb`) become the device container's Kubernetes CPU and memory limits. `cpu-set`
is rejected because CPU pinning has no portable Pod mapping. For a resource name that a
NodeProfile (or the global default) also sets, the profile value wins on the logical Node's
primary application container:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
  cpu: 2
  memory: 4Gb
```

### Resource Priority

The effective NodeProfile (explicitly authored or generated from Topology policy) overrides
global Config fields it sets. Omitted profile fields continue to use Config resolution:

1. Referenced/generated NodeProfile resources
2. Global default resources (`config.deployment.resourcesDefault`)

## Recommended Resource Values

| Device Type | Memory Request | CPU Request | Notes |
| ------------- | ---------------- | ------------- | ------- |
| SR Linux | 4Gi | 2 | Standard variant |
| SR Linux (IXR-10) | 16Gi | 8 | Large variant |
| SR OS (vSIM) | 8Gi | 4 | Minimum for boot |
| cEOS | 2Gi | 1 | Arista container |
| Linux | 512Mi | 250m | Basic containers |

## Node Scheduling

### Node Selectors

Schedule pods on specific Kubernetes nodes:

```yaml
spec:
  deployment:
    scheduling:
      nodeSelector:
        kubernetes.io/arch: amd64
        node-type: network-lab
        disktype: ssd
```

Pods will only run on nodes with ALL specified labels.

### Label Your Nodes

```bash
# Add labels to nodes
kubectl label node worker-1 node-type=network-lab
kubectl label node worker-2 node-type=network-lab

# Verify labels
kubectl get nodes --show-labels
```

### Global Node Selectors by Image

In the Config CRD, map node selectors to image patterns:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Config
metadata:
  name: clabernetes
spec:
  deployment:
    nodeSelectorsByImage:
      "ghcr.io/nokia/srlinux*":
        node-type: srl-capable
        kubernetes.io/arch: amd64
      "internal.io/nokia_sros*":
        node-type: baremetal
        hardware: kvm-enabled
      "default":
        node-type: standard
```

The longest matching pattern takes precedence.

## Tolerations

Run pods on tainted nodes:

```yaml
spec:
  deployment:
    scheduling:
      tolerations:
        - key: "dedicated"
          operator: "Equal"
          value: "network-lab"
          effect: "NoSchedule"
        - key: "nvidia.com/gpu"
          operator: "Exists"
          effect: "NoSchedule"
```

### Taint Your Nodes

```bash
# Add taint
kubectl taint nodes worker-1 dedicated=network-lab:NoSchedule

# Verify taints
kubectl describe node worker-1 | grep Taints

# Remove taint
kubectl taint nodes worker-1 dedicated=network-lab:NoSchedule-
```

## Affinity Rules

Affinity rules apply to direct device Pods and use the native Kubernetes affinity structure. They
can require or prefer particular Kubernetes nodes with `nodeAffinity`, or place device Pods in
relation to other Pods with `podAffinity` and `podAntiAffinity`.

### Topology-level affinity

Set affinity under `spec.deployment.scheduling` to apply one scheduling policy to all device Pods
generated for a Topology:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: scheduled-topology
spec:
  deployment:
    scheduling:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: topology.kubernetes.io/zone
                    operator: In
                    values:
                      - zone-a
                      - zone-b
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                labelSelector:
                  matchLabels:
                    c9s.run/topologyOwner: scheduled-topology
                topologyKey: kubernetes.io/hostname
```

The Topology controller copies this policy into its generated shared NodeProfile. Dedicated
profiles generated for resource overrides retain the same topology-wide affinity.

### NodeProfile-level affinity

For directly authored Nodes, put the same affinity structure on a NodeProfile and reference it
from each Node that should use the policy:

```yaml
apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: network-lab-scheduling
spec:
  scheduling:
    affinity:
      nodeAffinity:
        preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 80
            preference:
              matchExpressions:
                - key: node-type
                  operator: In
                  values:
                    - network-lab
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  profileRef:
    name: network-lab-scheduling
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
```

One NodeProfile can be referenced by multiple Nodes. If Nodes share one Pod through
`network-mode: container:<primary>`, the primary Node's NodeProfile controls that shared Pod.
Affinity `labelSelector` fields select peer Pods for pod affinity or anti-affinity; they do not
select which Nodes receive a NodeProfile.

## Complete Example

Comprehensive scheduling configuration:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: production-lab
spec:
  deployment:
    resources:
      default:
        requests:
          memory: "4Gi"
          cpu: "2"
        limits:
          memory: "8Gi"
          cpu: "4"
      spine1:
        requests:
          memory: "16Gi"
          cpu: "8"
    scheduling:
      nodeSelector:
        kubernetes.io/arch: amd64
        node-type: network-lab
        storage: nvme
      tolerations:
        - key: "dedicated"
          operator: "Equal"
          value: "network-lab"
          effect: "NoSchedule"
  definition:
    containerlab: |
      name: production
      topology:
        nodes:
          spine1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
          leaf1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

## Troubleshooting

### Pods Stuck in Pending

Check events:

```bash
kubectl describe pod <pod-name>
```

Common causes:

- No nodes match selector
- Insufficient resources
- Node taints not tolerated

### Finding Suitable Nodes

```bash
# List nodes with labels
kubectl get nodes -L node-type,disktype

# Check node resources
kubectl describe node <node-name> | grep -A10 "Allocated resources"
```

### Resource Pressure

Check if nodes have capacity:

```bash
kubectl top nodes
kubectl describe node <node-name> | grep -A5 "Allocated"
```

## Privileged Mode

Each device container receives exactly the privilege, capabilities, and devices its containerlab
kind declares, including any `privileged`, `cap-add`, or `devices` vocabulary in the node
definition itself. Privilege is never a NodeProfile or Config setting.

## Related

- [Example: with-resources.yaml](https://github.com/clabernetes/clabernetes/blob/main/examples/deployment/with-resources.yaml)
- [Example: with-scheduling.yaml](https://github.com/clabernetes/clabernetes/blob/main/examples/deployment/with-scheduling.yaml)
- [CRD Reference: Deployment](/docs/crd/topology)
- [CRD Reference: NodeProfile](/docs/crd/node-profile)
