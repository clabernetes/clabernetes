# Image Pull Configuration Guide

This guide explains how to configure image pulling in Clabernetes, including private registries, pull secrets, and pull-through modes.

## Overview

Clabernetes launcher pods run containerlab, which in turn runs Docker to manage network device containers. Images can be pulled in several ways:

1. **Direct pull**: Docker in the launcher pulls images directly
2. **Pull-through**: Clabernetes pre-pulls images via the cluster CRI

## Pull-Through Modes

### Auto (Default)

Clabernetes automatically detects if pull-through is needed:

```yaml
spec:
  imagePull:
    pullThroughOverride: auto
```

**Behavior:**
- Checks if image exists in launcher's Docker
- If not, requests pull via ImageRequest CRD
- Controller creates a pull pod on the same node
- Image is pulled to node's CRI, then available to Docker

### Always

Force pull-through for all images:

```yaml
spec:
  imagePull:
    pullThroughOverride: always
```

**Use cases:**
- Private registries requiring cluster credentials
- Ensuring images are cached at CRI level
- Consistent behavior across all topologies

### Never

Disable pull-through, use Docker direct pull:

```yaml
spec:
  imagePull:
    pullThroughOverride: never
```

**Use cases:**
- Public images that Docker can pull directly
- When pull-through isn't working
- Debugging image pull issues

## Private Registry Configuration

### Using Pull Secrets

Create a Kubernetes secret for your registry:

```bash
kubectl create secret docker-registry my-registry-secret \
  --docker-server=registry.example.com \
  --docker-username=myuser \
  --docker-password=mypass \
  --docker-email=myemail@example.com
```

Reference in your topology:

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: private-images
spec:
  imagePull:
    pullThroughOverride: always
    pullSecrets:
      - my-registry-secret
  definition:
    containerlab: |
      name: private
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: registry.example.com/nokia/srlinux:latest
```

**How it works:**
1. Controller creates ImageRequest for each image
2. Pull pod is created with the specified pull secrets
3. Image is pulled to node's CRI
4. Launcher can then use the image

### Using Docker Config

For more complex authentication (multiple registries, credential helpers):

```bash
# Create secret from existing docker config
kubectl create secret generic docker-config \
  --from-file=config.json=$HOME/.docker/config.json
```

Reference in topology:

```yaml
spec:
  imagePull:
    dockerConfig: docker-config  # Secret name
```

The secret must contain a `config.json` key with valid Docker config.

### Docker Daemon Configuration

For daemon-level settings (insecure registries, mirrors):

```bash
# Create secret with daemon.json
kubectl create secret generic docker-daemon-config \
  --from-file=daemon.json=/path/to/daemon.json
```

Example daemon.json:
```json
{
  "insecure-registries": ["registry.local:5000"],
  "registry-mirrors": ["https://mirror.example.com"]
}
```

Reference in topology:

```yaml
spec:
  imagePull:
    dockerDaemonConfig: docker-daemon-config
```

## Insecure Registries

For registries without valid TLS:

```yaml
spec:
  imagePull:
    insecureRegistries:
      - registry.local:5000
      - 10.0.0.100:5000
```

**Note:** This is ignored if `dockerDaemonConfig` is set (configure in daemon.json instead).

## HTTP(S) Proxy Support

If your cluster reaches the internet through an HTTP(S) proxy, set the standard proxy env vars
on the launcher pods -- either per topology or globally:

```yaml
spec:
  deployment:
    extraEnv:
      - name: HTTP_PROXY
        value: http://proxy.example.com:8080
      - name: HTTPS_PROXY
        value: http://proxy.example.com:8080
      - name: NO_PROXY
        value: 10.96.0.0/16,10.244.0.0/16,.svc,.svc.cluster.local,localhost,127.0.0.1
```

Or for all topologies via the Config CRD (or the `globalConfig.deployment.extraEnv` helm value):

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Config
metadata:
  name: clabernetes
spec:
  deployment:
    extraEnv:
      - name: HTTPS_PROXY
        value: http://proxy.example.com:8080
      # ...
```

**How it works:**
- The launcher writes the proxy env vars into its Docker daemon config (`proxies` section), so
  direct Docker pulls go through the proxy.
