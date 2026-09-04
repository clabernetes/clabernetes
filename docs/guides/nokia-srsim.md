---
title: Nokia SR-SIM
description: Deploy integrated and distributed Nokia SR-SIM systems with Clabernetes.
---

This guide explains how to deploy Nokia SR-SIM (SR OS Simulator) topologies with Clabernetes, including supported configurations.

## Overview

Nokia SR-SIM is a containerized version of Nokia SR OS, replacing the legacy VM-based vSIM variant (`vr-sros`). SR-SIM is identified by the `nokia_srsim` kind in containerlab topology files.

## Prerequisites

1. **License**: A valid SR-SIM license file is mandatory. The `license` directive must point to the
   path where Clabernetes mounts the license, or the deployment will fail.

2. **Image**: The SR-SIM image must be reachable by the kubelet on every eligible worker. Loading
   an image only on the workstation is not sufficient for a remote Kubernetes cluster.

3. **Resources**: SR-SIM nodes require significant resources. Ensure your cluster nodes have adequate CPU and memory.

### Private Registry Images

Create a Kubernetes Docker registry Secret in the same namespace as the Topology:

```bash
kubectl create secret docker-registry srsim-registry \
  --docker-server=ghcr.io \
  --docker-username="$GITHUB_USER" \
  --docker-password="$GITHUB_TOKEN"
```

Reference that Secret from the Topology:

```yaml
spec:
  imagePull:
    pullSecrets:
      - srsim-registry
```

See [Image pulling](/docs/guides/image-pull) for a cluster-wide pull Secret default, registry
mirrors, and private CAs.

## Supported Configurations

### Integrated Systems

Integrated SR-SIM systems run as a single container. Nokia supports the integrated model only
for these platform types. Every other chassis, including the FP5 small fixed platforms such
as `sr-1-92s`, must run distributed (declare `components`):

| Platform Type | Description                      |
| ------------- | -------------------------------- |
| `sr-1`        | SR-1 integrated system (default) |
| `sr-1s`       | SR-1s integrated system          |
| `ixr-r6`      | 7250 IXR-R6                      |
| `ixr-ec`      | 7250 IXR-ec                      |
| `ixr-e2`      | 7250 IXR-e2                      |
| `ixr-e2c`     | 7250 IXR-e2c                     |

A componentless node of any other type boots a single simulator container that never attaches
its data-path ports: the Node reports ready on management but forwards nothing.

**Example topology:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: srsim-license
data:
  license.txt: |
    # Your license content here

---
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: srsim-integrated
spec:
  deployment:
    filesFromConfigMap:
      sr1:
        - filePath: /opt/nokia/sros/license.txt
          configMapName: srsim-license
          configMapPath: license.txt
      sr2:
        - filePath: /opt/nokia/sros/license.txt
          configMapName: srsim-license
          configMapPath: license.txt
  definition:
    containerlab: |
      name: srsim-integrated
      topology:
        kinds:
          nokia_srsim:
            image: nokia_srsim:25.7.R1
            license: /opt/nokia/sros/license.txt
        nodes:
          sr1:
            kind: nokia_srsim
            type: sr-1
          sr2:
            kind: nokia_srsim
            type: sr-1s
