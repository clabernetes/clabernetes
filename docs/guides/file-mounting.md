---
title: File mounting
description: Mount configuration, licenses, and other files into network nodes.
---

c9s places files into device Pods from three sources: Kubernetes ConfigMaps, Kubernetes
Secrets, and HTTP(S) URLs.

Every declared file is a payload of the node's device plan: its content digest is recorded in
the accepted plan, and the preparation init container stages it into plan-scoped volumes and
verifies every path, mode, and digest before the device containers start. Changing a payload's
content therefore produces a new plan and recreates the Pod. Arbitrary host binds are rejected
before a workload is created.

## Files from ConfigMaps

Create a ConfigMap with the file content:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: srl-license
  namespace: default
data:
  license.key: |
    # license content
    AAAAB3NzaC1yc2EAAA...
```

Or create it from a file:

```bash
kubectl create configmap srl-license --from-file=license.key=/path/to/license.key
```

Reference the ConfigMap from a Topology under `spec.deployment.filesFromConfigMap`, keyed by
node name:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Topology
metadata:
  name: with-license
spec:
  deployment:
    filesFromConfigMap:
      srl1:  # node name
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

The compiler moves each map entry onto the corresponding generated Node; it does not create a
one-off NodeProfile just to carry payload.

For directly authored primitive resources, payload attachments live on the Node spec itself
(NodeProfile carries deployment policy only, never per-node files):

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

A node can mount any number of files, from any mix of ConfigMaps:

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
      mode: execute
```

### FileFromConfigMap fields

| Field | Required | Description |
| ------- | ---------- | ------------- |
| `filePath` | Yes | Destination path inside the pod |
| `configMapName` | Yes | Name of the ConfigMap |
| `configMapPath` | No | Specific key in the ConfigMap (mounts the entire ConfigMap if omitted) |
| `mode` | No | `read` (0o444) or `execute` (0o555), default: `read` |

## Files from Secrets

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

### FileFromSecret fields

| Field | Required | Description |
| ------- | ---------- | ------------- |
| `filePath` | Yes | Destination path, or destination directory when `secretPath` is omitted |
| `secretName` | Yes | Name of the same-namespace Secret |
| `secretPath` | No | Secret data key (all keys are projected beneath `filePath` if omitted) |
| `mode` | No | `read` (0o444) or `execute` (0o555), default: `read` |

## Files from URLs

```yaml
spec:
  deployment:
    filesFromURL:
      srl1:
        - filePath: /tmp/config.json
          url: https://raw.githubusercontent.com/example/configs/main/srl1.json
          digest: sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
```

### FileFromURL fields

| Field | Required | Description |
|-------|----------|-------------|
| `filePath` | Yes | Destination path inside the pod |
| `url` | Yes | URL to download the file from |
| `digest` | Yes | `sha256:<64 hex>` digest of the file bytes; a download that differs fails |

The digest pins the payload: a mutable URL cannot change a device's content without a matching
change to the accepted plan. Compute it with `sha256sum <file>`.

### URL requirements

- The URL must be a direct file download, not an HTML page. For GitHub content use the "raw"
  URL (`raw.githubusercontent.com/...`), not the `github.com/.../blob/...` page.
- It must be an absolute HTTP(S) URL without embedded credentials.
- The endpoint must be publicly resolvable: the fetch runs from the planning worker and again
  from the preparation init container, and hosts that resolve to private, loopback, or
  otherwise non-public addresses are rejected.
- Downloads are capped at 64 MB.

URL payloads are fetched anonymously. For content behind authentication, load it into a
ConfigMap or Secret and reference that instead. Registry credentials belong in Kubernetes
`imagePull.pullSecrets`, not in file mounting.

## Inline startup configurations with clabverter

When converting containerlab topologies with clabverter, `startup-config` can be a file path
reference:

```yaml
nodes:
  srl1:
    kind: nokia_srlinux
    startup-config: configs/srl1.cfg
```

or an inline configuration embedded in the YAML:

```yaml
nodes:
  srl1:
    kind: nokia_srlinux
    startup-config: |
      set / interface ethernet-1/1 admin-state enable
      set / interface ethernet-1/1 subinterface 0 ipv4 address 10.0.0.1/24
      set / network-instance default interface ethernet-1/1.0
```

Clabverter detects inline configurations (by checking for newlines in the value) and creates
ConfigMaps without attempting to read from the filesystem. Both styles are converted to the
same Kubernetes ConfigMap format.

## ConfigMap or URL?

| Aspect | ConfigMap | URL |
| -------- | ----------- | ----- |
| Size limit | 1 MB | 64 MB |
| Updates | New content = new plan, Pod recreated | Change file *and* `digest` together |
| Security | In-cluster (use Secrets for sensitive bytes) | Public endpoint required |
| Best for | Small configs | Large files, external sources |

If a ConfigMap payload exceeds the 1 MB limit, switch to URL-based mounting or split the
content across multiple ConfigMaps.

## Troubleshooting

If a file does not appear in the device, confirm the ConfigMap exists and check the Pod
events:

```bash
kubectl get configmap <name>
kubectl describe pod <pod-name>
```

A URL fetch or digest failure during planning is reported on the Node before any workload
exists; a failure during staging shows up in the preparation init container:

```bash
kubectl describe nodes.c9s.run <node>
kubectl logs deploy/<node> -c planner
```

Verify URL accessibility from inside the cluster:

```bash
kubectl run curl-test --rm -it --image=curlimages/curl -- curl -I <url>
```

## Related

- [Example: with-configmap-files.yaml](https://github.com/clabernetes/clabernetes/blob/main/examples/deployment/with-configmap-files.yaml)
- [Node CRD reference](/docs/crd/node)
