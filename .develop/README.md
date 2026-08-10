# Local development with DevSpace

This directory holds the DevSpace configuration used by `make dev` from the repository root.

## Quick start

```bash
kubectl config use-context <your-cluster>
make dev
```

On remote clusters (not kind/minikube/docker-desktop), `make dev` enables DevSpace's native
`localRegistry` profile automatically.

```bash
make purge-dev   # tear down when done
```

Full options: see [Development](../README.md#development) in the root README.

## How `make dev` works

1. Detect the cluster platform (`linux/amd64` or `linux/arm64`) via [`target-platform.sh`](target-platform.sh)
2. Build `clabernetes-manager`, `clabernetes-manager-dev`, and `clabernetes-launcher` for that platform
3. Deploy the Helm chart into namespace `clabernetes` (or `NS=...`)
4. Sync source into the manager dev pod and run via `.develop/start.sh`

Each run uses `--force-deploy` and overwrites the global `Config` CR from development values.

## Registry modes (`LOCAL_REGISTRY`)

| Value | Remote cluster | kind / minikube |
| ------- | ---------------- | ----------------- |
| unset (`auto`) | native `local-registry` profile | push to `REGISTRY` |
| `1` | native `local-registry` profile | native `local-registry` profile |
| `0` | push to `REGISTRY` (GHCR) | push to `REGISTRY` |

```bash
# external registry (cluster must be able to pull)
LOCAL_REGISTRY=0 make dev DEVSPACE_ARGS="--profile always-pull"

# force in-cluster registry on kind
LOCAL_REGISTRY=1 make dev
```

Default `REGISTRY`: `ghcr.io/clabernetes/clabernetes`.

## In-cluster registry (default on remote clusters)

The `local-registry` DevSpace profile enables:

```yaml
localRegistry:
  enabled: true
  localbuild: true
```

DevSpace then:

1. Deploys `registry:2.8.1` in the dev namespace (NodePort)
2. Port-forwards `localhost:<nodePort>` → registry
3. Rewrites all image URLs to `localhost:<nodePort>/...` (Helm, dev pod, etc.)
4. Builds on your machine and pushes into that registry

### Why `localhost:<port>` in image URLs?

Image pulls are done by the **kubelet on the worker node**, not the pod. DevSpace uses NodePort
so nodes pull via `localhost:<nodePort>` on whichever node schedules the pod. Your laptop pushes
through the same port via DevSpace's port-forward.

### Platform-aware builds

[`target-platform.sh`](target-platform.sh) inspects node `operatingSystem` and `architecture` in
the current kubectl context and sets `TARGET_PLATFORM` (for example `linux/arm64`). DevSpace
passes that to builds as:

- `docker buildx --platform=${TARGET_PLATFORM}` when pushing to an external registry
- `BUILDPLATFORM=${TARGET_PLATFORM}` build-arg when using the local-registry engine (`docker build`)

Dockerfiles default `BUILDPLATFORM` for the local-registry path:

```dockerfile
ARG BUILDPLATFORM=linux/amd64
FROM --platform=${BUILDPLATFORM} golang:1.25-bookworm AS builder
```

Override detection explicitly on mixed-platform clusters:

```bash
TARGET_PLATFORM=linux/amd64 make dev
```

## Image tags

Development builds tag images with `dev-latest` and the current git commit hash. Helm and the dev
pod use explicit `:dev-latest` refs via `MANAGER_*_TAGGED` variables in `devspace.yaml`.

## DevSpace profiles

| Profile | Purpose |
| --------- | --------- |
| `local-registry` | Native in-cluster registry + local build (default on remote via Makefile) |
| `auto-run-manager` | Run manager automatically instead of interactive shell |
| `always-pull` | `imagePullPolicy: Always` — use with `LOCAL_REGISTRY=0` on remote clusters |
| `debug` | Debug log levels |
| `single-manager` | `replicaCount: 1` |

## Helper scripts

| Script | Role |
|--------|------|
| [`target-platform.sh`](target-platform.sh) | Detect cluster node OS/arch for `TARGET_PLATFORM` |
| [`start.sh`](start.sh) | Dev container entrypoint |

## Troubleshooting

### `ImagePullBackOff` on `*-manager-dev`

- Stale deployment: `make purge-dev`, then `make dev`
- Private GHCR: use default local registry (`LOCAL_REGISTRY` unset on remote)
- Wrong tag: pod should show `:dev-latest`, not bare `ghcr.io/...` (resolves to `:latest`)

### `failed to parse platform ""`

Usually means `localbuild` + Dockerfiles without a `BUILDPLATFORM` default. Ensure you are on a
current checkout with `ARG BUILDPLATFORM=...` in the Dockerfiles.

### Leftover registry after experiments

```bash
kubectl -n clabernetes delete deploy,svc registry --ignore-not-found
# or
make purge-dev
```

### Mixed-platform cluster

```bash
TARGET_PLATFORM=linux/amd64 make dev
```
