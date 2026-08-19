# Deployment Configuration Examples

This directory contains examples demonstrating deployment-related configurations for Clabernetes topologies.

## Examples

### with-persistence.yaml

Enable persistent storage for topology nodes. Node state (configs, logs, etc.) survives pod restarts.

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "10Gi"
      storageClassName: "fast-ssd"  # Optional
```

**Use cases:**
- Long-running labs where you don't want to lose config changes
- Nodes that require state persistence (e.g., after configuration saves)
- Development environments with iterative changes

**Notes:**
- A PVC is created for each node
- Claim size cannot be reduced after creation (can only increase)
- Uses default storage class if `storageClassName` not specified

### with-resources.yaml

Set resource requests and limits for launcher pods.

```yaml
spec:
  deployment:
    resources:
      default:           # Applied to all nodes
        requests:
          memory: "2Gi"
          cpu: "1"
      specific-node:     # Override for named node
        requests:
          memory: "8Gi"
```

**Use cases:**
- Ensuring consistent resource allocation
- Preventing resource starvation on busy clusters
- Meeting QoS requirements for critical topologies

### with-scheduling.yaml

Control pod placement with node selectors and tolerations.

```yaml
spec:
  deployment:
    scheduling:
      nodeSelector:
        node-type: network-lab
      tolerations:
        - key: "dedicated"
          operator: "Equal"
          value: "network-lab"
          effect: "NoSchedule"
```

**Use cases:**
- Running topologies on dedicated hardware (bare metal, GPU nodes)
- Isolating network labs from other workloads
- Meeting compliance requirements for workload placement

### with-configmap-files.yaml

Mount files from ConfigMaps into nodes.

```yaml
spec:
  deployment:
    filesFromConfigMap:
      srl1:
        - filePath: /opt/srlinux/etc/license.key
          configMapName: srl-license
          configMapPath: license.key
          mode: read  # or 'execute'
```

**Use cases:**
- Mounting license files
- Injecting startup configurations
- Providing certificates or keys

**Notes:**
- ConfigMap must exist in the same namespace
- `configMapPath` specifies the key within the ConfigMap
- `mode`: `read` (0o444) or `execute` (0o555)

## Additional Deployment Options

### Files from URL

Download files from URLs instead of ConfigMaps:

```yaml
spec:
  deployment:
    filesFromURL:
      srl1:
        - filePath: /tmp/config.json
          url: https://raw.githubusercontent.com/example/configs/main/srl.json
```

**Notes:**
- URL must be directly downloadable (raw file, not HTML page)
- Useful for files larger than ConfigMap 1MB limit
- Re-downloaded on pod restart

### Containerlab Options

```yaml
spec:
  deployment:
    containerlabDebug: true        # Enable debug logging
    containerlabTimeout: "30m"     # Deploy timeout
```

### Launcher Configuration

```yaml
spec:
  deployment:
    launcherImage: "my-registry/clabernetes-launcher:v1.0.0"
    launcherImagePullPolicy: Always
    launcherLogLevel: debug
    privilegedLauncher: true       # Default is true
    extraEnv:
      - name: MY_VAR
        value: "my-value"
```

## Combining Options

Multiple deployment options can be combined:

```yaml
spec:
  deployment:
    persistence:
      enabled: true
      claimSize: "20Gi"
    resources:
      default:
        requests:
          memory: "4Gi"
    scheduling:
      nodeSelector:
        disktype: ssd
    filesFromConfigMap:
      srl1:
        - filePath: /opt/srlinux/etc/license.key
          configMapName: srl-license
          configMapPath: license.key
```

## Related

- [Persistence Guide](../../docs/guides/persistence.md)
- [File Mounting Guide](../../docs/guides/file-mounting.md)
- [Resource Management Guide](../../docs/guides/resource-management.md)
- [CRD Reference: Deployment Fields](../../docs/crd-reference.md#deployment)