```

### Distributed Chassis Systems

Distributed chassis-based SR-SIM systems (SR-7, SR-14s, etc.) simulate a single chassis using
multiple containers, one for each card slot (CPM-A, CPM-B, IOMs). In the explicit-card form,
secondary containers share a network namespace via `network-mode: container:<name>`; in the
component form, the imported containerlab package expands the node into card containers during
planning.

| Platform Type | Description |
| --------------- | ------------- |
| `sr-2s` | SR-2s chassis system |
| `sr-2se` | SR-2se chassis system |
| `sr-7` | SR-7 chassis system |
| `sr-14s` | SR-14s chassis system |
| `sr-1-92s` | SR-1-92s system |

**Terminology:**

- **Chassis**: A single SR OS router (e.g., one SR-7). In Clabernetes, a chassis is represented by a group of containers deployed in the same pod.
- **Cards/Slots**: Components within a chassis (CPM-A, CPM-B, IOM-1, etc.). Each card runs as a separate container sharing the chassis's network namespace.

**How it works:**

Containerlab supports two ways to describe the cards. A single logical node can use a `components`
block, in which case the imported containerlab package expands it into card containers and
constructs the internal fabric. Alternatively, each card can be an explicit node and secondary
cards can use `network-mode: container:<primary-card>`. Clabernetes keeps every card of either
form in one device Pod and one shared network namespace as required by distributed SR-SIM; the
imported containerlab package remains responsible for the SR-SIM expansion and fabric setup, and
each card runs as a directly visible application container of that Pod.

The component form is the closest match to a normal containerlab topology:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: srsim-license
data:
  license.txt: |
    # Your license content here

---
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: srsim-components
spec:
  deployment:
    filesFromConfigMap:
      srsim:
        - filePath: /opt/nokia/sros/license.txt
          configMapName: srsim-license
          configMapPath: license.txt
  definition:
    containerlab: |
      name: srsim-components
      topology:
        nodes:
          srsim:
            kind: nokia_srsim
            image: nokia_srsim:25.10.R1
            type: sr-7
            license: /opt/nokia/sros/license.txt
            components:
              - slot: A
              - slot: 1
                type: iom5-e
                sfm: m-sfm6-7/12
                mda:
                  - slot: 1
                    type: me6-100gb-qsfp28
          client:
            kind: linux
            image: ghcr.io/srl-labs/network-multitool:latest
        links:
          - type: veth
            endpoints:
              - node: srsim
                interface: 1/1/c1/1
              - node: client
                interface: eth1
```

The structured `veth` endpoints above compile to the same c9s Link as brief endpoints such as
`["srsim:1/1/c1/1", "client:eth1"]`. Clabernetes still creates one Node and one device Pod for
`srsim`; the planned card containers are directly visible application containers of that Pod, so
`kubectl logs` and `kubectl exec` reach a specific card with `-c`. An empty `components: []`
declaration is also accepted for SR-SIM images that use Containerlab's default component
expansion. In every component form, Clabernetes requires
one namespace owner and verifies that every dependent component resolves into that namespace;
invalid ownership fails planning rather than selecting a card arbitrarily.

The same Containerlab definition can be converted with `clabverter --emit-crs`. The imported
containerlab package remains responsible for component expansion and fabric construction, while
Clabernetes owns the Kubernetes Node, device Pod, readiness, and shared payload lifecycle.

**Example distributed topology:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: srsim-license
data:
  license.txt: |
    # Your license content here

---
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: srsim-distributed
spec:
  deployment:
    filesFromConfigMap:
      srsim-a:
        - filePath: /opt/nokia/sros/license.txt
          configMapName: srsim-license
          configMapPath: license.txt
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
          client:
            kind: linux
            image: ghcr.io/srl-labs/network-multitool:latest
        links:
          # Links to other chassis or nodes leave the Pod over the cluster fabric
          - endpoints: ["srsim-iom1:1/1/c1/1", "client:eth1"]
```

**Key points for distributed mode:**

1. **Primary card**: The card without `network-mode` (typically CPM-A) is the primary. The Deployment/Pod and its resource policy are associated with this card's name.

2. **Secondary cards**: Cards with `network-mode: container:<primary>` (CPM-B, IOMs) are grouped with their primary and deployed in the same pod.

3. **Links**: Links between cards in the same chassis stay inside the Pod's shared network namespace. Links to other chassis or external nodes cross the cluster fabric: veth legs whose sidecar side feeds the Pod's fabric wire riding the cluster network.

4. **Service names**: Every card Node receives its own services, all selecting the shared chassis pod; the primary card name always works when connecting from other pods.

5. **Multiple chassis**: If you deploy multiple distributed chassis (e.g., two SR-7 routers), each chassis gets its own pod. Different chassis can be scheduled on different Kubernetes worker nodes.

### Card and Component Configuration

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

For a distributed chassis, include the card inventory in the `components` block when containerlab
should generate the corresponding SR OS card configuration:

```yaml
nodes:
  srsim:
    kind: nokia_srsim
    type: sr-7
    components:
      - slot: A
      - slot: 1
        type: iom5-e
        sfm: m-sfm6-7/12
        mda:
          - slot: 1
            type: me6-100gb-qsfp28
