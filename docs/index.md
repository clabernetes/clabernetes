---
title: Overview
description: Run containerlab network topologies across a Kubernetes cluster.
---

Clabernetes, also known as **c9s**, is a set of Kubernetes custom resources and controllers for
running containerlab network nodes across a cluster.

Instead of placing every node on one machine, c9s creates a launcher workload for each network
node, connects interfaces across pods, and exposes management services through Kubernetes.

## Choose an authoring model

### Nodes and Links

`Node` and `Link` are the primary API. Each resource stays bounded to one network node or one wire,
which makes direct resources the preferred model for generated and larger labs.

[Learn about Nodes and Links](/docs/concepts/nodes-and-links)

### Topology

`Topology` is a supported higher-level resource that accepts a containerlab definition and
compiles it into `LauncherProfile`, `Node`, and `Link` resources.

[Learn about Topology](/docs/concepts/topology)

## Get started

Use the disposable local workflow to create a KinD cluster, install c9s, and launch a sample lab:

```bash
make try-c9s
```

[Follow the local quickstart](/docs/quickstart)

## Continue exploring

- [Architecture](/docs/architecture) explains the controllers, launchers, and connectivity model.
- [Guides](/docs/guides/expose-configuration) cover common deployment and operations tasks.
- [Examples](/docs/examples) point to complete manifests in this repository.
- [CRD reference](/docs/crd-reference) documents the available resource fields.
