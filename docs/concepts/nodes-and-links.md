---
title: Nodes and Links
description: The primary Clabernetes API for describing network nodes and point-to-point wires.
icon: Cable
---

`Node` and `Link` resources form the primary c9s API. A Node contains one containerlab node
definition, while a Link contains one connection between two node interfaces.

Both resources are namespace-scoped. A namespace is therefore the boundary of a directly authored
lab, and both endpoints of a Link must refer to Nodes in that namespace.

## Node

The Node name is the containerlab node name. Its specification uses containerlab vocabulary for
fields such as `kind`, `image`, `type`, `startup-config`, and `exec`.

```yaml
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  launcherProfileRef:
    name: lab-policy
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:26.3
```

A Node can reference one same-namespace
[LauncherProfile](/docs/concepts/launcher-profiles). If the reference is omitted, global `Config`
defaults apply. An explicit reference that does not exist prevents the Node from being realized.

## Link

A Link declares exactly two endpoints and a connectivity flavor (`vxlan` or `slurpeeth`).

```yaml
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: srl1-e1-1-multitool-eth1
spec:
  endpointA:
    nodeName: srl1
    interfaceName: e1-1
  endpointB:
    nodeName: multitool
    interfaceName: eth1
  connectivity: vxlan
```

Wiring belongs only to Link objects; interfaces are not embedded into Node specifications. The
link controller validates endpoints and records a cluster-wide tunnel allocation in status, while
each device Pod's connectivity helper watches only the Links terminating on its own Nodes. The
transport realizing a wire is controller-selected: same-worker endpoint pairs are patched
directly, and cross-worker pairs use a VXLAN tunnel carrying the Link's allocated tunnel id.

## Why direct resources?

Each Node grows only with its own configuration and each Link remains one wire. This removes the
single aggregate-object size limit of a source Topology and allows tooling such as `clabverter
--emit-crs` to produce independently reconciled resources.

Direct resources do not promise unlimited scale: Kubernetes API capacity, controller throughput,
and the total object count still matter.

## Complete example

The
[individual-resource SR Linux and multitool example](https://github.com/clabernetes/clabernetes/tree/main/examples/basic/individual-resources/srl-multitool)
contains a LauncherProfile, two Nodes, and one Link.

See the [Node reference](/docs/crd/node) and
[Link reference](/docs/crd/link) for all fields.
