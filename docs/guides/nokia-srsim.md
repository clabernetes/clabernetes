# Nokia SR-SIM Support Guide

This guide explains how to deploy Nokia SR-SIM (SR OS Simulator) topologies with Clabernetes, including supported configurations.

## Overview

Nokia SR-SIM is a containerized version of Nokia SR OS, replacing the legacy VM-based vSIM variant (`vr-sros`). SR-SIM is identified by the `nokia_srsim` kind in containerlab topology files.

## Prerequisites

1. **License**: A valid SR-SIM license file is mandatory. The license must be provided via the `license` directive or the deployment will fail.

2. **Image**: The SR-SIM container image must be downloaded from the Nokia Support Portal and loaded into your container runtime.

3. **Resources**: SR-SIM nodes require significant resources. Ensure your cluster nodes have adequate CPU and memory.

## Supported Configurations

### Integrated Systems

Integrated SR-SIM systems run as a single container:

| Platform Type | Description |
|---------------|-------------|
| `sr-1` | SR-1 integrated system (default) |
| `sr-1s` | SR-1s integrated system |

**Example topology:**

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: srsim-integrated
spec:
  definition:
    containerlab: |
      name: srsim-integrated
      topology:
        kinds:
          nokia_srsim:
            image: nokia_srsim:25.7.R1
            license: /path/to/license.txt
        nodes:
          sr1:
            kind: nokia_srsim
            type: sr-1
            startup-config: sr1-config.cfg
          sr2:
            kind: nokia_srsim
            type: sr-1s
```

### Distributed Chassis Systems

Distributed chassis-based SR-SIM systems (SR-7, SR-14s, etc.) simulate a single chassis using multiple containers—one for each card slot (CPM-A, CPM-B, IOMs). These containers share a network namespace via the `network-mode: container:<name>` directive.

| Platform Type | Description |
| --------------- | ------------- |
| `sr-2s` | SR-2s chassis system |
| `sr-2se` | SR-2se chassis system |
| `sr-7` | SR-7 chassis system |
| `sr-14s` | SR-14s chassis system |
| `sr-1x-92S` | SR-1x-92S system |

**Terminology:**

- **Chassis**: A single SR OS router (e.g., one SR-7). In Clabernetes, a chassis is represented by a group of containers deployed in the same pod.
- **Cards/Slots**: Components within a chassis (CPM-A, CPM-B, IOM-1, etc.). Each card runs as a separate container sharing the chassis's network namespace.

**How it works:**

Clabernetes automatically detects containers with `network-mode: container:<primary-card>` and groups them together as a single chassis. All cards in a chassis are deployed in the same launcher pod, allowing them to share the network namespace as required by distributed SR-SIM.

**Example distributed topology:**

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: srsim-distributed
spec:
  deployment:
    resources:
      # Resources are specified for the primary card (chassis leader)
      srsim-a:
        requests:
          memory: "8Gi"
          cpu: "4"
        limits:
          memory: "16Gi"
          cpu: "8"
  definition:
    containerlab: |
      name: srsim-distributed
      topology:
        kinds:
          nokia_srsim:
            image: nokia_srsim:25.7.R1
            license: /opt/nokia/sros/license.txt
        nodes:
          # Primary card (CPM-A) - this is the chassis leader
          srsim-a:
            kind: nokia_srsim
            type: sr-7
            env:
              NOKIA_SROS_SLOT: A
          # Secondary card (CPM-B) - references primary via network-mode
          srsim-b:
            kind: nokia_srsim
            type: sr-7
            network-mode: container:srsim-a
            env:
              NOKIA_SROS_SLOT: B
          # IOM card - also references primary
          srsim-iom1:
            kind: nokia_srsim
            type: sr-7
            network-mode: container:srsim-a
            env:
              NOKIA_SROS_SLOT: "1"
        links:
          # Links to other chassis or external devices use VXLAN tunnels
          - endpoints: ["srsim-iom1:1/1/c1/1", "external-router:e1-1"]
```

**Key points for distributed mode:**

1. **Primary card**: The card without `network-mode` (typically CPM-A) is the primary. Resources, services, and tunnels are associated with this card's name.

2. **Secondary cards**: Cards with `network-mode: container:<primary>` (CPM-B, IOMs) are grouped with their primary and deployed in the same pod.

