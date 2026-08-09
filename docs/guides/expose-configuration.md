---
title: Service exposure
description: Configure how Clabernetes exposes network nodes through Kubernetes Services.
---

This guide explains how to configure Clabernetes service exposure for your network topologies.

## Overview

By default, Clabernetes creates LoadBalancer services for each node in your topology, automatically exposing common network management ports. This behavior can be customized to match your access requirements.

## Exposure Options

### Complete Disable (`disableExpose: true`)

When you don't need any Kubernetes services for your topology nodes:

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

**Effects:**

- No services are created for any node
- Nodes can still communicate with each other via VXLAN tunnels
- No external access to nodes

**Use cases:**

- Automated testing pipelines where nodes only need internal connectivity
- Resource-constrained clusters where LoadBalancers are expensive
- Security-sensitive environments

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
              - 22:22/tcp       # SSH only
              - 57400:57400/tcp # gNMI
```

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
- Access via service name: `<topology>-<node>.<namespace>.svc.cluster.local`
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
- Useful for StatefulSet-like access patterns where you need direct pod connectivity

**Use cases:**

- Service discovery where clients need to connect directly to specific pods
- Custom load balancing logic in client applications
- Integration with external service meshes that handle their own load balancing
- Scenarios where you need DNS-based pod discovery without Kubernetes proxying

### None

No services but configuration preserved:

```yaml
spec:
  expose:
    exposeType: None
```

**Characteristics:**

- Similar to `disableExpose: true` but the expose configuration is preserved
- Useful when you might want to enable services later without changing other settings

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
| `disableExpose: true` | None | No | N/A |
| `disableAutoExpose: true` | LoadBalancer | Yes | Manual only |
| `exposeType: ClusterIP` | ClusterIP | No | Auto + Manual |
| `exposeType: Headless` | Headless (clusterIP: None) | No | Auto + Manual |
| `exposeType: None` | None | No | N/A |

## Accessing Nodes

### With LoadBalancer

```bash
# Get service IPs
kubectl get svc -l clabernetes/topology=my-topology

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
ssh admin@my-topology-srl1.default.svc.cluster.local
```

### With Headless

```bash
# From within the cluster - DNS returns pod IPs directly
kubectl run debug --rm -it --image=alpine -- sh
apk add openssh-client bind-tools

# DNS lookup returns pod IP(s) instead of a virtual service IP
nslookup my-topology-srl1.default.svc.cluster.local

# Connect directly to the pod
ssh admin@my-topology-srl1.default.svc.cluster.local
```

### With No Services

```bash
# Access via pod directly (not recommended for production)
kubectl exec -it deploy/my-topology-srl1 -- sr_cli
```

## Best Practices

1. **Production deployments**: Use `exposeType: LoadBalancer` with `disableAutoExpose: true` to expose only necessary ports

2. **CI/CD pipelines**: Use `disableExpose: true` when nodes only need internal connectivity

3. **Development**: Use default settings for convenience

4. **Security**: Disable auto-expose and explicitly define only required ports

5. **Cost optimization**: Use `ClusterIP` or `Headless` when external access isn't needed

6. **Service mesh integration**: Use `exposeType: Headless` when integrating with service meshes that handle their own load balancing

## Related

- [CRD Reference: Expose Fields](/docs/crd-reference#expose)
- [Examples: Expose Configurations](https://github.com/clabernetes/clabernetes/tree/main/examples/expose)
