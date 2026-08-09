---
title: Topology
description: The supported higher-level compiler from containerlab definitions to primitive resources.
icon: Network
---

`Topology` is a supported auxiliary resource for authors who want to submit a complete containerlab
lab as one object. Its controller compiles that source into the primary c9s resources:

```text
Topology
   └── LauncherProfile
   └── Node (one per network node)
   └── Link (one per wire)
```

The generated resources are reconciled by the same controllers as directly authored
[Nodes and Links](/docs/concepts/nodes-and-links).

## Example

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: two-nodes
spec:
  definition:
    containerlab: |
      name: two-nodes
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:26.3
          client:
            kind: linux
            image: ghcr.io/srl-labs/network-multitool:latest
        links:
          - endpoints:
              - srl1:e1-1
              - client:eth1
  connectivity: vxlan
```

Existing Topology settings for connectivity, exposure, deployment, image pulling, and status
probes are translated into the generated primitive resources and launcher policy.

## Reconciliation lifecycle

The compiler:

1. validates and expands the source definition
2. reconciles LauncherProfiles and Links before Nodes
3. corrects drift on generated resources
4. prunes generated resources removed from the source
5. aggregates bounded readiness and resource counts into Topology status

## When to use direct resources

A Topology still embeds the entire source lab in one Kubernetes object. For large or generated labs,
prefer direct resources or use:

```bash
clabverter --emit-crs <topology-file>
```

This emits LauncherProfile, Node, and Link manifests without persisting an aggregate Topology.

See the [Topology reference](/docs/crd/topology) for all fields.
