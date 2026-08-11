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

The target requires Docker and creates a single-node KinD cluster by default. It installs MetalLB
and prints access endpoints:

```text
SR Linux SSH:      ssh admin@<load-balancer-ip>
SR Linux gNMI:     <load-balancer-ip>:57400
SR Linux NETCONF:  <load-balancer-ip>:830
```

If KinD, kubectl, or Helm are not installed, it downloads local copies under
`build/try-c9s/bin`.

Select a published or local source explicitly:

```bash
make ls-releases
make try-c9s C9S_VERSION=0.6.0
make try-c9s C9S_VERSION=local
```

To install into an existing cluster, see [`docs/installation.md`](docs/installation.md) or run:

```bash
C9S_CONTEXT=<your-context> make install
```

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
cluster, builds the manager/launcher images, loads them into the cluster,
installs the local Helm chart, and runs the `e2e/...` Go tests. Re-runs are
cheap: tools are cached and the cluster is reused.

To iterate on just the tests against the already-running cluster, use:

```bash
make e2e-test
```

`make e2e-test` runs the full setup automatically if the cluster is missing, and
otherwise reuses the existing cluster.

CI runs the exact same `e2e-*` make targets (see
[.github/workflows/e2e.yaml](.github/workflows/e2e.yaml)), so local and CI
share all of the setup code.

Tear down the e2e cluster with:

```bash
make e2e-clean
```

## Documentation development

The Fumadocs site under `docs-site/` renders the repository-owned content in `docs/`. Start the Vite
development server from the repository root:

```bash
make serve-docs
```

The target installs the locked documentation dependencies automatically and binds the server to
`0.0.0.0` so it is reachable from the host. Override the bind address with `DOCS_HOST` when needed.

Edits under `docs/` are reflected by the development server. Additional documentation workflows
are available from the repository root:

```bash
make check-docs    # type-check the app and validate documentation links
make build-docs    # create the static site under docs-site/build/client
make preview-docs  # build and serve the static output locally
```

## Development

Run the manager from your current checkout in an existing Kubernetes cluster:

```bash
kubectl config use-context <your-cluster>
make dev
```

DevSpace builds images, deploys the Helm chart into the `c9s-dev` namespace, syncs source into
the manager pod, and starts the manager with debug logging. Each run forces a chart redeploy and
overwrites the global `Config` CR from development values.

If `devspace` is not on `PATH`, the pinned version is downloaded to `build/dev/bin`.

**Full documentation** (registry modes, `localhost:<port>` image URLs, helper scripts,
troubleshooting): [`.develop/README.md`](.develop/README.md).

### Registry mode summary

| Command | Use when |
| ------- | -------- |
| `make dev` | Default. Remote clusters → project-managed local registry. kind/minikube → push to `REGISTRY`. |
| `LOCAL_REGISTRY=0 make dev` | Push dev images to `DEV_REGISTRY` (default GHCR); cluster pulls from there. Requires `docker login`. |

Default `DEV_REGISTRY` is `ghcr.io/clabernetes/clabernetes`.

### Common options

```bash
# different namespace (defaults to c9s-dev)
DEV_NS=my-c9s-dev make dev

# push dev images to GHCR (requires docker login ghcr.io)
LOCAL_REGISTRY=0 make dev

# mixed-platform cluster
TARGET_PLATFORM=linux/amd64 make dev

# extra DevSpace profiles
make dev DEVSPACE_ARGS="--profile always-pull"
```

Platform detection: [`.develop/target-platform.sh`](.develop/target-platform.sh) (see
[`.develop/README.md`](.develop/README.md)).

### Verify and clean up

```bash
kubectl -n c9s-dev get pods
kubectl -n c9s-dev rollout status deployment/clabernetes-manager
```

```bash
make purge-dev   # removes deployment, clabernetes CRDs, and the dev namespace
```
