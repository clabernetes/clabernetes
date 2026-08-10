# Local development with DevSpace

This directory holds the DevSpace configuration and helper scripts used by `make dev` from the
repository root. The goal is to run the clabernetes manager from your current checkout inside an
existing Kubernetes cluster: build images, deploy the Helm chart, sync source into a dev pod, and
run the manager with `go run`.

## Quick start

```bash
kubectl config use-context <your-cluster>
make dev
```

From another terminal:

```bash
kubectl -n clabernetes get pods
kubectl -n clabernetes rollout status deployment/clabernetes-manager
```

Stop and remove the deployment:

```bash
build/dev/bin/devspace run purge
# or, when devspace is on PATH:
devspace run purge
```

`purge` deletes the Helm release, DevSpace leases, and all `clabernetes` / `c9s.run` CRDs in the
namespace. Pass the same `NS` you used for `make dev` if you overrode the namespace.

## What `make dev` does

1. **Build** three images (when using the default DevSpace pipeline):
   - `clabernetes-manager` — production manager binary (init container + chart default)
   - `clabernetes-manager-dev` — Go toolchain image for live development (`go run` + file sync)
   - `clabernetes-launcher` — launcher image referenced by global config
2. **Deploy** the local Helm chart (`charts/clabernetes`) into namespace `clabernetes` (or `NS=...`)
3. **Replace** the manager container with the dev image and sync the repository into `/clabernetes`
4. **Start** a shell via `.develop/start.sh` (or auto-run the manager with `--profile auto-run-manager`)

Each run passes `--force-deploy` so Helm values (including launcher image and global config) match
the current checkout. The global `Config` CR is merged with `mergeMode: overwrite` from development
values.

## Image registry modes (`LOCAL_REGISTRY`)

`make dev` chooses how images are built, pushed, and referenced in the cluster.

| `LOCAL_REGISTRY` | When | Build | Cluster pulls from |
| ------------------ | ------ | ------- | ------------------- |
| `auto` (default) | Remote cluster (not kind/minikube/docker-desktop) | Custom buildx script → in-cluster registry | `localhost:<nodePort>/...:dev-latest` |
| `auto` (default) | kind / minikube / docker-desktop | DevSpace buildx → `REGISTRY` | `REGISTRY/...:dev-latest` |
| `1` | Forced | Same as remote `auto` | In-cluster registry |
| `0` | Forced | DevSpace buildx → `REGISTRY` | `REGISTRY/...:dev-latest` |

Detection uses the current `kubectl` context name: contexts matching `kind-*`, `docker-desktop`, or
`minikube` are treated as local.

### External registry (GHCR or other)

Use this when the cluster can pull from a registry you push to (public GHCR packages, private
registry with `imagePullSecrets`, etc.):

```bash
LOCAL_REGISTRY=0 make dev DEVSPACE_ARGS="--profile always-pull"
```

Requirements:

- `docker login` to `REGISTRY` on your workstation
- Push access so DevSpace can publish after build
- Cluster nodes can pull `clabernetes-manager`, `clabernetes-manager-dev`, and `clabernetes-launcher`
  at tag `dev-latest` (and the current git commit hash)
- On remote clusters, use `--profile always-pull` so nodes do not cache stale tags

Custom registry host:

```bash
LOCAL_REGISTRY=0 REGISTRY=registry.example.com/clabernetes \
  make dev DEVSPACE_ARGS="--profile always-pull"
```

Default `REGISTRY` is `ghcr.io/clabernetes/clabernetes` (overridable via DevSpace env / `REGISTRY`).

**Note:** The `clabernetes-manager-dev` package on GHCR is private by default. Anonymous cluster
pulls return `not found`. That is why remote clusters default to the in-cluster registry unless
you opt into `LOCAL_REGISTRY=0` and fix pull access.

### In-cluster registry (default on remote clusters)

On remote clusters, `make dev` enables the DevSpace `local-registry` profile automatically. DevSpace
deploys a registry `Service` (NodePort) in the development namespace. Custom scripts build images on
the host and push them into that registry.

