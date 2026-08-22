---
title: Topology
description: The supported higher-level compiler from containerlab definitions to primitive resources.
icon: Network
---

`Topology` is a supported auxiliary resource for authors who want to submit a complete containerlab
lab as one object. Its controller compiles that source into the primary c9s resources:

```text
Topology
   └── NodeProfile
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
```

Existing Topology settings for exposure, deployment, image pulling, and status
probes are translated into the generated primitive resources and NodeProfile policy.

### Native definition vocabulary

The embedded `containerlab` definition remains native containerlab YAML, and compilation is
fail-closed: a field c9s does not implement fails compilation with a structured diagnostic naming
the field and its source line, and deliberately rejected fields (`runtime`, `auto-remove`,
`pid-mode`, `cgroupns-mode`, `cpu-set`, `stages`, `credentials`) each fail with a diagnostic
stating why. Diagnostics are collected and sorted, so one compile reports every problem at once,
and no resource is created until all of them are resolved. Only constructs whose loss cannot
change lab behavior inside the cluster are accepted with a warning instead: Docker-only
management-network fields (`mgmt.network`, `mgmt.bridge`, MTU, external access, driver options)
and the host half of Docker-style `host:container` port bindings, whose Pod-side port is kept.
Malformed YAML and recognized fields with invalid value types also fail compilation.

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
controller copies them to the device Deployment and its Pods. They inherit from `defaults` and
`kinds` like `env`, so Pods can be selected with `kubectl get pods -l owner=roman`. There is no
`Node.spec.labels`; labels in the embedded definition are converted to Kubernetes metadata.
Invalid Kubernetes labels and c9s-owned namespaces or identity/selector keys fail compilation
before any resource is emitted. The one reserved source directive, `c9s.run/exposePorts`, is
consumed into `Node.spec.ports` instead of becoming metadata. Its role is to request c9s Service
reachability for one or more internal destination ports without using native Containerlab `ports`
bindings, which would publish ports on the local Docker host; see
[Service exposure](../guides/expose-configuration.md#portable-containerlab-topologies).

## Reconciliation lifecycle

The compiler:

1. validates and expands the source definition
2. reconciles NodeProfiles and Links before Nodes
3. corrects drift on generated resources
4. prunes generated resources removed from the source
5. aggregates bounded readiness and resource counts into Topology status

## Child-resource name conflicts

Generated Node, Link, and NodeProfile names are namespace-scoped and are not automatically
prefixed with the Topology name. Before creating children, the controller checks that each desired
name is available. If another resource already uses one of those names, the Topology remains
unmaterialized and reports the conflicts in `status.error`, for example:

```text
duplicate resources found in the lab namespace: link/frr1-eth1-frr2-eth1, node/frr1
create the topology in a different namespace or disambiguate node names.
```

Create the Topology in a different namespace or rename the conflicting nodes. Existing children
owned by the same Topology are compatible and do not trigger this error.

## When to use direct resources

A Topology still embeds the entire source lab in one Kubernetes object. For large or generated labs,
prefer direct resources or use:

```bash
clabverter --emit-crs <topology-file>
```

This emits NodeProfile, Node, and Link manifests without persisting an aggregate Topology.

See the [Topology reference](/docs/crd/topology) for all fields.
