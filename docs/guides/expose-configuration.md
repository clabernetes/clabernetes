---
title: Service exposure
description: Configure how Clabernetes exposes network nodes through Kubernetes Services.
---

By default, c9s creates a LoadBalancer Service for every node in a lab and exposes a default
set of management ports through it. This page covers how that works and how to change it:
which ports are exposed, which Service type carries them, and how to turn exposure off.

Exposure is policy: on a Topology it lives under `spec.expose`, and for directly authored
Nodes the same fields live on the [NodeProfile](/docs/concepts/node-profiles) the Node
references. The examples below use the Topology form.

## How exposure works

A direct device runs in its Pod's own network namespace, so every port the node listens on is
already bound at the Pod address. A Kubernetes Service still carries an explicit port list,
which keeps the exposed port set a declared list rather than "everything the node listens on":

1. The default management port list (see the table below) is exposed unless
   `disableAutoExpose: true`.
2. Anything outside that list (a custom app on 8080, iperf3 on 5201) has to be named in the
   node's `ports` (ports the imported kind plan itself declares on the container are carried
   automatically unless auto-expose is disabled).
3. Each destination port is recorded in the Node's `status.exposedPorts`. That status is the
   source of truth the node's expose Service (named after the node itself) is programmed
   from.

Clients always connect to the node's natural port: the Service listens on the destination port
and targets that same port on the device Pod directly, with no Docker port publication in
between. This is why `ports` entries declare a destination port only. Declared `aliases`
each add an extra headless Service selecting the node's Pod under the alias name.

### Portable containerlab topologies

A normal containerlab `ports` entry publishes the port on the local Docker host. When a port is
needed only so nodes can communicate through a c9s Service, use the c9s definition label instead:

```yaml
topology:
  nodes:
    gnmic:
      kind: linux
      image: ghcr.io/openconfig/gnmic:latest
      labels:
        c9s.run/exposePorts: "9273/tcp,8125/udp"
```

The value is a comma-separated list using the same destination-port grammar as `Node.spec.ports`.
Each entry is a destination port with an optional `tcp` or `udp` protocol. The c9s topology
compiler and `clabverter --emit-crs` consume all entries into `Node.spec.ports`; the label is not
copied to Kubernetes labels. Invalid entries fail compilation. Local containerlab keeps the value
as an inert container label and does not publish either port on the host.

This label only declares which ports the c9s Service carries. The effective NodeProfile still
controls whether that Service is a `ClusterIP`, `LoadBalancer`, `Headless`, or disabled.

## Disabling exposure

Set `disableExpose: true` when the lab needs no Kubernetes Services at all, for example in
automated test pipelines, on clusters where LoadBalancers are expensive, or in environments
that must not accept outside connections:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: internal-only
spec:
  expose:
    disableExpose: true
  definition:
    containerlab: |
      name: internal
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

No expose Services are created and nodes are not reachable from outside the cluster. Nodes
still communicate with each other over their Links; fabric connectivity does not depend on
expose Services.

## Choosing the exposed ports

Set `disableAutoExpose: true` to skip the default management port list and expose exactly the
ports declared in the topology:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: minimal-ports
spec:
  expose:
    disableAutoExpose: true
  definition:
    containerlab: |
      name: minimal
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
            ports:
              - 22/tcp    # SSH only
              - 57400/tcp # gNMI
```

Each entry is the destination port (the port the node itself listens on) with an optional
protocol. The Service listens on that port and targets it directly on the device Pod; Docker
style `host:container` bindings are rejected on Nodes, and a Topology `definition:` normalizes
two-sided entries to their destination port.

With auto-expose disabled, the following default ports are no longer added:

| Port | Protocol | Service |
| ------ | ---------- | --------- |
| 21 | TCP | FTP |
| 22 | TCP | SSH |
| 23 | TCP | Telnet |
| 80 | TCP | HTTP |
| 161 | UDP | SNMP |
| 443 | TCP | HTTPS |
| 830 | TCP | NETCONF over SSH |
| 5000 | TCP | vrnetlab QEMU telnet |
| 5900 | TCP | VNC |
| 6030 | TCP | gNMI (Arista default) |
| 9339 | TCP | gNMI/gNOI |
| 9340 | TCP | gRIBI |
| 9559 | TCP | P4RT |
| 57400 | TCP | gNMI (Nokia default) |

## Service types

`exposeType` selects the kind of Service created per node:

```yaml
spec:
  expose:
    exposeType: LoadBalancer
```

- **`LoadBalancer`** (default): provisions a cloud load balancer, or an address from MetalLB
  on bare metal. Each node gets an external IP and its ports are reachable from outside the
  cluster.
- **`ClusterIP`**: in-cluster access only, at `<node>.<namespace>.svc.cluster.local`. Suitable
  for in-cluster automation and testing.
- **`Headless`**: a Service with `clusterIP: None`. DNS returns the Pod IP directly and
  kube-proxy does no load balancing or proxying. Use it when clients must connect to the Pod
  itself, for example service meshes or clients that do their own balancing.
- **`None`**: no Services are created, but unlike `disableExpose: true` the rest of the expose
  configuration is preserved, so exposure can be re-enabled later without touching other
  settings.

## Using management IPs

With `useNodeMgmtIpv4Address` (or `useNodeMgmtIpv6Address`), a node's pinned `mgmt-ipv4`
(`mgmt-ipv6`) address is requested as its LoadBalancer IP, giving labs stable, predictable
external addresses:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: static-ips
spec:
  expose:
    exposeType: LoadBalancer
    useNodeMgmtIpv4Address: true
  definition:
    containerlab: |
      name: static
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
            mgmt-ipv4: 10.100.1.10  # requested as the LoadBalancer IP
          srl2:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
            mgmt-ipv4: 10.100.1.11
```

The cluster must be able to honor the requested addresses: MetalLB (or the cloud equivalent)
needs them in its address pool. If an address is invalid or unavailable, Kubernetes allocates
one automatically.

## Configuration summary

| Configuration | Services created | External access | Port control |
| -------------- | ------------------ | ----------------- | -------------- |
| Default | LoadBalancer | Yes | Auto + manual |
| `disableExpose: true` | None | No | N/A |
| `disableAutoExpose: true` | LoadBalancer | Yes | Manual only |
| `exposeType: ClusterIP` | ClusterIP | No | Auto + manual |
| `exposeType: Headless` | Headless (`clusterIP: None`) | No | Auto + manual |
| `exposeType: None` | None | No | N/A |

## Accessing nodes

With a LoadBalancer Service:

```bash
# Get service IPs
kubectl get svc -l c9s.run/topologyServiceType=expose

# SSH to node
ssh admin@<EXTERNAL-IP>

# gNMI to node
gnmic -a <EXTERNAL-IP>:57400 -u admin -p NokiaSrl1! capabilities
```

With ClusterIP or Headless, connect from inside the cluster using the Service name:

```bash
kubectl run debug --rm -it --image=alpine -- sh
apk add openssh-client
ssh admin@srl1.default.svc.cluster.local
```

With no Services, exec into the device Pod directly; the Deployment is named after the node,
and an unqualified exec targets the device application container:

```bash
kubectl exec -it deploy/srl1 -- sr_cli
```

## Related

- [Topology CRD reference](/docs/crd/topology)
- [Exposure examples](https://github.com/clabernetes/clabernetes/tree/main/examples/expose)
