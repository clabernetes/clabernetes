---
title: Installation
description: Install c9s into an existing Kubernetes cluster or try it in KinD.
icon: Download
---

## Existing cluster

`make install` uses the current Kubernetes context. Set `C9S_CONTEXT` when installing into a
specific context:

```bash
make install
C9S_CONTEXT=my-cluster make install
```

The command uses repository-local pinned `gh`, Helm, kubectl, yq, and UV binaries. It verifies the
context, API access, nodes, required permissions, selected chart, and c9s CRD API group before
running Helm. It does not create KinD, install MetalLB, or apply a demo topology.

## Select a source

```bash
make install                         # latest stable GitHub Release
make install VERSION=0.6.0           # exact published release
make install VERSION=main            # mutable main chart, version 0.0.0
make install VERSION=select          # interactive stable/development picker
make install VERSION=0.0.0-abc1234
```

Inspect installable published releases without a Kubernetes context:

```bash
make ls-releases
make ls-releases ALL=1
```

The default table shows the newest 10 installable artifacts. `ALL=1` shows the complete catalog.
It includes stable GitHub Releases, the mutable `main` chart, and successful manual development
builds such as `0.0.0-<short-sha>`. Probes run concurrently and the table is sorted by publication
or workflow-availability time, newest first. It only includes candidates whose exact OCI chart can
be fetched. The timestamp is labeled **Published/available (UTC)**; it is not a GHCR push timestamp.

## Local checkout images

For a KinD context, local source builds manager and launcher images, loads them into the cluster,
and installs the checkout chart:

```bash
C9S_CONTEXT=kind-my-cluster C9S_KIND_CLUSTER=my-cluster \
  make install VERSION=local
```

The cluster must report one operating-system/architecture pair. Local images receive a unique
checkout/dirty-worktree identity and use `IfNotPresent`.

For a non-KinD cluster, set a registry prefix that every node can pull from:

```bash
C9S_REGISTRY=ghcr.io/example/c9s make install VERSION=local
```

The registry must already be usable for both host pushes and cluster pulls. Static installation
does not start DevSpace or its source-sync workflow.

## Development artifacts

An authorized developer can download the pinned GitHub CLI and dispatch the `cicd` workflow against
a repository branch or tag:

```bash
make c9s-release-tools
build/try-c9s/bin/gh-v2.97.0 workflow run cicd.yaml --ref feature/my-change -f push=true
```

After lint, unit, e2e, image, and chart publication complete, the workflow summary reports the
resolved full SHA and exact commands. The chart and images use `0.0.0-<short-sha>` and no GitHub
Release is created:

```bash
make install VERSION=0.0.0-abc1234
make try-c9s C9S_VERSION=0.0.0-abc1234
```

The `0.0.0` chart is the mutable main channel. It is distinct from latest stable and pins manager
and launcher to the immutable `0.0.0-<short-sha>` images from the same main build.

## API-group compatibility

The installer refuses to cross between `clabernetes.containerlab.dev` and `c9s.run` in place.
Back up resources, then use:

```bash
make uninstall-c9s C9S_CONTEXT=my-cluster
```

CRD deletion removes all c9s custom resources in the target cluster. Reinstall only after
confirming that destructive cleanup is intended.

## Verification and cleanup

Successful output includes the selected context, source/channel, chart version, manager image, and
launcher image. For existing installations:

```bash
make uninstall-c9s C9S_CONTEXT=my-cluster
```

For the disposable quickstart:

```bash
make try-c9s-clean
```