**Why image URLs use `localhost:<port>`**

Image pulls are performed by the **kubelet on the worker node**, not by the pod. Kubelet/containerd
generally cannot use in-cluster DNS names like `registry.clabernetes.svc.cluster.local`.

DevSpace exposes the registry as a **NodePort**. The convention (and what these scripts use) is:

| Actor | How it reaches the registry |
| ------- | ---------------------------- |
| Worker node (kubelet pull) | `localhost:<nodePort>` — NodePort forwarding on the node |
| Developer machine (`docker push`) | `kubectl port-forward svc/registry <nodePort>:5000` → `127.0.0.1:<nodePort>` |

So a pod spec like `localhost:31548/clabernetes/clabernetes/clabernetes-manager-dev:dev-latest`
means: on whichever node schedules the pod, pull from the registry via that node's NodePort.

Force in-cluster registry even on kind:

```bash
LOCAL_REGISTRY=1 make dev
```

#### Docker `insecure-registries`

The in-cluster registry serves plain HTTP. If `docker push` fails with an insecure-registry error,
add the NodePort to `/etc/docker/daemon.json` and restart Docker:

```json
{
  "insecure-registries": ["localhost:31548"]
}
```

Look up the port:

```bash
kubectl -n clabernetes get svc registry
```

## Image tags and references

Development builds tag images with:

- `dev-latest` — moving tag updated on every `make dev` build
- `<commit-hash>` — `git describe --always --abbrev=8` (e.g. `5b2dbf5a`)

Helm and the dev pod use explicit tagged references (`:dev-latest`), not bare image names. Bare
names resolve to `:latest` on the cluster, which may be missing or an old release.

| Image | Purpose |
| ------- | --------- |
| `.../clabernetes-manager:dev-latest` | Init container (CRD bootstrap, global config) and manager when not in dev replace mode |
| `.../clabernetes-manager-dev:dev-latest` | Dev container: Go toolchain, synced source, `go run` |
| `.../clabernetes-launcher:dev-latest` | Launcher pods (via global `Config` CR) |

With the in-cluster registry, the host prefix becomes `localhost:<nodePort>/` instead of
`ghcr.io/clabernetes/clabernetes/`.

## Helper scripts

| Script | Role |
| -------- | ------ |
| [`build-for-local-registry.sh`](build-for-local-registry.sh) | `docker buildx build --load`, then `docker push` to the in-cluster registry via port-forward. Used only when `LOCAL_REGISTRY=1` (or `auto` on a remote cluster). Also tags and pushes `dev-latest` for manager, launcher, and dev images. |
| [`ensure-registry-port-forward.sh`](ensure-registry-port-forward.sh) | Keeps `kubectl port-forward svc/registry <nodePort>:5000` running on the host so `docker push` can reach the registry. Uses a flock lock so parallel image builds share one forward. |
| [`local-registry-image-ref.sh`](local-registry-image-ref.sh) | Prints `localhost:<nodePort>/<path>:<tag>` for Helm and dev pod image fields. Called from DevSpace vars when the `local-registry` profile is active. |
| [`target-platform.sh`](target-platform.sh) | Detects uniform `GOOS/GOARCH` from cluster nodes for `docker buildx --platform`. Fails on mixed-platform clusters unless you set `TARGET_PLATFORM`. |
| [`start.sh`](start.sh) | Dev container entry: prints banner, or with `--run` re-runs the initializer from synced source then `go run cmd/clabernetes/main.go run`. |

Port-forward state files (gitignored): `.registry-port-forward.pid`, `.registry-port-forward.lock`.

## DevSpace configuration

Main config: [`devspace.yaml`](devspace.yaml).

### Profiles used by `make dev`

