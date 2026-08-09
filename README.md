[![Discord](https://img.shields.io/discord/860500297297821756?style=flat-square&label=discord&logo=discord&color=00c9ff&labelColor=bec8d2)](https://discord.gg/vAyddtaEV9)
[![Go Report](https://img.shields.io/badge/go%20report-A%2B-blue?style=flat-square&color=00c9ff&labelColor=bec8d2)](https://goreportcard.com/report/github.com/clabernetes/clabernetes)

# clabernetes a.k.a c9s

<p>
  <img src="https://gitlab.com/rdodin/pics/-/wikis/uploads/b5d611838fcb9c588b6311bccf11b954/c9s_logo1-upscale2x-white-tag+font-min__1_.png" width="200" align="left" alt="clabernetes"/>
  Love containerlab? Want containerlab, just distributed in a kubernetes cluster? Enter
  clabernetes -- containerlab + kubernetes. clabernetes is a kubernetes controller that deploys valid
  containerlab topologies into a kubernetes cluster.

  See [clabernetes docs](https://containerlab.dev/manual/clabernetes) for reference.
</p>

<br clear="left"/>

## Try c9s

You can launch a disposable KinD cluster, install the published clabernetes Helm chart, and apply a
sample SR Linux plus multitool topology with:

```bash
make try-c9s
```

The target requires Docker and creates a single-node KinD cluster by default. It writes a KinD
config with a fixed UI host port mapping, installs MetalLB, and prints access endpoints:

```text
UI:                http://localhost:3000
SR Linux SSH:      ssh admin@<load-balancer-ip>
SR Linux gNMI:     <load-balancer-ip>:57400
SR Linux NETCONF:  <load-balancer-ip>:830
```

If KinD, kubectl, or Helm are not installed, it downloads local copies under
`build/try-c9s/bin`.

SR Linux management access uses the clabernetes LoadBalancer service
directly.

Clean up the sample resources and the KinD cluster with:

```bash
make try-c9s-clean
```

## Local e2e

You can run the full e2e suite locally against a disposable KinD cluster using
**locally built** images (no published images, no devspace) with:

```bash
make test-e2e-local
```

This downloads pinned tools into `build/e2e/bin`, creates a single-node KinD
cluster, builds the manager/launcher/ui images, loads them into the cluster,
installs the local Helm chart, and runs the `e2e/...` Go tests. Re-runs are
cheap: tools are cached and the cluster is reused.

To iterate on just the tests against the already-running cluster, use:

```bash
make e2e-test
```

`make e2e-test` runs the full setup automatically if the cluster is missing, and
otherwise reuses the existing cluster.

CI runs the exact same `e2e-*` make targets (see
[.github/workflows/test.yaml](.github/workflows/test.yaml)), so local and CI
share all of the setup code.

Tear down the e2e cluster with:

```bash
make e2e-clean
```

## Development

To run the manager from the current checkout in an existing Kubernetes cluster, first select the
desired `kubectl` context, then run:

```bash
make dev
```

The target uses the existing DevSpace configuration to build the manager, launcher, and UI images,
install the local Helm chart in the `clabernetes` namespace, synchronize the source tree into the
manager pod, and start the manager with debug logging. If `devspace` is not already on `PATH`, the
pinned version is downloaded to `build/dev/bin`. Each run forces the local chart to be redeployed
and replaces the global `Config` CR from the development values. This keeps the manager and launcher
images from different checkouts from being mixed, but it also means `make dev` overwrites an existing
global clabernetes configuration in the selected development namespace.

DevSpace invokes [`.develop/target-platform.sh`](.develop/target-platform.sh) through the
`DETECTED_TARGET_PLATFORM` variable in `.develop/devspace.yaml`. The script reads the operating
system and architecture reported by every node in the current `kubectl` context. Its result becomes
`TARGET_PLATFORM`, which every image passes to BuildKit as `--platform=<os>/<architecture>`.

Run the detector directly with `bash .develop/target-platform.sh`. If the cluster contains more
than one platform, select the platform on which clabernetes will run:

```bash
TARGET_PLATFORM=linux/amd64 make dev
```

Override the development namespace with `NS`:

```bash
NS=my-clabernetes make dev
```

For a non-local cluster, set a registry that is writable from the development machine and readable
by the cluster, and enable fresh image pulls:

```bash
NS=my-clabernetes REGISTRY=registry.example.com/clabernetes \
  make dev DEVSPACE_ARGS="--profile always-pull"
```

Verify the installation from another terminal:

```bash
kubectl -n clabernetes get pods
kubectl -n clabernetes rollout status deployment/clabernetes-manager
```

To remove the DevSpace deployment, run `devspace run purge` (or
`build/dev/bin/devspace run purge` when using the downloaded binary). This also deletes all
clabernetes CRDs and their custom resources from the cluster. When overriding the namespace, pass
the same value to the purge command, for example `NS=my-clabernetes devspace run purge`.
