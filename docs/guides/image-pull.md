---
title: Image pulling
description: Configure direct kubelet pulls, private registry credentials, and registry trust.
---

Device images run directly in c9s-managed Pods. The kubelet and cluster container runtime are the
only components that download and start them. c9s does not import image layers, create image-puller
Pods, mount a CRI socket, or copy images into an inner Docker daemon.

## Pull policy

Set the default Kubernetes pull policy in the singleton Config or in the one LauncherProfile
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

Reference it from a LauncherProfile:

```yaml
apiVersion: c9s.run/v1alpha1
kind: LauncherProfile
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
  launcherProfileRef:
    name: private-images
  kind: nokia_srlinux
  image: registry.example.com/network/srlinux:latest
```

c9s places the Secret name in `Pod.spec.imagePullSecrets`; credential bytes are never stored in a
device plan or immutable ConfigMap. The controller accepts only Kubernetes Docker-config Secret
types when it needs the same credentials for OCI metadata access.

Topology-level `spec.imagePull` is compiled into the shared LauncherProfile generated for that
Topology. Direct Node manifests select a profile explicitly through
`Node.spec.launcherProfileRef`.

## Registry mirrors, private CAs, HTTP, and proxies

Configure registry mirrors, runtime proxy settings, private certificate authorities, and
plain-HTTP endpoints in the container runtime on every Kubernetes node eligible to run a device
Pod. The files and reload procedure depend on the cluster distribution and runtime. Validate that
path by scheduling an ordinary Pod with the same image, placement, pull policy, and pull Secret.

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

## Troubleshooting

Metadata authentication or trust failures are reported on the Node before a device workload is
created. Kubelet pull failures appear on the device Pod as normal events such as `ErrImagePull` or
`ImagePullBackOff`.

Inspect the Node, Pod, events, and resolved profile:

```bash
kubectl -n lab describe nodes.c9s.run router
kubectl -n lab get pods -l c9s.run/direct-workload=router
kubectl -n lab describe pod -l c9s.run/direct-workload=router
kubectl -n lab get launcherprofile private-images -o yaml
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
- [LauncherProfile CRD reference](/docs/crd/launcher-profile)
- [Config CRD reference](/docs/crd/config)
