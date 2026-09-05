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

The management subnet spans the whole namespace, matching containerlab's management network. A
device dialing a peer's management address reaches the peer device itself, with any protocol
and in both directions, with source addresses intact, across Pods on any worker. Hardcoded
telemetry targets, syslog collectors, gNMI subscriptions, and plain ping between devices all
work. Nodes joining, leaving, or moving between workers are picked up automatically without
restarting any Pod.

Nothing is flooded to get there. The connectivity sidecar answers the device's ARP (and IPv6
neighbor discovery) for every peer with the gateway identity and routes the packet over a
management tunnel endpoint on the Pod network, holding exactly one neighbor entry and one
forwarding entry per peer. The peers come from a namespace peer directory the controller
publishes as the `c9s-peer-directory-0` to `c9s-peer-directory-7` ConfigMaps, projected into
every Pod; there is no discovery traffic and no DNS lookup on the path.

The management gateway is the one address that never crosses to another Pod: every Pod answers
it itself, with the same identity. Traffic a device sends beyond the management subnet leaves
through the Pod's own network identity, so cluster Services and external destinations stay
reachable through ordinary Kubernetes networking.

### What does not cross the mesh

Only ARP and neighbor discovery are emulated between Pods. Other broadcasts and multicasts a
device sends on its management port stay inside its Pod: gratuitous ARP, LLDP, DHCP, mDNS. A
management stack that relies on any of these to find its peers has to be pointed at addresses
or names instead.

### IPv6

Declare an `ipv6-subnet` and every node also gets an IPv6 management address, reachable
between devices exactly like the IPv4 one. A device that disables IPv6 in its namespace (EOS
does) keeps a working IPv4 mesh; the sidecar installs IPv6 mesh state only while the device
carries the IPv6 gateway.

## Reaching a node from the rest of the cluster

Other workloads reach a node through its Pod address or its Services rather than through the
management subnet: the expose Service named after the node and one headless Service per alias,
both as `<name>.<namespace>.svc.cluster.local`. Declared ports are translated to the device's
management address inside the Pod, and the device sees the Pod-local gateway as the client,
the same source identity containerlab's Docker port publishing presents. This works the same
for container network operating systems, for devices that run a virtual machine behind
vrnetlab's port forwarding (SR OS, IOS XR, NX-OS, IOS XE), and for plain Linux nodes.

## Name resolution

Inside a device, every node name in the namespace, every declared alias, and every chassis
component name resolves to that node's management address. The entries are written into the
Pod's `/etc/hosts` from the peer directory and updated as nodes join or leave, without
restarting any Pod. A device can therefore target `srl2` exactly as on containerlab's
management network, on any port.

## Scaling

The mesh's per-Pod state is constant per peer: one neighbor entry, one forwarding entry, and
one hosts line. The sidecar idles at a few millicores per Pod, and the eight directory shards
grow by roughly 70 bytes per node, far from the ConfigMap size limit even at thousands of
nodes. The controller re-checks a ready node every five minutes; nodes still coming up are
re-checked every minute.

What bounds a large lab in practice is Pod capacity, not the mesh:

- Each node is one Pod, and a deploy wave briefly adds two short-lived helper Pods per node
  (planning and image pull). The kubelet allows 110 Pods per node by default (`maxPods`); when
  a wave exhausts the slots, Pods stay `Pending` with `Too many pods` until the helpers finish
  and the scheduler retries, then the wave completes. Raise `maxPods` on the workers for large
  labs.
- Peer state converges through the kubelet's ConfigMap projection: a node added to a running
  lab is reachable by name from its peers about half a minute after it is ready, and a Pod
  rescheduled to another worker reaches its peers again within the kubelet's sync period,
  about a minute, longer while a worker is busy starting many Pods.

## Verifying and troubleshooting

The sidecar container `clabwire` logs management and wire events:

```bash
kubectl logs deploy/srl1 -c clabwire
```

From a shell in a node's Pod (the device container shares the network namespace), the shape to
expect is:

```bash
ip -br link            # c9sr0 paired with the device's management leg, c9sm0 the tunnel endpoint
ip neigh show dev c9sm0   # one PERMANENT entry per peer
bridge fdb show dev c9sm0 # one entry per peer, pointing at the peer's Pod address
grep c9s-peer /etc/hosts  # every peer by name
```

The published directory is visible from outside the Pods:

```bash
kubectl get configmap -l app.kubernetes.io/name=clabernetes -o name | grep peer-directory
kubectl get configmap c9s-peer-directory-0 -o jsonpath='{.data.peers\.json}'
```

Common symptoms:

- **A peer is unreachable right after it was rescheduled.** Its new Pod address has to reach
  the other Pods through the kubelet's ConfigMap sync; compare the `pod` field of its
  directory entry with the forwarding entry on a peer, and allow about a minute.
- **A node is ready but its management plane does not answer** (SSH works, gNMI or NETCONF
  does not). Check that the device took over its management leg: for SR Linux the Pod
  namespace shows `mgmt0` paired with `c9sr0`, not `eth0`. If the leg still carries the
  kernel-held address, the pod kernel is answering for the device; restart the Pod.
- **Everything is unreachable between two Pods on different workers.** NetworkPolicies must
  allow UDP 14789 (management mesh) and UDP 14790 (link wire) between device Pods, TCP 14791
  from the nodes (readiness), and egress to the API server; see
  [Lab operations](lab-operations.md).

## Upgrading from the flooded mesh

The routed mesh replaced a bridged, head-end-replicated one. The two shapes do not talk to
each other on one tunnel, so the runtime image change rolls every device Pod of a namespace;
the controller then removes the old `c9s-management-mesh` discovery Service and the single
`c9s-peer-directory` ConfigMap. Roll namespaces one at a time if a lab must not restart all
at once.

## Related

- [Service exposure](expose-configuration.md) for reaching nodes from outside the cluster
- [Differences from containerlab](/docs/concepts/containerlab-differences)
- [NodeProfile CRD reference](/docs/crd/node-profile)
