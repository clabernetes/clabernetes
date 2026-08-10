# Basic Examples

This directory contains minimal examples to get started with Clabernetes.

These examples use the backward-compatible auxiliary `Topology` resource for convenience. The
controller compiles each file into explicitly referenced LauncherProfile, Link, and Node
resources. For direct primitive manifests (and large labs that should avoid persisting one
aggregate source object), use `clabverter --emit-crs`.

## Examples

### simple-srl.yaml

The simplest possible Clabernetes topology - a single Nokia SR Linux node.

```bash
kubectl apply -f simple-srl.yaml
```

This creates:

- One launcher pod running the SR Linux container
- A LoadBalancer service exposing common management ports (SSH, gNMI, etc.)

### srl-multitool.yaml

A Nokia SR Linux node connected to a Linux host running the network multitool image.

```bash
kubectl apply -f srl-multitool.yaml
```

This creates:

- One launcher pod running the SR Linux container
- One launcher pod running the multitool container
- A point-to-point link between `srl1:e1-1` and `multitool:eth1`
- IPv4 addressing: `192.0.2.0/31` on srl1 and `192.0.2.1/31` on multitool
- An SR Linux SSH readiness probe, with the multitool host excluded from probing

### two-nodes-connected.yaml

Two SR Linux nodes connected via a point-to-point link with pre-configured IP addressing.

```bash
kubectl apply -f two-nodes-connected.yaml
```

This creates:

- Two launcher pods, one for each SR Linux node
- VXLAN tunnels between the pods for the `e1-1` interface connection
- LoadBalancer services for each node
- Startup configs that configure:
  - IPv4 addressing: `192.168.0.0/31` on srl1, `192.168.0.1/31` on srl2
  - IPv6 addressing: `2002::192.168.0.0/127` on srl1, `2002::192.168.0.1/127` on srl2
  - Interfaces added to the default network instance

### inline-startup-config.yaml

Demonstrates inline startup configurations embedded directly in YAML using multiline syntax.

```bash
kubectl apply -f inline-startup-config.yaml
```

This shows how startup-config can be specified inline rather than as file references:

```yaml
startup-config: |
  set / interface ethernet-1/1 admin-state enable
  set / network-instance default interface ethernet-1/1.0
```

**Clabverter support:** When converting containerlab topologies with inline startup-config,
clabverter automatically detects the embedded content and creates ConfigMaps without
attempting to read from the filesystem. Both file path references and inline configs are supported.

## Accessing Nodes

Once deployed, get the service IPs:

```bash
kubectl get svc -l c9s.run/topologyOwner=<topology-name>
```

SSH to a node (default credentials: admin/NokiaSrl1!):

```bash
ssh admin@<service-ip>
```

## Checking Status

View topology status:

```bash
kubectl get topologies
```

Check node readiness:

```bash
kubectl get nodes.c9s.run
kubectl get node.c9s.run <node-name> -o yaml
```

Node readiness, probe observations, exposed ports, and the applied LauncherProfile identity are
resource-local Node status. Link tunnel allocation and errors are resource-local Link status.

## Cleanup

```bash
kubectl delete -f <filename>.yaml
```
