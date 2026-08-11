---
title: Quickstart
description: Launch c9s and a sample network lab in a disposable KinD cluster.
icon: Rocket
---

The repository includes a local workflow that creates a single-node KinD cluster, installs the
selected Clabernetes Helm chart, configures MetalLB, and applies a source-compatible sample SR Linux
plus network multitool lab.

The default uses the latest stable published release. Set `C9S_VERSION=local` to build the current
checkout and load its manager and launcher images into KinD.

## Requirements

- Docker
- GNU Make
- Enough local CPU and memory to run Kubernetes and the sample network node

KinD, `kubectl`, Helm, yq, UV, and GitHub CLI are downloaded into `build/try-c9s/bin` at the
repository-pinned versions. Host executables with the same names are not used by this workflow.

## Start the lab

From the repository root, run:

```bash
make try-c9s
```

Other supported selections:

```bash
make ls-releases                         # available artifacts, newest first
make try-c9s C9S_VERSION=0.6.0           # exact published release
make try-c9s C9S_VERSION=local            # build and load the current checkout
make try-c9s C9S_VERSION=0.0.0-abc1234   # exact unpublished development chart
```

Published demos are retrieved from the selected release tag. Development builds use the source
revision recorded in their chart metadata. The latest stable release is resolved through GitHub
Releases and installed with an exact OCI chart version; it is not Helm's floating chart selection.

The command prints the assigned SR Linux management endpoints:

```text
SR Linux SSH:      ssh admin@<load-balancer-ip>
SR Linux gNMI:     <load-balancer-ip>:57400
SR Linux NETCONF:  <load-balancer-ip>:830
```

The exact load balancer address depends on the local cluster configuration.

## Inspect the resources

```bash
kubectl get pods --all-namespaces
kubectl get nodes.c9s.run --all-namespaces
kubectl get links.c9s.run --all-namespaces
kubectl get services --all-namespaces
```

## Clean up

Delete the sample resources and disposable cluster with:

```bash
make try-c9s-clean
```

If installation or topology readiness fails, keep the output: the command reports the selected
source, chart, manager and launcher images, topology status, pods, events, and manager logs. A
failed readiness check returns a non-zero exit status.

## Next steps

- [Understand Nodes and Links](/docs/concepts/nodes-and-links)
- [Read the architecture](/docs/architecture)
- [Browse complete examples](/docs/examples)