- Pull-through mode also works: the pull pod uses the node CRI (configure your CRI for the proxy
  as usual, most distributions handle this already), and the launcher-side `nerdctl` operations
  inherit the env vars.
- The in-cluster Kubernetes API service address is automatically appended to `NO_PROXY` by the
  launcher, so it never tries to reach the API through the proxy.

**Note:** `NO_PROXY` should contain your cluster's service and pod CIDRs plus cluster-local DNS
suffixes (see the example above -- adjust the CIDRs for your cluster). Proxy env vars are ignored
if `dockerDaemonConfig` is set (configure `proxies` in your daemon.json instead).

## Global Configuration

Set defaults in the Config CRD:

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Config
metadata:
  name: clabernetes
spec:
  imagePull:
    # Default pull-through mode
    pullThroughOverride: auto
    # CRI socket for K3s
    criSockOverride: /run/k3s/containerd/containerd.sock
    # Default docker config for all topologies
    dockerConfig: global-docker-config
    dockerDaemonConfig: global-daemon-config
```

### CRI Socket Override

For non-standard CRI socket locations (e.g., K3s):

```yaml
spec:
  imagePull:
    criSockOverride: /run/k3s/containerd/containerd.sock
```

Common paths:
- Standard containerd: `/run/containerd/containerd.sock`
- K3s: `/run/k3s/containerd/containerd.sock`
- Minikube: `/var/run/containerd/containerd.sock`

## Complete Examples

### Public Registry

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: public-images
spec:
  imagePull:
    pullThroughOverride: auto
  definition:
    containerlab: |
      name: public
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
```

### Private Registry with Pull Secrets

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: enterprise
spec:
  imagePull:
    pullThroughOverride: always
    pullSecrets:
      - enterprise-registry-creds
  definition:
    containerlab: |
      name: enterprise
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: registry.corp.example.com/network/srlinux:23.10.1
```

### Insecure Local Registry

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: local-dev
spec:
  imagePull:
    pullThroughOverride: never
    insecureRegistries:
      - localhost:5000
  definition:
    containerlab: |
      name: local
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: localhost:5000/srlinux:dev
```

### Air-Gapped Environment

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: airgapped
spec:
  imagePull:
    pullThroughOverride: always
    pullSecrets:
      - internal-registry-secret
    dockerConfig: docker-auth-config
  definition:
    containerlab: |
      name: airgapped
      topology:
        nodes:
          srl1:
            kind: nokia_srlinux
            image: internal-registry.corp:5000/nokia/srlinux:latest
```

## Troubleshooting

### Image Pull Failures

Check ImageRequest status:
```bash
kubectl get imagerequests
kubectl describe imagerequest <name>
```

Check pull pod:
```bash
kubectl get pods -l clabernetes/imagePuller=true
kubectl logs <pull-pod-name>
```

### Authentication Issues

Verify secret exists and is correct:
```bash
kubectl get secret my-registry-secret -o yaml
```

Test manually:
```bash
kubectl run test --rm -it --image=<your-image> \
  --overrides='{"spec":{"imagePullSecrets":[{"name":"my-registry-secret"}]}}'
```

### CRI Socket Issues

Verify socket path:
```bash
# On the node
ls -la /run/containerd/containerd.sock
```

Check launcher logs:
```bash
kubectl logs -l clabernetes/topologyNode=<node> -c clabernetes-launcher
```

### Pull-Through Not Working

1. Check ImageRequest is created and accepted
2. Verify pull pod starts and completes
3. Check CRI has the image: `crictl images | grep <image>`
4. Verify Docker can see the image in launcher

## Best Practices

1. **Use pull-through for private registries**: Leverages Kubernetes secrets properly
2. **Pre-pull large images**: Reduces topology startup time
3. **Use registry mirrors**: For faster pulls in large clusters
4. **Set appropriate pull policies**: `IfNotPresent` for stability, `Always` for latest
5. **Secure your secrets**: Use RBAC to protect registry credentials

## Configuration Priority

1. Topology-level `imagePull` settings
2. Global Config CRD `imagePull` settings
3. Default behavior (auto pull-through)

## Related

- [Example: private-registry.yaml](../../examples/advanced/private-registry.yaml)
- [CRD Reference: ImagePull](../crd-reference.md#imagepull)
- [CRD Reference: ImageRequest](../crd-reference.md#imagerequest-crd)
