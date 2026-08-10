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
make purge-dev   # tear down when done (including the dev namespace)
```

Full options: see [Development](../README.md#development) in the root README.

## How `make dev` works

1. Detect the cluster platform (`linux/amd64` or `linux/arm64`) via [`target-platform.sh`](target-platform.sh)
2. Build `clabernetes-manager`, `clabernetes-manager-dev`, and `clabernetes-launcher` for that platform
3. Deploy the Helm chart into namespace `c9s-dev` (or `DEV_NS=...`)
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
LOCAL_REGISTRY=0 make dev

# force in-cluster registry on kind
LOCAL_REGISTRY=1 make dev
```

Default `DEV_REGISTRY`: `ghcr.io/clabernetes/clabernetes` (override with `DEV_REGISTRY=... make dev`).

### External registry (`LOCAL_REGISTRY=0`)

Use this when you want DevSpace to `docker buildx build --push` all dev images to GHCR (or another
registry) and have the cluster pull them directly.

Prerequisites:

1. **Docker login on the build host** — `make dev` runs `ensure-registry-auth.sh` and fails fast
   if the registry host from `REGISTRY` is missing from `~/.docker/config.json`.
2. **Push permission** to the target namespace (for example `write:packages` on a GitHub PAT when
   using GHCR).
3. **Cluster pull access** — GHCR packages must be public, or the cluster needs an `imagePullSecret`
   that can read from `REGISTRY`.

```bash
echo "$GITHUB_PAT" | docker login ghcr.io -u YOUR_GITHUB_USER --password-stdin
LOCAL_REGISTRY=0 make dev
```

DevSpace builds and pushes three images (tags `dev-latest` and the current git commit hash):

- `ghcr.io/clabernetes/clabernetes/clabernetes-manager-dev`
- `ghcr.io/clabernetes/clabernetes/clabernetes-manager`
- `ghcr.io/clabernetes/clabernetes/clabernetes-launcher`

The `always-pull` profile is enabled automatically via `external-registry` for this path so nodes pick up freshly pushed
tags. Override the registry with `DEV_REGISTRY=ghcr.io/my-org/clabernetes make dev` if needed.

Verify a push before debugging `ImagePullBackOff`:

```bash
docker buildx imagetools inspect ghcr.io/clabernetes/clabernetes/clabernetes-manager-dev:dev-latest
```

## In-cluster registry (default on remote clusters)

The `local-registry` profile uses a registry managed by this project. DevSpace's `localRegistry`
feature remains disabled because custom BuildKit builds run before that feature bootstraps its
registry. `make dev` then:

1. Ensures a `registry:2.8.1` Deployment and NodePort Service exist in the dev namespace
2. Builds with `docker buildx build --load` on the host (optional `--secret` for Zscaler)
3. Port-forwards and `docker push`es into that registry
4. Passes explicit `localhost:<nodePort>/...:dev-latest` refs to Helm and the dev pod

### Why `localhost:<port>` in image URLs?

Image pulls are done by the **kubelet on the worker node**, not the pod. The project-managed
registry uses a NodePort so nodes pull via `localhost:<nodePort>` on whichever node schedules the
pod. Your laptop pushes through the same port via the project-managed port-forward.

### Platform-aware builds

[`target-platform.sh`](target-platform.sh) inspects node `operatingSystem` and `architecture` in
the current kubectl context and sets `TARGET_PLATFORM` (for example `linux/amd64`). The development
flow passes that to builds as:

- `docker buildx --platform=${TARGET_PLATFORM}` when pushing to an external registry (`LOCAL_REGISTRY=0`)
- `build-for-local-registry.sh` (`docker buildx --load` + host `docker push`) for the in-cluster registry path

Dockerfiles default `BUILDPLATFORM` for the local-registry path:

```dockerfile
ARG BUILDPLATFORM=linux/amd64
FROM --platform=${BUILDPLATFORM} golang:1.25-bookworm AS builder
```

Override detection explicitly on mixed-platform clusters:

```bash
TARGET_PLATFORM=linux/amd64 make dev
```

## Build hosts behind Zscaler

Launcher builds can pass the host trust bundle via BuildKit `--secret`. The Dockerfile uses the
secret only as a temporary CA file for `apt` and `curl`; it never copies it into the image
filesystem. Runtime CA should still come from a Kubernetes Secret on launcher pods:

- **In-cluster registry path:** the launcher build uses
  `/etc/ssl/certs/ca-certificates.crt` by default; override it with
  `LOCAL_REGISTRY_BUILD_SECRET=/path/to/ca-bundle.crt`
