# Advanced Examples

This directory contains advanced Clabernetes configurations for complex scenarios.

## Examples

### multi-vendor.yaml

Mixed-vendor topology with Nokia SRL, SR OS, and Cisco devices.

**Features:**
- Multiple device kinds in one topology
- Per-node resource configuration
- Heterogeneous link connections

**Use cases:**
- Multi-vendor interoperability testing
- Realistic production network simulation
- Vendor comparison studies

### spine-leaf.yaml

Data center spine-leaf (Clos) architecture with 2 spines and 4 leaves.

**Features:**
- Full mesh connectivity between tiers
- Persistence for configuration retention
- Status probes for health monitoring
- Default node kind/image for DRY configuration

**Use cases:**
- Data center fabric testing
- BGP/EVPN validation
- Automation testing at scale

### with-status-probes.yaml

Custom health probe configuration for different node types.

**Features:**
- SSH probes with custom credentials per node
- TCP probes for nodes without SSH
- Node exclusion from probing
- Custom startup timeouts

**Probe types:**
- `sshProbeConfiguration`: SSH login validation
- `tcpProbeConfiguration`: TCP port connectivity check

**Use cases:**
- Heterogeneous labs with different device types
- Automated deployment validation
- CI/CD pipeline integration

### private-registry.yaml

Using images from private container registries.

**Features:**
- Pull secrets for registry authentication
- Kubernetes-native pull policy
- Cluster-runtime registry configuration
- Separate controller metadata trust policy

**Setup:**
```bash
# Create pull secret
kubectl create secret docker-registry my-registry-secret \
  --docker-server=registry.example.com \
  --docker-username=myuser \
  --docker-password=mypass
```

**Use cases:**
- Enterprise environments with private registries
- Air-gapped deployments
- Custom/modified container images

### slurpeeth-connectivity.yaml

Declares `slurpeeth` connectivity on the Topology.

Both accepted values map onto the controller-selected realization: the device always sees a
plain veth, same-worker endpoint pairs are patched directly in the worker host namespace, and
cross-worker pairs use a VXLAN tunnel keyed by the Link's allocated tunnel id. Wire semantics
(L2 point-to-point, MTU intent, live rewires, cleanup) are identical for both values.

**Connectivity options:**
| Mode | Description |
|------|-------------|
| `vxlan` | Accepted intent (default) |
| `slurpeeth` | Accepted intent; same realization |

## Combining Advanced Features

Multiple features can be combined:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: production-like
spec:
  connectivity: vxlan
  expose:
    exposeType: LoadBalancer
    disableAutoExpose: true
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
        node-type: network-lab
  statusProbes:
    enabled: true
    probeConfiguration:
      sshProbeConfiguration:
        username: admin
        password: NokiaSrl1!
  imagePull:
    policy: IfNotPresent
    pullSecrets:
      - registry-credentials
  definition:
    containerlab: |
      name: production
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: registry.example.com/nokia/srlinux:latest
            ports:
              - 22/tcp
              - 57400/tcp
```

## Related

- [CRD Reference](../../docs/crd-reference.md)
- [Image Pull Guide](../../docs/guides/image-pull.md)
- [Resource Management Guide](../../docs/guides/resource-management.md)
