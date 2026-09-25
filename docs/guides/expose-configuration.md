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

Nodes that share one Pod through `network-mode: container:<primary>` share one port space. A
port an image exposes is kept for the first member that brings it, an explicit `ports` entry
takes precedence over an image-exposed port, and two members declaring the same explicit port
fail planning.

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
compiler and `clabverter --emitCRs` consume all entries into `Node.spec.ports`; the label is not
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

| Port | Protocol | Service | Default `appProtocol` |
| ------ | ---------- | --------- | --------- |
| 21 | TCP | FTP | `ftp` |
| 22 | TCP | SSH | `ssh` |
| 23 | TCP | Telnet | `telnet` |
| 80 | TCP | HTTP | `http` |
| 161 | UDP | SNMP | `snmp` |
| 443 | TCP | HTTPS | `https` |
| 830 | TCP | NETCONF over SSH | `netconf-ssh` |
| 5000 | TCP | vrnetlab QEMU telnet | `telnet` |
| 5900 | TCP | VNC | `rfb` |
| 6030 | TCP | gNMI (Arista default) | `c9s.run/gnmi` |
| 9339 | TCP | gNMI/gNOI | `c9s.run/gnmi` |
| 9340 | TCP | gRIBI | `c9s.run/gribi` |
| 9559 | TCP | P4RT | `c9s.run/p4runtime` |
| 57400 | TCP | gNMI (Nokia default) | `c9s.run/gnmi` |

## Application-protocol hints

Each expose Service port can carry `spec.ports[].appProtocol` to identify the application using
that endpoint. The defaults in the table above are matched by destination port and transport,
including when an explicit Node port or image metadata selects the port. Unknown ports omit the
hint unless you supply an override. Fabric, alias, and management-mesh Services do not receive
these hints.

