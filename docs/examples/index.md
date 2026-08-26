---
title: Examples
description: Complete Clabernetes manifests for common lab and deployment patterns.
---

The repository contains ready-to-apply manifests grouped by purpose. These examples remain next to
the code so they can be tested and updated with the APIs they demonstrate.

## Basic labs

- [Basic Topology examples](https://github.com/clabernetes/clabernetes/tree/main/examples/basic)
  demonstrate small containerlab labs.
- [Direct Node and Link example](https://github.com/clabernetes/clabernetes/tree/main/examples/basic/individual-resources/srl-multitool)
  builds an SR Linux and network multitool lab without a Topology object.

## Deployment configuration

[Deployment examples](https://github.com/clabernetes/clabernetes/tree/main/examples/deployment)
cover resource requests, scheduling, persistence, and file mounting.

## Service exposure

[Exposure examples](https://github.com/clabernetes/clabernetes/tree/main/examples/expose) show
ClusterIP, Headless, disabled auto-expose, and fully disabled exposure modes.

## Advanced labs

[Advanced examples](https://github.com/clabernetes/clabernetes/tree/main/examples/advanced) include
larger topologies, private registries, probes, grouped nodes, and Nokia SR-SIM.

## Apply an example

Most examples can be applied directly with `kubectl` after c9s is installed:

```bash
kubectl apply -f examples/basic/<example>.yaml
```

Read the README in each example directory for prerequisites and cleanup instructions.