- **External registry path (`LOCAL_REGISTRY=0`):** `buildKit.args` `--secret=id=host_ca,...` in
  `devspace.yaml`

The release workflow does not provide `host_ca`, and a build without the secret uses the standard
public CA bundle. `required=false` keeps the secret optional.

## Image tags

Development builds tag images with `dev-latest` and the current git commit hash. Helm and the dev
pod use explicit `:dev-latest` refs via `MANAGER_*_TAGGED` variables in `devspace.yaml`.

## DevSpace profiles

| Profile | Purpose |
| --------- | --------- |
| `external-registry` | Force build/push to `REGISTRY` + `imagePullPolicy: Always` (`LOCAL_REGISTRY=0`) |
| `local-registry` | In-cluster registry + buildx custom builds (default on remote via Makefile) |
| `auto-run-manager` | Run manager automatically instead of interactive shell |
| `always-pull` | `imagePullPolicy: Always` only (included in `external-registry`) |
| `debug` | Debug log levels |
| `single-manager` | `replicaCount: 1` |

## Helper scripts

| Script | Role |
| -------- | ------ |
| [`build-for-local-registry.sh`](build-for-local-registry.sh) | `docker buildx` build + push to in-cluster registry |
| [`ensure-registry-auth.sh`](ensure-registry-auth.sh) | Fail fast when `LOCAL_REGISTRY=0` but Docker is not logged into `REGISTRY` (uses `uv` from try-c9s tools) |
| [`ensure-local-registry.sh`](ensure-local-registry.sh) | Create the registry Deployment and NodePort Service before custom builds |
| [`ensure-registry-port-forward.sh`](ensure-registry-port-forward.sh) | `kubectl port-forward` for host `docker push` |
| [`local-registry-image-ref.sh`](local-registry-image-ref.sh) | Generate explicit `localhost:<nodePort>/...` refs for Helm/dev pod |
| [`target-platform.sh`](target-platform.sh) | Detect cluster node OS/arch for `TARGET_PLATFORM` |
| [`start.sh`](start.sh) | Dev container entrypoint |

## Troubleshooting

### `ImagePullBackOff` on `*-manager-dev`

- Stale deployment: `make purge-dev`, then `make dev`
- Image never pushed: confirm the build finished without push errors; inspect with
  `docker buildx imagetools inspect ghcr.io/clabernetes/clabernetes/clabernetes-manager-dev:dev-latest`
- Private GHCR package: make `clabernetes-manager-dev` public in GitHub package settings, or use
  the in-cluster registry (`make dev`)
- Wrong tag: DevSpace tags images with `dev-latest` and the git commit hash; both must exist in the registry

### Pushed to the wrong GHCR package path

`make dev` passes `DEV_REGISTRY` to DevSpace as `REGISTRY`. The value must be the full GHCR
repository prefix, for example `ghcr.io/clabernetes/clabernetes` — not just `ghcr.io/clabernetes`.
A short prefix produces images like `ghcr.io/clabernetes/clabernetes-manager-dev` instead of
`ghcr.io/clabernetes/clabernetes/clabernetes-manager-dev`, and the cluster will not find them where
you expect in the org packages UI.

### `Skip building image` with `LOCAL_REGISTRY=0`

DevSpace skips builds when `rebuildStrategy: ignoreContextChanges` and images already exist
locally — even if they were never pushed to `REGISTRY`. The `external-registry` profile (enabled
automatically for `LOCAL_REGISTRY=0`) forces `build_images --force-rebuild` on every `make dev`.

`LOCAL_REGISTRY=0` uses your Docker credentials from `~/.docker/config.json`. Log in before
`make dev`:

```bash
echo "$GITHUB_PAT" | docker login ghcr.io -u YOUR_GITHUB_USER --password-stdin
```

### `UNAUTHORIZED` pushing to GHCR during `make dev` (local-registry path)

The project-managed build script rewrites the logical image reference to the local registry before
running `docker push`. If GHCR appears in the push target, ensure the `local-registry` profile is
active and that the custom build command is being used.

### `failed to parse platform ""` / `--mount requires BuildKit`

The in-cluster registry path must use `build-for-local-registry.sh` (custom buildx). If builds log
`engine 'localregistry'`, the project-managed registry profile is not being used.

Empty `BUILDPLATFORM` usually means a build without `--platform` / `BUILDPLATFORM` build-arg.
Ensure Dockerfiles declare `ARG BUILDPLATFORM=...` and builds use buildx.

### Leftover registry after experiments

```bash
kubectl -n c9s-dev delete deploy,svc registry --ignore-not-found
# or
make purge-dev
```

### Mixed-platform cluster

```bash
TARGET_PLATFORM=linux/amd64 make dev
```
