---
title: Management network
description: How nodes get management addresses, how to choose the subnet, and how devices reach each other on it.
---

Every node in a lab gets a management address on a shared management network, exactly as it
would under containerlab on a bare host. This guide covers what a node gets by default, how to
choose the subnet or pin per-node addresses, and what reachability the management network
provides.

## What every node gets

Before the device starts, the connectivity sidecar presents a management interface carrying a
controller-allocated address. The device adopts it like a physical management port, and each
kind's own imported templates render management configuration with the real address, prefix,
and gateway. Without any configuration, addresses come from containerlab's default management
subnet `172.20.20.0/24`, with the gateway at the first usable address (`172.20.20.1`). IPv6
management addresses are allocated only when an IPv6 subnet is declared.

The allocated identity is recorded on the Node:

```bash
kubectl get nodes.c9s.run srl1 -o jsonpath='{.status.directManagement}'
```

This reports the management interface name, the IPv4/IPv6 address and prefix, and the gateway.

## Choosing the subnet

Declare management address policy in the containerlab `mgmt:` block of a Topology definition:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: lab
spec:
  definition:
    containerlab: |
      name: lab
      mgmt:
        ipv4-subnet: 10.20.30.0/24
        ipv6-subnet: 3fff:10:20:30::/64
      topology:
        ...
```

For direct resources, set the same policy on the NodeProfile the Nodes reference:

```yaml
apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: lab-policy
spec:
  mgmt:
    ipv4-subnet: 10.20.30.0/24
    ipv6-subnet: 3fff:10:20:30::/64
```

Both forms accept the containerlab address-policy fields: `ipv4-subnet`, `ipv4-gw`,
`ipv4-range`, and their IPv6 counterparts. An omitted gateway defaults to the subnet's first
usable address. A `range` restricts automatic allocation to a slice of the subnet; pinned
addresses and the gateway only need to be inside the subnet. Docker-only `mgmt` fields
(`network`, `bridge`, `mtu`, `external-access`, `skip-when-unused`, `driver-opts`) describe the
local Docker host and are accepted with a warning, see
[Differences from containerlab](/docs/concepts/containerlab-differences).

## Pinning per-node addresses

`mgmt-ipv4` and `mgmt-ipv6` on a node keep their containerlab meaning:

```yaml
topology:
  nodes:
    srl1:
      kind: nokia_srlinux
      mgmt-ipv4: 10.20.30.11
```

A pinned address must belong to the management subnet. An address declared by two nodes, or
colliding with the gateway, fails before anything is realized. Unpinned nodes get addresses
allocated automatically around the pinned ones.

Pinned management addresses can also drive external reachability: with
`useNodeMgmtIpv4Address`, the node's expose Service requests its `mgmt-ipv4` as the
LoadBalancer IP. See [Service exposure](expose-configuration.md#using-management-ips).

## Device-to-device reachability

The management subnet spans the whole namespace as one L2 broadcast domain, matching
containerlab's management network. A device dialing a peer's management address reaches the
peer device itself, with any protocol and in both directions, even across Pods on different
workers. Hardcoded telemetry targets, syslog collectors, and plain ping between devices all
work. Nodes joining or leaving the namespace are picked up automatically without restarting
any Pod.

The management gateway is the one exception: every Pod answers the gateway address itself with
the same identity, and gateway traffic never crosses to another Pod. Traffic a device sends
beyond the management subnet leaves through the Pod's own network identity, so cluster
Services and external destinations stay reachable through ordinary Kubernetes networking.

## Name resolution

Lab members also resolve each other by node name: each node has a namespace-local Service
named after it, and declared `aliases` add extra headless Services bound to the same Pod. A
device can target `srl2` or a declared alias instead of a management address.

## Related

- [Service exposure](expose-configuration.md) for reaching nodes from outside the cluster
- [Differences from containerlab](/docs/concepts/containerlab-differences)
- [NodeProfile CRD reference](/docs/crd/node-profile)
