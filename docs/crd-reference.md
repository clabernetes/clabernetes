# Clabernetes CRD Reference

This document provides a comprehensive reference for all Custom Resource Definitions (CRDs) in Clabernetes.

## Table of Contents

- [Topology CRD](#topology-crd)
  - [TopologySpec Fields](#topologyspec-fields)
  - [TopologyStatus Fields](#topologystatus-fields)
- [Config CRD](#config-crd)
- [Connectivity CRD](#connectivity-crd)
- [ImageRequest CRD](#imagerequest-crd)

---

## Topology CRD

The `Topology` CRD is the primary resource for defining network topologies in Clabernetes. It represents a containerlab (or KNE) topology that will be deployed across Kubernetes pods.

### Basic Structure

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: my-topology
spec:
  definition:
    containerlab: |
      # Your containerlab topology YAML here
  expose: {}
  deployment: {}
  statusProbes: {}
  imagePull: {}
  naming: global
  connectivity: vxlan
```

### TopologySpec Fields

#### definition (required)

Holds the underlying topology definition. A Topology must have exactly one definition type.

| Field | Type | Description |
|-------|------|-------------|
| `containerlab` | string | A valid containerlab topology in YAML format |
| `kne` | string | A valid KNE topology (alternative to containerlab) |

**Example:**

```yaml
spec:
  definition:
    containerlab: |
      name: my-lab
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

#### expose

Configures how clabernetes exposes topology nodes via Kubernetes services.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `disableExpose` | bool | `false` | Completely disables service creation for all nodes |
| `disableAutoExpose` | bool | `false` | Disables automatic port exposure (see auto-exposed ports below) |
| `exposeType` | enum | `LoadBalancer` | Service type: `None`, `ClusterIP`, or `LoadBalancer` |
| `useNodeMgmtIpv4Address` | bool | `false` | Use node's `mgmt-ipv4` address for LoadBalancer IP |
| `useNodeMgmtIpv6Address` | bool | `false` | Use node's `mgmt-ipv6` address for LoadBalancer IP |

**Auto-Exposed Ports** (when `disableAutoExpose: false`):

- 21/tcp (FTP)
- 22/tcp (SSH)
- 23/tcp (Telnet)
- 80/tcp (HTTP)
- 161/udp (SNMP)
- 443/tcp (HTTPS)
- 830/tcp (NETCONF)
- 5000/tcp (vrnetlab QEMU telnet)
- 5900/tcp (VNC)
- 6030/tcp (gNMI - Arista)
- 9339/tcp (gNMI/gNOI)
- 9340/tcp (gRIBI)
- 9559/tcp (P4RT)
- 57400/tcp (gNMI - Nokia)

**Example:**

```yaml
spec:
  expose:
    disableAutoExpose: true
    exposeType: ClusterIP
```

#### deployment

Configures deployment-related settings for launcher pods.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `resources` | map[string]ResourceRequirements | - | Resource limits per node (or "default") |
| `scheduling` | Scheduling | - | Node selector and tolerations |
| `privilegedLauncher` | *bool | `true` | Run launcher pods in privileged mode |
| `filesFromConfigMap` | map[string][]FileFromConfigMap | - | Mount files from ConfigMaps |
| `filesFromURL` | map[string][]FileFromURL | - | Download files from URLs |
| `persistence` | Persistence | - | PVC configuration for persistent storage |
| `containerlabDebug` | *bool | - | Enable containerlab debug logging |
| `containerlabTimeout` | string | - | Containerlab deploy timeout |
| `containerlabVersion` | string | - | Override containerlab version |
| `launcherImage` | string | - | Override default launcher image |
| `launcherImagePullPolicy` | enum | - | `IfNotPresent`, `Always`, or `Never` |
| `launcherLogLevel` | enum | - | `disabled`, `critical`, `warn`, `info`, or `debug` |
| `extraEnv` | []EnvVar | - | Additional environment variables |

##### Persistence

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable persistent storage for lab directory |
| `claimSize` | string | `5Gi` | PVC size (e.g., "10Gi") |
| `storageClassName` | string | - | Storage class name (uses default if empty) |

**Example:**

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "10Gi"
      storageClassName: "fast-ssd"
```

##### Scheduling

| Field | Type | Description |
|-------|------|-------------|
| `nodeSelector` | map[string]string | Kubernetes node selector labels |
| `tolerations` | []Toleration | Pod tolerations |

**Example:**

```yaml
spec:
  deployment:
    scheduling:
      nodeSelector:
        kubernetes.io/arch: amd64
        disktype: ssd
      tolerations:
        - key: "network-lab"
          operator: "Equal"
          value: "true"
          effect: "NoSchedule"
```

##### FileFromConfigMap

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filePath` | string | Yes | Destination path in the pod |
| `configMapName` | string | Yes | Name of the ConfigMap |
| `configMapPath` | string | No | Specific key in ConfigMap to mount |
| `mode` | enum | No | `read` (0o444) or `execute` (0o555), default: `read` |

**Example:**

```yaml
spec:
  deployment:
    filesFromConfigMap:
      srl1:
        - filePath: /opt/srlinux/etc/license.key
          configMapName: srl-license
          configMapPath: license.key
```

##### FileFromURL

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `filePath` | string | Yes | Destination path in the pod |
| `url` | string | Yes | URL to download the file from |

**Example:**

```yaml
spec:
  deployment:
    filesFromURL:
      srl1:
        - filePath: /tmp/config.json
          url: https://example.com/configs/srl1.json
```

##### Resources

Resources are specified per node name, or use "default" for all nodes:

```yaml
spec:
  deployment:
    resources:
      default:
        requests:
          memory: "2Gi"
          cpu: "1"
        limits:
          memory: "4Gi"
          cpu: "2"
      srl1:  # Override for specific node
        requests:
          memory: "8Gi"
          cpu: "4"
```

#### statusProbes

Configures health checking for containerlab nodes.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | Enable status probes |
| `excludedNodes` | []string | - | Nodes to exclude from probing |
| `probeConfiguration` | ProbeConfiguration | - | Default probe settings |
| `nodeProbeConfigurations` | map[string]ProbeConfiguration | - | Per-node probe settings |

##### ProbeConfiguration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `startupSeconds` | int | ~780 (13min) | Startup probe timeout |
| `sshProbeConfiguration` | SSHProbeConfiguration | - | SSH-based probe |
| `tcpProbeConfiguration` | TCPProbeConfiguration | - | TCP-based probe |

##### SSHProbeConfiguration

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `username` | string | Yes | SSH username |
| `password` | string | Yes | SSH password |
| `port` | int | No | SSH port (default: 22) |

##### TCPProbeConfiguration

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `port` | int | Yes | TCP port to probe |

**Example:**

```yaml
spec:
  statusProbes:
    enabled: true
    excludedNodes:
      - host1
    probeConfiguration:
      startupSeconds: 900
      sshProbeConfiguration:
        username: admin
        password: NokiaSrl1!
        port: 22
```

#### imagePull

Configures image pulling behavior for launcher pods.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `insecureRegistries` | []string | - | List of insecure registries |
| `pullThroughOverride` | enum | - | `auto`, `always`, or `never` |
| `pullSecrets` | []string | - | Secret names for private registries |
| `dockerDaemonConfig` | string | - | Secret name containing daemon.json |
| `dockerConfig` | string | - | Secret name containing config.json |

**Example:**

```yaml
spec:
  imagePull:
    pullThroughOverride: always
    pullSecrets:
      - my-registry-secret
    insecureRegistries:
      - my-registry.local:5000
```

#### naming

Controls resource naming convention.

| Value | Description |
|-------|-------------|
| `global` | Use global Config setting (default) |
| `prefixed` | Include topology name as prefix in resources |
| `non-prefixed` | Don't include topology name prefix |

**Note:** This field is immutable after creation. Use `non-prefixed` only when deploying topologies in separate namespaces.

#### connectivity

Tunnel type for inter-node connectivity.

| Value | Description |
|-------|-------------|
| `vxlan` | VXLAN tunnels (default) |
| `slurpeeth` | Experimental TCP tunnel mode |

---

### TopologyStatus Fields

The `status` subresource is managed exclusively by the controller. It is never set by the
user. Use `kubectl get topology <name> -o yaml` or `kubectl explain topology.status` to
inspect the current values.

#### topologyState

The current lifecycle state of the topology. Visible as the **State** column in
`kubectl get topologies`.

| Value | Description |
|-------|-------------|
| `deploying` | Resources are being created/updated; not all nodes have reported ready yet. |
| `deployfailed` | One or more nodes entered a terminal failure (CrashLoopBackOff / pod Failed) before the topology ever reached `running`. |
| `running` | All nodes have reported ready. The topology is fully operational. |
| `degraded` | The topology was previously `running` but one or more nodes have since become unready or started crashing. |
| `destroying` | A delete request has been received. The controller holds this state for ~5 s so external watchers can observe the transition. |
| `destroyfailed` | The finalizer removal patch failed during deletion. The object remains until the issue is resolved. |

See the [Topology Lifecycle guide](guides/topology-lifecycle.md) for the full state machine
diagram and troubleshooting steps.

#### topologyReady

Boolean. `true` when every node in the topology has reported ready. Mirrors the
`TopologyReady` condition and is surfaced here for `kubectl get` print columns.

#### nodeReadiness

Map of node name → simplified readiness string. Updated every reconcile cycle.

| Value | Description |
|-------|-------------|
| `ready` | Startup and readiness probes both passing. |
| `notready` | Pod exists but probes have not yet passed. |
| `unknown` | No deployment found for this node. |
| `deploymentDisabled` | The topology has the `clabernetes/disableDeployments` label set. |

#### nodeProbeStatuses

Map of node name → per-probe status object. Provides finer-grained observability than
`nodeReadiness`. Each node entry has three fields:

| Field | Description |
|-------|-------------|
| `startupProbe` | Derived from `pod.status.containerStatuses[0].started`. Passing once the lab node writes its status file. |
| `readinessProbe` | Derived from `pod.status.containerStatuses[0].ready`. Passing when the node is ready to accept traffic. |
| `livenessProbe` | Inferred from container state: `Running` → passing; `CrashLoopBackOff` → failing; other → unknown. |

Possible values for all probe fields: `passing`, `failing`, `unknown`, `disabled`.

#### conditions

List of `metav1.Condition` entries managed by the controller. Currently contains:

| Type | True when | False when |
|------|-----------|------------|
| `TopologyReady` | All nodes report ready. | Any node is not ready. |

---

## Config CRD

The `Config` CRD holds global clabernetes configuration. There must be exactly one Config resource named `clabernetes`.

### Basic Structure

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Config
metadata:
  name: clabernetes  # Must be named "clabernetes"
spec:
  metadata: {}
  imagePull: {}
  deployment: {}
  naming: prefixed
```

### ConfigSpec Fields

#### metadata

Global metadata applied to all clabernetes-created resources.

| Field | Type | Description |
|-------|------|-------------|
| `annotations` | map[string]string | Annotations for created resources |
| `labels` | map[string]string | Labels for created resources |

**Example:**

```yaml
spec:
  metadata:
    annotations:
      environment: lab
    labels:
      managed-by: clabernetes
```

#### inClusterDNSSuffix

Override the in-cluster DNS suffix (default: `cluster.local`).

#### imagePull

Global image pull configuration.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `pullThroughOverride` | enum | `auto` | `auto`, `always`, or `never` |
| `criSockOverride` | string | - | Override CRI socket path (e.g., for K3s) |
| `criKindOverride` | enum | - | Override CRI type: `containerd` |
| `dockerDaemonConfig` | string | - | Default docker daemon config secret |
| `dockerConfig` | string | - | Default docker config secret |

**Example (K3s):**

```yaml
spec:
  imagePull:
    criSockOverride: /run/k3s/containerd/containerd.sock
```

#### deployment

Global deployment configuration.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `resourcesDefault` | ResourceRequirements | - | Default resources for all pods |
| `resourcesByContainerlabKind` | map | - | Resources by kind/type |
| `nodeSelectorsByImage` | map | - | Node selectors by image pattern |
| `privilegedLauncher` | bool | `false` | Default privileged mode |
| `containerlabDebug` | bool | `false` | Default debug logging |
| `containerlabTimeout` | string | - | Default deploy timeout |
| `containerlabVersion` | string | - | Override containerlab version |
| `launcherImage` | string | `ghcr.io/clabernetes/clabernetes/clabernetes-launcher:latest` | Default launcher image |
| `launcherImagePullPolicy` | enum | `IfNotPresent` | Default pull policy |
| `launcherLogLevel` | enum | - | Default log level |
| `extraEnv` | []EnvVar | - | Global environment variables |

##### resourcesByContainerlabKind

Resources can be set by containerlab kind and type:

```yaml
spec:
  deployment:
    resourcesByContainerlabKind:
      nokia_srlinux:
        default:
          requests:
            memory: "4Gi"
            cpu: "2"
        ixr10:
          requests:
            memory: "16Gi"
            cpu: "8"
      nokia_sros:
        default:
          requests:
            memory: "8Gi"
            cpu: "4"
```

##### nodeSelectorsByImage

Node selectors can be applied based on image patterns:

```yaml
spec:
  deployment:
    nodeSelectorsByImage:
      "ghcr.io/nokia/srlinux*":
        node-type: network
      "internal.io/sros*":
        node-type: baremetal
      "default":
        node-type: standard
```

#### naming

Global naming convention for resources.

| Value | Description |
|-------|-------------|
| `prefixed` | Include topology name as prefix (default) |
| `non-prefixed` | Don't include topology name prefix |

---

## Connectivity CRD

The `Connectivity` CRD is automatically managed by clabernetes to track point-to-point tunnels between nodes. Users typically do not create or modify this resource directly.

### Basic Structure

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Connectivity
metadata:
  name: my-topology
spec:
  pointToPointTunnels:
    srl1:
      - tunnelID: 1
        destination: my-topology-srl2.default.svc.cluster.local
        localNode: srl1
        localInterface: e1-1
        remoteNode: srl2
        remoteInterface: e1-1
```

### ConnectivitySpec Fields

#### pointToPointTunnels

Map of node names to their tunnel configurations.

##### PointToPointTunnel

| Field | Type | Description |
|-------|------|-------------|
| `tunnelID` | int | Tunnel ID (VNID or segment ID) |
| `destination` | string | Destination service FQDN |
| `localNode` | string | Local node name |
| `localInterface` | string | Local interface name |
| `remoteNode` | string | Remote node name |
| `remoteInterface` | string | Remote interface name |

---

## ImageRequest CRD

The `ImageRequest` CRD is automatically managed by clabernetes to coordinate image pulling. Launcher pods create these requests, and the controller processes them. Users typically do not create this resource directly.

### Basic Structure

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: ImageRequest
metadata:
  name: my-topology-srl1-abc123
spec:
  topologyName: my-topology
  topologyNodeName: srl1
  kubernetesNode: worker-1
  requestedImage: ghcr.io/nokia/srlinux:latest
  requestedImagePullSecrets:
    - my-registry-secret
status:
  accepted: true
  complete: false
```

### ImageRequestSpec Fields

| Field | Type | Description |
|-------|------|-------------|
| `topologyName` | string | Name of the requesting Topology |
| `topologyNodeName` | string | Node name in the topology |
| `kubernetesNode` | string | K8s node where image is needed |
| `requestedImage` | string | Container image to pull |
| `requestedImagePullSecrets` | []string | Pull secrets to use |

### ImageRequestStatus Fields

| Field | Type | Description |
|-------|------|-------------|
| `accepted` | bool | Controller has acknowledged the request |
| `complete` | bool | Image has been pulled successfully |

---

## Common Patterns

### Minimal Topology

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: minimal
spec:
  definition:
    containerlab: |
      name: minimal
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

### Production Topology

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: production
spec:
  definition:
    containerlab: |
      name: production
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
  expose:
    exposeType: LoadBalancer
    disableAutoExpose: false
  deployment:
    persistence:
      enabled: true
      claimSize: "20Gi"
    resources:
      default:
        requests:
          memory: "4Gi"
          cpu: "2"
    scheduling:
      nodeSelector:
        node-type: network
  statusProbes:
    enabled: true
    probeConfiguration:
      sshProbeConfiguration:
        username: admin
        password: NokiaSrl1!
  imagePull:
    pullThroughOverride: auto
```