3. **Links**: Links between cards in the same chassis remain as local containerlab links. Links to other chassis or external nodes use VXLAN tunnels.

4. **Service names**: Services are created for the primary card only. Use the primary card name when connecting from other pods.

5. **Multiple chassis**: If you deploy multiple distributed chassis (e.g., two SR-7 routers), each chassis gets its own pod. Different chassis can be scheduled on different Kubernetes worker nodes.

### MDA and Component Configuration

For integrated systems, you can customize MDAs (Media Dependent Adapters) using environment variables:

```yaml
nodes:
  sr1:
    kind: nokia_srsim
    type: sr-1
    env:
      NOKIA_SROS_MDA_1: me6-100gb-qsfp28
      NOKIA_SROS_MDA_2: me12-10/1gb-sfp+
```

Or using the `components` block:

```yaml
nodes:
  sr1:
    kind: nokia_srsim
    type: sr-1
    components:
      - slot: 1
        env:
          NOKIA_SROS_MDA_1: me6-100gb-qsfp28
```

## Interface Naming

SR-SIM uses a specific interface naming convention:

```
L/xX/M/cC/P
```

- `L` - Line card slot
- `X` - MDA slot (optional for some platforms)
- `M` - MDA number
- `C` - Connector number
- `P` - Port number

**Example:** `1/1/c1/1` = Card 1, MDA 1, Connector 1, Port 1

In topology links:

```yaml
links:
  - endpoints: ["sr1:1/1/c1/1", "sr2:1/1/c1/1"]
```

## Resource Recommendations

SR-SIM nodes are resource-intensive. Configure appropriate resource limits:

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: srsim-with-resources
spec:
  deployment:
    resources:
      sr1:
        requests:
          memory: "4Gi"
          cpu: "2"
        limits:
          memory: "8Gi"
          cpu: "4"
  definition:
    containerlab: |
      name: srsim
      topology:
        nodes:
          sr1:
            kind: nokia_srsim
            type: sr-1
```

For distributed chassis, specify resources for the primary card (the pod runs all cards in the chassis):

```yaml
spec:
  deployment:
    resources:
      srsim-a:  # Primary card name (CPM-A)
        requests:
          memory: "8Gi"
          cpu: "4"
        limits:
          memory: "16Gi"
          cpu: "8"
```

## License File Mounting

The license file must be accessible to the SR-SIM container. Use ConfigMaps to mount the license:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: srsim-license
data:
  license.txt: |
    # Your license content here

---
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: srsim-with-license
spec:
  deployment:
    filesFromConfigMap:
      sr1:
        - filePath: /opt/nokia/sros/license.txt
          configMapName: srsim-license
          configMapPath: license.txt
  definition:
    containerlab: |
      name: srsim
      topology:
        kinds:
          nokia_srsim:
            license: /opt/nokia/sros/license.txt
        nodes:
          sr1:
            kind: nokia_srsim
            type: sr-1
```

## Limitations

### Cards Within a Chassis Must Be Co-located

All cards (CPM-A, CPM-B, IOMs) within a single distributed chassis must run on the same Kubernetes worker node. This is a fundamental constraint of Linux network namespaces—they cannot span multiple hosts.

**Impact:** A single Kubernetes worker must have sufficient resources (CPU, memory) for all cards in a chassis.

**Mitigations:**

- Use Kubernetes node selectors or taints/tolerations to ensure chassis pods are scheduled on appropriately sized nodes
- Consider using integrated SR-SIM types (sr-1, sr-1s) when resource constraints are a concern
- Plan cluster capacity with distributed chassis resource requirements in mind

### Different Chassis Can Be Distributed

While cards within a chassis must be co-located, different chassis (routers) in your topology can be scheduled on different Kubernetes worker nodes. For example, if you have two SR-7 routers in your topology, each can run on a different worker node—only the cards within each individual router must share a node.

### Port Publishing

Secondary cards (those with `network-mode: container:<primary>`) cannot have their own port mappings. All exposed ports are configured on the primary card and shared across the chassis via the common network namespace.

## Related Resources

- [Containerlab SR-SIM Documentation](https://containerlab.dev/manual/kinds/sros/)
- [SR-SIM Lab Examples](https://github.com/srl-labs/containerlab/tree/main/lab-examples/sr-sim)
- [File Mounting Guide](./file-mounting.md)
- [Resource Management Guide](./resource-management.md)
