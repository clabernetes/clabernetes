#!/usr/bin/env bash

# Build and push one development image into DevSpace's in-cluster local registry.
#
# This script is used only by the DevSpace `local-registry` profile (enabled with
# `LOCAL_REGISTRY=1 make dev`). DevSpace's built-in localregistry builder cannot be used here
# because it relies on a rootless BuildKit sidecar that many remote clusters cannot run, and it
# does not pass platform flags correctly to docker build.
#
# Flow:
#   1. Rewrite the image reference from REGISTRY (for example ghcr.io/...) to
#      127.0.0.1:<node-port>/... so cluster nodes can pull from the in-cluster registry.
#   2. Build with `docker buildx build --load` on the host.
#   3. Ensure a kubectl port-forward to the registry service exists (see
#      ensure-registry-port-forward.sh).
#   4. `docker push` from the host daemon through that port-forward.
#
# The default `make dev` path does not use this script; it builds with buildx and pushes to
# REGISTRY directly via DevSpace.

set -euo pipefail

# DevSpace exports VERSION/COMMIT_HASH while running custom builds. Unset VERSION so a stray
# $VERSION expansion cannot corrupt shell words (for example turning VERSION=... into ERSION=...).
unset VERSION

if [[ $# -lt 4 ]]; then
    echo "usage: $0 <image> <tag> <dockerfile> <context> [build-arg...]" >&2
    exit 1
fi

image=$1
tag=$2
dockerfile=$3
context=$4
shift 4

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "${script_dir}/.." && pwd)
cd "${repo_root}"

if [[ "${LOCAL_REGISTRY_BUILD:-}" == "1" ]]; then
    namespace="${DEVSPACE_NAMESPACE:-${NS:-clabernetes}}"
    registry_name="${LOCAL_REGISTRY_NAME:-registry}"

    # DevSpace deploys the registry with a NodePort. Cluster nodes pull via that port on
    # localhost; the developer machine reaches the same port through kubectl port-forward.
    registry_port=$(
        kubectl get svc "${registry_name}" \
            -n "${namespace}" \
            -o jsonpath='{.spec.ports[0].nodePort}'
    )
    if [[ -z "${registry_port}" ]]; then
        echo "failed to discover NodePort for local registry service ${registry_name} in namespace ${namespace}" >&2
        exit 1
    fi

    # DevSpace may already pass a rewritten localhost:<port>/... runtime image name. Otherwise
    # rewrite ghcr.io/... to match the in-cluster registry path nodes pull from.
    if [[ "${image}" != localhost:* && "${image}" != 127.0.0.1:* ]]; then
        if [[ "${image}" =~ ^[^/]+/(.+)$ ]]; then
            image="localhost:${registry_port}/${BASH_REMATCH[1]}"
        fi
    fi
    registry_port_forward="${script_dir}/ensure-registry-port-forward.sh"
fi

if [[ -n "${TARGET_PLATFORM:-}" ]]; then
    platform=${TARGET_PLATFORM}
else
    platform=$("${script_dir}/target-platform.sh")
fi

build_args=()
for arg in "$@"; do
    build_args+=(--build-arg "$arg")
done

# Mirror the image VERSION build-arg used by the standard DevSpace image definitions.
if [[ ${#build_args[@]} -eq 0 ]]; then
    case "$(basename "${dockerfile}")" in
        manager.Dockerfile|launcher.Dockerfile|clabverter.Dockerfile)
            commit_hash=$(git -C "${repo_root}" describe --always --abbrev=8 2>/dev/null || true)
            if [[ -n "${commit_hash}" ]]; then
                build_args+=(--build-arg "VERSION=0.0.0-${commit_hash}")
            fi
            ;;
    esac
fi

secret_args=()
if [[ -n "${LOCAL_REGISTRY_BUILD_SECRET:-}" ]]; then
    secret_args+=(--secret "id=host_ca,src=${LOCAL_REGISTRY_BUILD_SECRET}")
fi

# --load imports the image into the host docker daemon. buildx --push would run inside the
# buildkit container and cannot reach the registry port-forward on the host.
docker buildx build \
    --platform="${platform}" \
    "${build_args[@]}" \
    "${secret_args[@]}" \
    -f "${dockerfile}" \
    -t "${image}:${tag}" \
    --load \
    "${context}"

if [[ "${LOCAL_REGISTRY_BUILD:-}" == "1" ]]; then
    bash "${registry_port_forward}" "${namespace}" "${registry_name}" "${registry_port}"
fi
docker push "${image}:${tag}"

# Manager, launcher, and dev images also publish a dev-latest tag in the default DevSpace config.
case "$(basename "${dockerfile}")" in
    manager.Dockerfile|launcher.Dockerfile|dev.Dockerfile)
        docker tag "${image}:${tag}" "${image}:dev-latest"
        docker push "${image}:dev-latest"
        ;;
esac