```

A component entry containing only `slot` starts that card's simulator container but does not tell
containerlab which SR OS card, SFM, or MDA to provision. Supply `type`, `sfm`, and `mda` inventory
when automatic card provisioning is required.

### Platform Rules Cheat Sheet

Modular chassis follow real SR OS equipage semantics, so a lab that skips them fails with exact
SR OS diagnostics rather than booting degraded:

- **Modular chassis need typed components.** SR OS requires configuration-side equipage
  (`card <slot> card-type ...`, `mda <slot> mda-type ...`) before any port on that card exists.
  Containerlab generates those lines only from typed `components` entries; a bare `slot: 1`
  yields `MGMT_CORE #4001: ... MDA must exist` at config commit and dead ports.
- **Card/MDA pairings come from Nokia's supported-hardware tables** (the SR-SIM Installation
  and Setup appendix). Pairing an MDA with the wrong card fails commit with
  `Not supported by chassis/card/xiom type/capability`. For example `me6-100gb-qsfp28`
  pairs with `iom5-e`, not `iom4-e`.
- **XIOM-based cards take `ms*` MDAs.** On `-s` chassis (`sr-2s`, `sr-7s`, ...) the XIOM MDA
  must be one of the `ms*` types (`ms18-100gb-qsfp28`, `ms24-10/100gb-sfpdd`, ...); the config
  commit rejects anything else and names the accepted keywords. Ports gain an `x` element:
  `1/x1/1/c1/1` maps to interface `1/x1/1/c1/1` on the Link and `e1-x1-1-c1-1` in the Pod.
- **SR-SIM forwards at most ~1000 pps per port** by design; it validates control-plane and
  reachability behavior, not throughput.
- **Live link changes work; leave the default `link-apply-mode` alone.** Although Nokia
  documents data-path interfaces attached at container start only, the simulator hot-attaches
  interfaces added or recreated at runtime. Two practical caveats: a linux peer's boot-time
  `exec` addressing does not survive its own veth being recreated (exactly as on a containerlab
  host), and a freshly changed wire can take a few seconds of ARP convergence. If you do
  override the mode, prefer `recreate` (a clean Pod roll) and avoid `restart` for
  multi-container chassis: SR OS exits non-zero on its stop signal, so the kubelet treats
  each in-place restart as a failure and applies exponential backoff while the card
  containers crash-loop until their CPM is back.

## Interface Naming

SR-SIM uses a specific interface naming convention:

```text
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
apiVersion: c9s.run/v1alpha1
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

For distributed chassis, specify resources for the primary card (the policy applies to each card's application container in the chassis pod):

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
apiVersion: c9s.run/v1alpha1
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

All components of one logical node see the same license because the preparation container stages
and verifies the node's payload once, and every card container mounts it at the planned path.
For the explicit-card form, attach the license to the primary Node when authoring primitive resources
directly. If converted group members repeat the same shared destination, Clabernetes renders one Pod
mount at that normalized path only when the ConfigMap, key, and mode agree. A conflicting repeated
attachment stops reconciliation instead of silently choosing one license.

## Limitations

### Cards Within a Chassis Must Be Co-located

All cards (CPM-A, CPM-B, IOMs) within a single distributed chassis must run on the same Kubernetes worker node. This is a fundamental constraint of Linux network namespaces: they cannot span multiple hosts.

A single Kubernetes worker must therefore have enough CPU and memory for all cards of a chassis.
Use node selectors or tolerations to place chassis Pods on suitably sized workers, or use an
integrated type such as `sr-1` when resources are tight. Different chassis in one topology can
still run on different workers.

### Port Publishing

All cards of one chassis share a single network namespace, so exposed ports are chassis-wide rather than per-card: every card Node's expose service targets that shared namespace, and the default auto-exposed port set is allocated once across the group.

## Related Resources

- [Containerlab SR-SIM Documentation](https://containerlab.dev/manual/kinds/sros/)
- [SR-SIM Lab Examples](https://github.com/srl-labs/containerlab/tree/main/lab-examples/sr-sim)
- [File Mounting Guide](/docs/guides/file-mounting)
- [Resource Management Guide](/docs/guides/resource-management)
