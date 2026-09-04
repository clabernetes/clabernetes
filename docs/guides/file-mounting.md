---
title: File mounting
description: Mount configuration, licenses, and other files into network nodes.
---

This guide explains how to mount external files into Clabernetes topology nodes using ConfigMaps, Secrets, and URLs.

## Overview

Clabernetes supports three sources for placing files into device pods:

1. **ConfigMaps**: Mount files from Kubernetes ConfigMaps
2. **Secrets**: Mount sensitive files from Kubernetes Secrets
3. **URLs**: Download files from HTTP/HTTPS endpoints

Every declared file is a payload of the node's device plan: its content digest is recorded in
the accepted plan, and the preparation init container stages it into plan-scoped volumes and
verifies every path, mode, and digest before the device containers start. Changing a payload's
content therefore produces a new plan and recreates the Pod. Arbitrary host binds are rejected
before a workload is created.

## Mounting Files from ConfigMaps

### Creating the ConfigMap

First, create a ConfigMap with your file content:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: srl-license
  namespace: default
data:
  license.key: |
    # Your license content here
    AAAAB3NzaC1yc2EAAA...
```

Or create from a file:

```bash
kubectl create configmap srl-license --from-file=license.key=/path/to/license.key
```

### Mounting in Topology

Reference the ConfigMap in your topology:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: with-license
spec:
  deployment:
    filesFromConfigMap:
      srl1:  # Node name
        - filePath: /opt/srlinux/etc/license.key
          configMapName: srl-license
          configMapPath: license.key
          mode: read
  definition:
    containerlab: |
      name: licensed
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

Topology remains a supported auxiliary input. The compiler moves each map entry onto the
corresponding generated Node; it does not create a one-off NodeProfile just to carry payload.

### Mounting on a Direct Node

For directly authored primitive resources, payload attachments live in the Node spec:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
  filesFromConfigMap:
    - filePath: /opt/srlinux/etc/license.key
      configMapName: srl-license
      configMapPath: license.key
      mode: read
  filesFromURL:
    - filePath: /tmp/bootstrap.json
      url: https://example.com/bootstrap/srl1.json
      digest: sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
```

NodeProfile contains deployment policy only; it does not own per-node files.

### FileFromConfigMap Fields

| Field | Required | Description |
| ------- | ---------- | ------------- |
| `filePath` | Yes | Destination path inside the pod |
| `configMapName` | Yes | Name of the ConfigMap |
| `configMapPath` | No | Specific key in ConfigMap (mounts entire CM if omitted) |
| `mode` | No | `read` (0o444) or `execute` (0o555), default: `read` |

### Multiple Files

Mount multiple files to the same node:

```yaml
filesFromConfigMap:
  srl1:
    - filePath: /opt/srlinux/etc/license.key
      configMapName: srl-license
      configMapPath: license.key
    - filePath: /tmp/startup-config.json
      configMapName: srl-configs
      configMapPath: srl1-config.json
    - filePath: /tmp/custom-script.sh
      configMapName: scripts
      configMapPath: init.sh
      mode: execute  # Make script executable
```

### Files for Multiple Nodes

```yaml
filesFromConfigMap:
  srl1:
    - filePath: /opt/srlinux/etc/license.key
      configMapName: srl-license
      configMapPath: license.key
  srl2:
    - filePath: /opt/srlinux/etc/license.key
      configMapName: srl-license
      configMapPath: license.key
  srl3:
    - filePath: /opt/srlinux/etc/license.key
      configMapName: srl-license
      configMapPath: license.key
```

## Mounting Files from Secrets

Sensitive payloads (credentials, private keys, licenses under NDA) belong in Secrets rather
than ConfigMaps. Secret-backed payloads are marked sensitive: only their digest reaches the
plan, and the bytes are projected straight into the preparation container.

```yaml
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:latest
  filesFromSecret:
    - filePath: /opt/srlinux/etc/license.key
      secretName: srl-license
      secretPath: license.key
```

In a Topology, the equivalent map is `spec.deployment.filesFromSecret` keyed by node name.

### FileFromSecret Fields

| Field | Required | Description |
| ------- | ---------- | ------------- |
| `filePath` | Yes | Destination path, or destination directory when `secretPath` is omitted |
| `secretName` | Yes | Name of the same-namespace Secret |
| `secretPath` | No | Secret data key (all keys are projected beneath `filePath` if omitted) |
| `mode` | No | `read` (0o444) or `execute` (0o555), default: `read` |

## Mounting Files from URLs

### Basic URL Mount

```yaml
spec:
  deployment:
    filesFromURL:
      srl1:
        - filePath: /tmp/config.json
          url: https://raw.githubusercontent.com/example/configs/main/srl1.json
          digest: sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
```

### FileFromURL Fields

| Field | Required | Description |
|-------|----------|-------------|
| `filePath` | Yes | Destination path inside the pod |
| `url` | Yes | URL to download the file from |
| `digest` | Yes | `sha256:<64 hex>` digest of the file bytes; a download that differs fails |

The digest pins the payload: a mutable URL cannot change a device's content without a matching
change to the accepted plan. Compute it with `sha256sum <file>`.

### URL Requirements

- Must be a direct file download (not HTML page)
- GitHub: Use "raw" URLs
- Must be an absolute HTTP(S) URL without embedded credentials
- Must be a publicly resolvable endpoint: the fetch runs from the planning worker and again from
  the preparation init container, and hosts that resolve to private, loopback, or otherwise
  non-public addresses are rejected
