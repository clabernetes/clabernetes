# Expose Configuration Examples

This directory contains examples demonstrating different service exposure options.

## Overview

Clabernetes can expose topology nodes via Kubernetes services. By default:
- LoadBalancer services are created for each node
- Common management ports are automatically exposed (SSH, gNMI, NETCONF, etc.)

## Examples

### no-services.yaml

Completely disables service creation with `disableExpose: true`.

**Use cases:**
- Nodes only need to communicate with each other (not externally)
- Minimizing cluster resources
- Testing inter-node connectivity only

```yaml
spec:
  expose:
    disableExpose: true
```

### cluster-ip-only.yaml

Creates ClusterIP services instead of LoadBalancer.

**Use cases:**
- Access nodes only from within the cluster
- No external LoadBalancer required
- Accessing nodes from other pods in the cluster

```yaml
spec:
  expose:
    exposeType: ClusterIP
```

Access nodes by service name:
```bash
# From another pod in the cluster
ssh admin@cluster-ip-only-srl1.<namespace>.svc.cluster.local
```

### no-auto-expose.yaml

Disables automatic port exposure with `disableAutoExpose: true`.

**Use cases:**
- Security-conscious deployments
- Only specific ports should be accessible
- Compliance requirements

```yaml
spec:
  expose:
    disableAutoExpose: true
```

Define ports explicitly in the containerlab topology -- each entry is the destination port (the
port the node listens on) with an optional protocol:
```yaml
nodes:
  srl1:
    ports:
      - 22/tcp
      - 57400/tcp
```

## Auto-Exposed Ports

When `disableAutoExpose: false` (default), these ports are automatically exposed:

| Port | Protocol | Service |
|------|----------|---------|
| 21 | TCP | FTP |
| 22 | TCP | SSH |
| 23 | TCP | Telnet |
| 80 | TCP | HTTP |
| 161 | UDP | SNMP |
| 443 | TCP | HTTPS |
| 830 | TCP | NETCONF |
| 5000 | TCP | vrnetlab QEMU telnet |
| 5900 | TCP | VNC |
| 6030 | TCP | gNMI (Arista) |
| 9339 | TCP | gNMI/gNOI |
| 9340 | TCP | gRIBI |
| 9559 | TCP | P4RT |
| 57400 | TCP | gNMI (Nokia) |

## Expose Type Options

| Value | Description |
|-------|-------------|
| `LoadBalancer` | External access via cloud LB (default) |
| `ClusterIP` | Internal cluster access only |
| `None` | No services, but config preserved |

## Related

See also: [Expose Configuration Guide](../../docs/guides/expose-configuration.md)
