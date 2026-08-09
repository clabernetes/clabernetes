---
title: Local quickstart
description: Launch c9s and a sample network lab in a disposable KinD cluster.
---

The repository includes a local workflow that creates a single-node KinD cluster, installs the
published Clabernetes Helm chart, configures MetalLB, and applies a sample SR Linux plus network
multitool lab.

The workflow uses published c9s images, so it does not require a local image build.

## Requirements

- Docker
- GNU Make
- Enough local CPU and memory to run Kubernetes and the sample network node

KinD, `kubectl`, and Helm are downloaded into `build/try-c9s/bin` when they are not already
available.

## Start the lab

From the repository root, run:

```bash
make try-c9s
```

The command prints the operator UI address and the assigned SR Linux management endpoints:

```text
UI:                http://localhost:3000
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

## Next steps

- [Understand Nodes and Links](/docs/concepts/nodes-and-links)
- [Read the architecture](/docs/architecture)
- [Browse complete examples](/docs/examples)