- Downloads are capped at 64 MB

**Good URLs:**

```
https://raw.githubusercontent.com/user/repo/main/config.json
https://files.example.com/configs/router1.cfg
```

**Bad URLs:**

```
https://github.com/user/repo/blob/main/config.json  # HTML page
https://drive.google.com/file/d/xxx               # Requires auth
```

### Authentication

URL payloads are fetched anonymously. For content behind authentication, load it into a
ConfigMap or Secret and reference that instead. Registry credentials belong in Kubernetes
`imagePull.pullSecrets`, not in file mounting.

## Common Use Cases

### License Files

```yaml
# ConfigMap with license
apiVersion: v1
kind: ConfigMap
metadata:
  name: nokia-licenses
data:
  srl-license.key: |
    <license-content>
  sros-license.txt: |
    <license-content>
---
apiVersion: c9s.run/v1alpha1
kind: Topology
spec:
  deployment:
    filesFromConfigMap:
      srl1:
        - filePath: /opt/srlinux/etc/license.key
          configMapName: nokia-licenses
          configMapPath: srl-license.key
      sros1:
        - filePath: /tftpboot/license.txt
          configMapName: nokia-licenses
          configMapPath: sros-license.txt
```

### Startup Configurations

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: startup-configs
data:
  srl1.json: |
    {
      "system": {
        "name": {"host-name": "srl1-lab"}
      }
    }
  srl2.json: |
    {
      "system": {
        "name": {"host-name": "srl2-lab"}
      }
    }
---
spec:
  deployment:
    filesFromConfigMap:
      srl1:
        - filePath: /etc/opt/srlinux/config.json
          configMapName: startup-configs
          configMapPath: srl1.json
      srl2:
        - filePath: /etc/opt/srlinux/config.json
          configMapName: startup-configs
          configMapPath: srl2.json
```

### Inline Startup Configurations (Clabverter)

When using clabverter to convert containerlab topologies, startup-config can be specified in two ways:

**File path reference** (points to external file):

```yaml
nodes:
  srl1:
    kind: nokia_srlinux
    startup-config: configs/srl1.cfg
```

**Inline configuration** (embedded in YAML):

```yaml
nodes:
  srl1:
    kind: nokia_srlinux
    startup-config: |
      set / interface ethernet-1/1 admin-state enable
      set / interface ethernet-1/1 subinterface 0 ipv4 address 10.0.0.1/24
      set / network-instance default interface ethernet-1/1.0
```

Clabverter automatically detects inline configurations (by checking for newlines in the value) and creates ConfigMaps without attempting to read from the filesystem. Both styles are converted to the same Kubernetes ConfigMap format.

### TLS Certificates

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: lab-certs
data:
  ca.crt: |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
  server.crt: |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
  server.key: |
    -----BEGIN PRIVATE KEY-----
    ...
    -----END PRIVATE KEY-----
---
spec:
  deployment:
    filesFromConfigMap:
      srl1:
        - filePath: /etc/ssl/certs/ca.crt
          configMapName: lab-certs
          configMapPath: ca.crt
        - filePath: /etc/ssl/certs/server.crt
          configMapName: lab-certs
          configMapPath: server.crt
        - filePath: /etc/ssl/private/server.key
          configMapName: lab-certs
          configMapPath: server.key
```

### Custom Scripts

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: init-scripts
data:
  setup.sh: |
    #!/bin/bash
    echo "Running initialization..."
    # Your setup commands
---
spec:
  deployment:
    filesFromConfigMap:
      srl1:
        - filePath: /tmp/setup.sh
          configMapName: init-scripts
          configMapPath: setup.sh
          mode: execute  # 0o555 permissions
```

## ConfigMap vs URL

| Aspect | ConfigMap | URL |
| -------- | ----------- | ----- |
| Size limit | 1 MB | 64 MB |
| Updates | New content = new plan, Pod recreated | Change file *and* `digest` together |
| Security | In-cluster (use Secrets for sensitive bytes) | Public endpoint required |
| Versioning | Via K8s | Via URL versioning |
| Best for | Small configs | Large files, external sources |

## Troubleshooting

### File Not Appearing

Check ConfigMap exists:

```bash
kubectl get configmap <name>
```

Check pod events:

```bash
kubectl describe pod <pod-name>
```

### Permission Issues

Ensure correct mode:

- Scripts: `mode: execute`
- Config files: `mode: read` (default)

### ConfigMap Size Limit

If a file exceeds 1MB, mount it from a URL instead or split it into multiple ConfigMaps.

### URL Download Failures

A fetch or digest failure during planning is reported on the Node before any workload exists; a
failure during staging shows up in the preparation init container:

```bash
kubectl describe nodes.c9s.run <node>
kubectl logs deploy/<node> -c planner
```

Verify URL accessibility:

```bash
kubectl run curl-test --rm -it --image=curlimages/curl -- curl -I <url>
```

## Related

- [Example: with-configmap-files.yaml](https://github.com/clabernetes/clabernetes/blob/main/examples/deployment/with-configmap-files.yaml)
- [CRD Reference: FilesFromConfigMap](/docs/crd/node)
- [CRD Reference: FilesFromSecret](/docs/crd/node)
- [CRD Reference: FilesFromURL](/docs/crd/node)
