---
title: Converting containerlab topologies
description: Use clabverter to turn a containerlab topology and the files it references into c9s manifests.
---

clabverter converts a containerlab topology file, together with the local files it references,
into Kubernetes manifests. Files become ConfigMaps, and each node receives them through
`filesFromConfigMap` at the path the definition names, so the topology needs no changes.

## Get clabverter

Download the binary for your platform from the
[release assets](https://github.com/clabernetes/clabernetes/releases), or run the container
image from the directory that holds the topology:

```bash
docker run --rm -v "$(pwd)":/clabernetes/work \
  ghcr.io/clabernetes/clabernetes/clabverter:latest --stdout
```

## Convert

```bash
clabverter --topologyFile lab.clab.yml --destinationNamespace lab --outputDirectory ./manifests
kubectl apply -f ./manifests
```

Without `--topologyFile`, clabverter uses the `*.clab.yml` or `*.clab.yaml` file in the current
directory. The
output includes a Namespace manifest, one ConfigMap per startup configuration, one ConfigMap per
mounted file, and the Topology.

| Flag | Effect |
| --- | --- |
| `--emitCRs` | emit NodeProfile, Node, and Link manifests instead of a Topology, exactly what the in-cluster compiler would produce, for labs too large for one Topology object |
| `--imagePullSecrets regcred` | set `imagePull.pullSecrets` (comma-separated) |
| `--disableExpose` | set `exposeType: None` |
| `--topoSpecFile values.yaml` | merge additional Topology spec fields, such as `deployment` or `expose`, from a file |
| `--stdout` | print the manifests instead of writing files |

## What is converted

- `startup-config` file references are read from disk. Inline configuration (a value with
  newlines) is written to a ConfigMap and mounted at `/clabernetes/startup-config.partial.cfg`,
  with the definition rewritten to that path.
- `license` files and `binds` sources become ConfigMaps mounted at the same path the definition
  names. Bind targets that the Pod owns, such as `/etc/hosts`, are rejected.
- Node names Kubernetes cannot carry are sanitized, see
  [Differences from containerlab](/docs/concepts/containerlab-differences#naming-and-metadata).

Conversion fails with the same diagnostics as the in-cluster compiler when the definition uses
fields c9s cannot realize. Files larger than the ConfigMap limit must be delivered as
[URL payloads](/docs/guides/file-mounting#mounting-files-from-urls) instead.

## Related

- [Topology](/docs/concepts/topology)
- [File mounting](/docs/guides/file-mounting)