| Profile | Effect |
| --------- | -------- |
| `auto-run-manager` | Runs `.develop/start.sh --run` instead of an interactive shell |
| `local-registry` | Enables in-cluster registry, custom builds, and `localhost:<port>` image refs |
| `debug` | Manager, controller, and launcher log level `debug` (enabled from root `devspace.yaml`) |
| `single-manager` | `replicaCount: 1` |
| `always-pull` | `imagePullPolicy: Always` — use with `LOCAL_REGISTRY=0` on remote clusters |

Enable extra profiles via `DEVSPACE_ARGS`:

```bash
make dev DEVSPACE_ARGS="--profile always-pull"
```

### Pipelines

- `dev` — build dev + manager + launcher, deploy, `start_dev`
- `deploy` — build manager + launcher, deploy (no dev pod)
- `purge` — stop dev, remove deployment, delete clabernetes leases and CRDs

## Environment variables

| Variable | Default | Description |
| ---------- | --------- | ------------- |
| `LOCAL_REGISTRY` | `auto` | `auto`, `0` (external registry), or `1` (in-cluster registry) |
| `REGISTRY` | `ghcr.io/clabernetes/clabernetes` | Image registry prefix for external-registry builds |
| `NS` | `clabernetes` | Kubernetes namespace |
| `TARGET_PLATFORM` | auto-detected | e.g. `linux/amd64` for `docker buildx --platform` |
| `DEVSPACE_ARGS` | (empty) | Extra flags passed to `devspace run dev` |
| `LOCAL_REGISTRY_BUILD` | set by Makefile | `1` when using in-cluster registry scripts |

## Troubleshooting

### `ImagePullBackOff` on `clabernetes-manager-dev`

Common causes:

1. **Stale deployment** — image refs from an older DevSpace run (untagged `ghcr.io/...`). Fix: `devspace run purge`, then `make dev` again.
2. **Private GHCR dev image** — cluster cannot pull `*-manager-dev` without credentials. Fix: use default `LOCAL_REGISTRY=auto` on remote clusters, or `LOCAL_REGISTRY=0` only after fixing pull access.
3. **Wrong tag** — cluster pulls `:latest` but only `:dev-latest` exists. Fix: purge and redeploy with current `.develop` config.

Verify pod image refs:

```bash
kubectl -n clabernetes get pod -l clabernetes/component=manager \
  -o jsonpath='init={.spec.initContainers[0].image}{"\n"}manager={.spec.containers[0].image}{"\n"}'
```

Expected with in-cluster registry:

```
init=localhost:31548/clabernetes/clabernetes/clabernetes-manager:dev-latest
manager=localhost:31548/clabernetes/clabernetes/clabernetes-manager-dev:dev-latest
```

Expected with external registry:

```
init=ghcr.io/clabernetes/clabernetes/clabernetes-manager:dev-latest
manager=ghcr.io/clabernetes/clabernetes/clabernetes-manager-dev:dev-latest
```

### Init container warns about `configs.clabernetes.containerlab.dev` forbidden

The init container image is too old (pre-`c9s.run` API group) or CRDs are mixed. Fix:

```bash
make uninstall-c9s   # removes Helm release, legacy + c9s CRDs, namespace
make dev
```

Or delete legacy CRDs manually: `kubectl get crd | grep clabernetes.containerlab.dev`

### `connection refused` on `127.0.0.1:<port>` during build

The registry port-forward is not running. Re-run `make dev`; `ensure-registry-port-forward.sh` starts
it before push. Check nothing else is bound to the NodePort.

### Mixed-platform cluster

`target-platform.sh` fails if nodes report different OS/arch. Set explicitly:

```bash
TARGET_PLATFORM=linux/amd64 make dev
```

## File layout

```
.develop/
  devspace.yaml                  # DevSpace project config
  dev.Dockerfile                 # Dev image (Go toolchain)
  start.sh                       # Dev container entrypoint
  build-for-local-registry.sh    # In-cluster registry build + push
  ensure-registry-port-forward.sh
  local-registry-image-ref.sh
  target-platform.sh
```

Root [`devspace.yaml`](../devspace.yaml) only forwards to `.develop/devspace.yaml` via profiles
(`debug`, `single-manager`).
