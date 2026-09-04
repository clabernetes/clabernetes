---
title: Service exposure
description: Configure how Clabernetes exposes network nodes through Kubernetes Services.
---

This guide explains how to configure Clabernetes service exposure for your network topologies.

## Overview

By default, Clabernetes creates a LoadBalancer expose Service for each node and automatically
selects common network management ports. `exposeType` changes that Service to `ClusterIP` or
`Headless`, or suppresses it with `None`.

Exposure policy applies only to the Service named after the node. It does not disable the
headless `<node>-wire` Service used for fabric connectivity or headless Services created for
declared network aliases.

The default `LoadBalancer` mode is built into c9s. The global `Config` resource does not configure
an exposure mode.

> **Upgrade notice:** `disableExpose` was removed from the Topology and NodeProfile APIs.
> Replace every `disableExpose: true` with `exposeType: None` before applying manifests to this
> release; manifests that still contain `disableExpose` are rejected.

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

## Exposure Options

### Disable Expose Services (`exposeType: None`)

When you do not need client-facing Services for topology nodes:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: internal-only
spec:
  expose:
    exposeType: None
  definition:
    containerlab: |
      name: internal
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

**Effects:**

- No expose Services are created for any node
- Nodes still communicate with each other over their Links; fabric connectivity does not
  depend on expose Services
- Fabric and alias Services remain available
- No external access to nodes

**Use cases:**

- Automated testing pipelines where nodes only need internal connectivity
- Resource-constrained clusters where LoadBalancers are expensive
- Security-sensitive environments

### Direct NodeProfile Configuration

Directly authored Nodes configure exposure through an explicitly referenced, same-namespace
NodeProfile. Profiles are not selected by labels and are not merged:

```yaml
apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: internal-access
spec:
  expose:
    exposeType: ClusterIP
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  profileRef:
    name: internal-access
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
```

One NodeProfile can apply the same exposure policy to multiple Nodes. A direct Node without a
`profileRef`, or whose referenced profile omits `exposeType`, uses the built-in `LoadBalancer`
default. A Topology instead applies `spec.expose` topology-wide and compiles it into each generated
NodeProfile.

### Disable Auto-Expose (`disableAutoExpose: true`)

Control exactly which ports are exposed:

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
protocol. The Service listens on that port and targets it directly on the device Pod; docker
style `host:container` bindings are rejected on Nodes, and a Topology `definition:` normalizes
two-sided entries to their destination port.

**Effects:**

- Only ports explicitly defined in the containerlab topology are exposed
- Automatic port list is not added

**Auto-exposed ports (when disabled, these are NOT exposed):**

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

## Service Types

### LoadBalancer (Default)

External access via cloud load balancer:

```yaml
spec:
  expose:
    exposeType: LoadBalancer
```

**Characteristics:**

- Provisions a cloud LoadBalancer (or MetalLB in bare-metal clusters)
- Each node gets an external IP address
- Ports are accessible from outside the cluster

### ClusterIP

Internal-only access within the cluster:

```yaml
spec:
  expose:
    exposeType: ClusterIP
```

**Characteristics:**

- No external IP provisioned
- Access via service name: `<node>.<namespace>.svc.cluster.local`
- Suitable for in-cluster automation and testing

### Headless

Direct pod access via DNS without load balancing:

```yaml
spec:
  expose:
    exposeType: Headless
```

**Characteristics:**

- Creates a headless service (`clusterIP: None`)
- DNS queries return pod IPs directly instead of a virtual service IP
- No load balancing or proxying by kube-proxy
- Useful when a client must reach the Pod address directly

### None

No per-node expose Service:

```yaml
spec:
  expose:
    exposeType: None
```

**Characteristics:**

- Skips exposed-port allocation and removes any existing owned expose Service
- Does not remove fabric or alias Services
- Re-enable exposure by selecting `LoadBalancer`, `ClusterIP`, or `Headless`

## Using Management IPs

You can assign specific IPs to LoadBalancer services based on the node's management IP from your containerlab topology.

### IPv4 Management IP

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
            mgmt-ipv4: 10.100.1.10  # This becomes the LoadBalancer IP
          srl2:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
            mgmt-ipv4: 10.100.1.11
```

### IPv6 Management IP

```yaml
spec:
  expose:
    exposeType: LoadBalancer
    useNodeMgmtIpv6Address: true
```

**Requirements:**

- Your cluster must support the specified IP addresses
- MetalLB or similar must have the IPs in its address pool
- If the IP is invalid or unavailable, Kubernetes allocates an IP automatically

**Use cases:**

- Consistent IP addressing across topology deployments
- Integration with external systems expecting specific IPs
- DNS pre-configuration

## Examples Comparison

| Configuration | Services Created | External Access | Port Control |
| -------------- | ------------------ | ----------------- | -------------- |
| Default | LoadBalancer | Yes | Auto + Manual |
| `disableAutoExpose: true` | LoadBalancer | Yes | Manual only |
| `exposeType: ClusterIP` | ClusterIP | No | Auto + Manual |
| `exposeType: Headless` | Headless (clusterIP: None) | No | Auto + Manual |
| `exposeType: None` | No expose Service | No | N/A |

## Accessing Nodes

### With LoadBalancer

```bash
# Get service IPs
kubectl get svc -l c9s.run/topologyServiceType=expose

# SSH to node
ssh admin@<EXTERNAL-IP>

# gNMI to node
gnmic -a <EXTERNAL-IP>:57400 -u admin -p NokiaSrl1! capabilities
```

### With ClusterIP

```bash
# From within the cluster (e.g., from a debug pod)
kubectl run debug --rm -it --image=alpine -- sh
apk add openssh-client
ssh admin@srl1.default.svc.cluster.local
```

### With Headless

```bash
# From within the cluster - DNS returns pod IPs directly
kubectl run debug --rm -it --image=alpine -- sh
apk add openssh-client bind-tools

# DNS lookup returns pod IP(s) instead of a virtual service IP
nslookup srl1.default.svc.cluster.local

# Connect directly to the pod
ssh admin@srl1.default.svc.cluster.local
```

### With No Services

```bash
# Access via pod directly (not recommended for production); the deployment is named after
# the node, and unqualified exec targets the device application container
kubectl exec -it deploy/srl1 -- sr_cli
```

## Related

- [CRD Reference: Expose Fields](/docs/crd/topology)
- [Examples: Expose Configurations](https://github.com/clabernetes/clabernetes/tree/main/examples/expose)
