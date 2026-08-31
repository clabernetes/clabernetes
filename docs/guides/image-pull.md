---
title: Image pulling
description: Configure direct kubelet pulls, private registry credentials, registry trust, and controller metadata mirrors.
---

Device images run directly in c9s-managed Pods. The kubelet and cluster container runtime are the
only components that download and start them. c9s does not import image layers, create image-puller
Pods, mount a CRI socket, or copy images into an inner Docker daemon.

## Pull policy

Set the default Kubernetes pull policy in the singleton Config or in the one NodeProfile
selected by a Node:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Config
metadata:
  name: clabernetes
spec:
  imagePull:
    policy: IfNotPresent
```

The supported values are the Kubernetes policies `Always`, `IfNotPresent`, and `Never`. An
explicit image-pull policy produced by the imported containerlab node definition takes precedence
over this profile default.

## Private registry credentials

Create a Docker registry Secret in the same namespace as the Node:

```bash
kubectl -n lab create secret docker-registry device-registry \
  --docker-server=registry.example.com \
  --docker-username=myuser \
  --docker-password=mypass
```

Reference it from a NodeProfile:

```yaml
apiVersion: c9s.run/v1alpha1
kind: NodeProfile
metadata:
  name: private-images
  namespace: lab
spec:
  imagePull:
    pullSecrets:
      - device-registry
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: router
  namespace: lab
spec:
  profileRef:
    name: private-images
  kind: nokia_srlinux
  image: registry.example.com/network/srlinux:latest
```

c9s places the Secret name in `Pod.spec.imagePullSecrets`; credential bytes are never stored in a
device plan or immutable ConfigMap. The controller accepts only Kubernetes Docker-config Secret
types when it needs the same credentials for OCI metadata access.

Topology-level `spec.imagePull` is compiled into the shared NodeProfile generated for that
Topology. Direct Node manifests select a profile explicitly through
`Node.spec.profileRef`.

## Registry mirrors, private CAs, HTTP, and proxies

Configure registry mirrors, runtime proxy settings, private certificate authorities, and
plain-HTTP endpoints in the container runtime on every Kubernetes node eligible to run a device
Pod. The files and reload procedure depend on the cluster distribution and runtime. Validate that
path by scheduling an ordinary Pod with the same image, placement, pull policy, and pull Secret.
Node-runtime mirrors do not configure controller metadata access; when the controller must reach
registries through the same mirrors, declare them as
[controller metadata mirrors](#controller-metadata-mirrors) as well.

Before creating the device Pod, the c9s controller fetches only the OCI manifest and configuration
blob required by package-driven planning. Publicly trusted HTTPS needs no extra setting. If this
metadata request needs a private CA or an explicitly permitted HTTP endpoint, configure an exact
registry authority in the Config:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Config
metadata:
  name: clabernetes
spec:
  imagePull:
    registryMetadataTrust:
      - registry: registry.example.com
        caBundle: |-
          -----BEGIN CERTIFICATE-----
          ...
          -----END CERTIFICATE-----
      - registry: registry.lab.example:5000
        plainHTTP: true
```

This trust policy affects only controller metadata access. It does not configure kubelets,
mirrors, proxies, or node trust. Entries match exact `host[:port]` authorities. Wildcards, schemes,
repository paths, duplicate authorities, empty exceptions, and a CA combined with `plainHTTP` are
rejected. Configure the equivalent trust and endpoint in the node runtime as well. Prefer TLS with
a private CA; plain HTTP has no transport encryption.

## Controller metadata mirrors

Some clusters pull every image through a CRI-level pull-through registry, for example Harbor proxy
projects configured through containerd `hosts.toml` or Talos `machine.registries.mirrors`. The
kubelet then pulls public references such as `ghcr.io/...` without reaching the public registry,
but the controller's metadata request would still dial the original registry host. Declare the
same mirrors for controller metadata access in the Config:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Config
metadata:
  name: clabernetes
spec:
  imagePull:
    registryMetadataMirrors:
      - registry: docker.io
        endpoint: https://harbor.example.com/v2/docker
        overridePath: true
      - registry: ghcr.io
        endpoint: https://harbor.example.com/v2/ghcr
        overridePath: true
    registryMetadataTrust:
      - registry: harbor.example.com
        caBundle: |-
          -----BEGIN CERTIFICATE-----
          ...
          -----END CERTIFICATE-----
```

Each entry redirects the controller's manifest and config requests for one exact source registry
to the mirror endpoint. Docker Hub aliases (`docker.io`, `index.docker.io`,
`registry-1.docker.io`) share one entry. Only the HTTP connection is rewritten: image references,
resolved digest identities, and Pod image strings keep the original registry, so kubelets keep
using their own runtime mirror configuration. There is no origin fallback; a failing mirror fails
planning with a Node condition instead of silently retrying the public registry.

`overridePath` treats the endpoint path as the mirror's registry API root, replacing the standard
`/v2` prefix (containerd `override_path` semantics). A Harbor proxy project for `ghcr.io` at
`https://harbor.example.com/v2/ghcr` serves `ghcr.io/srl-labs/network-multitool` at
`/v2/ghcr/srl-labs/network-multitool/...`. An endpoint with a path requires `overridePath`; a
hostname-only endpoint such as `https://mirror.example.com` forwards paths unchanged.

Transport trust follows the connection: a `registryMetadataTrust` entry for the mirror endpoint
host applies to every source registry fetched through that mirror. The endpoint scheme selects
TLS or plain HTTP, and the mirror answers its own authentication challenges. Pull Secret
credentials still match the source registry in the image reference.

One sharp edge: the metadata client refuses authentication-token realms that are loopback or
RFC 1918 IP literals (a server-side request forgery guard). Harbor advertises its configured
external URL as the token realm, so a Harbor mirror reachable only as a private IP literal must
be addressed by a DNS hostname instead.

## Troubleshooting

Metadata authentication or trust failures are reported on the Node before a device workload is
created. A referenced pull Secret that does not exist or cannot be read is reported the same way:
`PlanApplied` goes `False` with reason `ImagePullSecretMissing` or `ImagePullSecretUnreadable`
plus a Warning event, and planning resumes once the Secret is available. Kubelet pull failures
appear on the device Pod as normal events such as `ErrImagePull` or `ImagePullBackOff`.

Inspect the Node, Pod, events, and resolved profile:

```bash
kubectl -n lab describe nodes.c9s.run router
kubectl -n lab get pods -l c9s.run/direct-workload=router
kubectl -n lab describe pod -l c9s.run/direct-workload=router
kubectl -n lab get nodeprofile private-images -o yaml
```

Test the kubelet path independently:

```bash
kubectl -n lab run image-pull-test --restart=Never --image=registry.example.com/network/srlinux:latest \
  --overrides='{"spec":{"imagePullSecrets":[{"name":"device-registry"}]}}'
kubectl -n lab describe pod image-pull-test
```

If the test Pod fails, fix the Secret or cluster-runtime registry configuration. c9s deliberately
has no CRI-socket, insecure-registry, pull-through, Docker-config mount, or image-import fallback.

## Related

- [Private registry example](https://github.com/clabernetes/clabernetes/blob/main/examples/advanced/private-registry.yaml)
- [Topology CRD reference](/docs/crd/topology)
- [NodeProfile CRD reference](/docs/crd/node-profile)
- [Config CRD reference](/docs/crd/config)