These values describe the endpoint; c9s does not change the device configuration, transport,
routing, or exposed port set. Kubernetes mirrors the hint to the corresponding EndpointSlices.
Controllers that understand an application protocol may use it to configure protocol-specific
behavior; see the [Kubernetes application-protocol documentation](https://kubernetes.io/docs/concepts/services-networking/service/#application-protocol).

Port 5000/TCP is identified as `telnet` because it serves the vrnetlab QEMU console. Port 22/TCP
remains `ssh`, including SSH File Transfer Protocol (SFTP) sessions carried as an SSH subsystem.
RDP and a separate SFTP endpoint are not part of the automatic port set.

### Direct Node overrides

Set `spec.appProtocols` on a Node to replace or suppress a hint:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
  appProtocols:
    - port: 57400/tcp
      appProtocol: kubernetes.io/h2c
    - port: 443/tcp
      appProtocol: ""
```

This example describes gNMI configured for cleartext HTTP/2 and suppresses the HTTPS hint on port
443. Each entry requires both fields. `port` must be a canonical destination key from `1/tcp` to
`65535/tcp`, or the equivalent lowercase `udp` form; host bindings, ranges, and duplicate keys are
rejected. A non-empty `appProtocol` replaces the default. An empty string omits the Service field,
while removing the entry restores any built-in default.

An override does not select a port for exposure. Use `spec.ports` or `c9s.run/exposePorts` to select
an additional port, and keep exposure policy on the NodeProfile. c9s reconciles added, changed,
and suppressed hints on existing expose Services while preserving allocated NodePorts.

### Topology overrides and inheritance

Use the reserved `c9s.run/appProtocols` label in a Containerlab definition:

```yaml
topology:
  defaults:
    kind: nokia_srlinux
    image: ghcr.io/nokia/srlinux:latest
    labels:
      c9s.run/appProtocols: "57400/TCP=kubernetes.io/h2c,443/tcp=https"
  nodes:
    srl1: {}
    srl2:
      labels:
        c9s.run/appProtocols: "57400/tcp="
```

The comma-separated entries use `<port-definition>=<appProtocol>`. The source syntax accepts
uppercase transport and an omitted transport (TCP), then normalizes keys to the Node API form.
An empty right-hand side suppresses the hint. Empty entries, missing `=`, malformed ports,
invalid non-empty names, and duplicate keys after normalization fail compilation with a diagnostic
identifying the Node and entry.

The directive inherits through defaults, kinds, groups, and Node labels using normal Containerlab
label precedence. A more specific directive replaces the entire inherited value. In this example,
`srl2` suppresses the gNMI hint and retains the built-in `https` hint on 443; it does not inherit the
other entries from the defaults label.

The topology compiler and `clabverter --emitCRs` consume this label into `Node.spec.appProtocols`
and remove it from emitted metadata. It adds no `spec.ports` entries and does not override
`disableAutoExpose` or `exposeType`. Local Containerlab treats it as an inert label.

### Protocol names and TLS

Non-empty values must follow Kubernetes qualified-name syntax: a name of at most 63 characters,
optionally preceded by a lowercase DNS subdomain of at most 253 characters and `/`. The name
starts and ends with an alphanumeric character and may contain `-`, `_`, and `.` internally.
DNS labels must be non-empty, use lowercase alphanumerics and hyphens, and start and end with an
alphanumeric character.

Unprefixed values are reserved for [IANA service names](https://www.iana.org/assignments/service-names-port-numbers/),
such as `ssh`, `http`, and `netconf-ssh`. Custom names require a DNS prefix, for example
`example.com/metrics`. c9s validates the syntax of user values but does not check their IANA
registration. Kubernetes-defined names such as `kubernetes.io/h2c` retain their defined meaning.

The default device gRPC hints (`c9s.run/gnmi`, `c9s.run/gribi`, and `c9s.run/p4runtime`) describe the
application without requesting generic HTTP or HTTPS handling. When device certificates or mutual
TLS must reach the client unchanged, configure any ingress or service mesh for TLS pass-through;
the hint alone does not enforce this. Select `kubernetes.io/h2c` only when the device endpoint is
configured for cleartext HTTP/2. Select `https` on a gRPC port only when the endpoint uses TLS and
termination by an intermediary is acceptable. These overrides describe an existing device setup;
they do not enable or disable TLS on the device.

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

With `useNodeMgmtIpv4Address`, each node's LoadBalancer Service requests the node's management
IPv4 address as its LoadBalancer IP. The address you reach a device at from outside the cluster
is then the address its management interface is configured with. That is the address reported in
`status.directManagement.ipv4`: the node's pinned `mgmt-ipv4`, or the address c9s allocated for
it from the management subnet.

### IPv4 Management IP

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: routed-mgmt
spec:
  expose:
    exposeType: LoadBalancer
    useNodeMgmtIpv4Address: true
  definition:
    containerlab: |
      name: routed-mgmt
      mgmt:
        ipv4-subnet: 10.100.1.0/24  # must be inside the LoadBalancer address pool
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
            mgmt-ipv4: 10.100.1.10  # pinned: this becomes the LoadBalancer IP
          srl2:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest  # allocated: its address becomes the LoadBalancer IP
```

For direct resources, set `spec.expose.useNodeMgmtIpv4Address: true` on the NodeProfile the Nodes
reference, next to its `spec.mgmt` subnet.

To confirm the provider assigned the requested address, compare the two status fields:

```bash
kubectl get nodes.c9s.run srl2 \
  -o jsonpath='{.status.directManagement.ipv4}{"\n"}{.status.exposedPorts.loadBalancerAddress}{"\n"}'
```

### IPv6 Management IP

```yaml
spec:
  expose:
    exposeType: LoadBalancer
    useNodeMgmtIpv6Address: true
```

This requests the node's management IPv6 address (`status.directManagement.ipv6`). IPv6
management addresses exist only when the management policy declares an `ipv6-subnet`. When both
options are set, the IPv4 address is requested.

**Requirements:**

- The LoadBalancer provider must honor `Service.spec.loadBalancerIP`. MetalLB, Cilium LB IPAM, and
  kube-vip do; most cloud providers ignore it. The field is deprecated upstream but still served.
- The provider's address pool must contain the management addresses. Make the management subnet,
  or its `ipv4-range`, part of the pool; a requested address outside the pool leaves the Service
  pending.
- The management subnet is per namespace, the address pool is cluster-wide. Give every lab
  namespace that uses this option its own management subnet; the default `172.20.20.0/24` is the
  same in every namespace.
- A node without a management address of the selected family, for example with management
  disabled, gets an IP assigned by the provider.

Allocated addresses survive Pod restarts and rescheduling. A node that is deleted and recreated
is allocated a new address, and its Service requests the new one. Pin `mgmt-ipv4` where an
address must survive recreation.

The LoadBalancer IP matching the management address does not change how traffic reaches the
device: it still arrives through the node's Pod, the device sees the Pod-local gateway as the
client, and traffic the device starts leaves through the Pod's address. See
[Reaching a node from the rest of the cluster](management-network.md#reaching-a-node-from-the-rest-of-the-cluster).

**Use cases:**

- Devices reachable at their management address from outside the cluster
- Consistent IP addressing across topology deployments
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
