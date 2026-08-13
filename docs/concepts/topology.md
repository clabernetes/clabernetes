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

### Native definition vocabulary

The embedded `containerlab` definition remains native containerlab YAML. Fields that c9s does not
implement are omitted from generated resources and reported as warnings with their source line;
they do not make the Topology fail. Malformed YAML and recognized fields with invalid value types
still fail compilation.

Containerlab node labels have a Kubernetes-native destination:

```yaml
topology:
  nodes:
    srl1:
      kind: nokia_srlinux
      labels:
        owner: roman
```

The compiler carries these labels onto the generated Node's `metadata.labels`, then the Node
controller copies them to the launcher Deployment and its Pods. They inherit from `defaults` and
`kinds` like `env`, so Pods can be selected with `kubectl get pods -l owner=roman`. There is no
`Node.spec.labels`; labels in the embedded definition are converted to Kubernetes metadata.
Invalid Kubernetes labels and c9s-owned namespaces or identity/selector keys are omitted with a
warning. The one reserved source directive, `c9s.run/exposePorts`, is consumed into
`Node.spec.ports` instead of becoming metadata. Its role is to request c9s Service reachability for
one or more internal destination ports without using native Containerlab `ports` bindings, which
would publish ports on the local Docker host; see [Service exposure](../guides/expose-configuration.md#portable-containerlab-topologies).

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
