#!/usr/bin/env bash

# Build and push one development image into DevSpace's in-cluster local registry.
#
# DevSpace's built-in localregistry engine uses classic `docker build` (no BuildKit), which breaks
# RUN --mount and other BuildKit features. This script uses `docker buildx` on the host instead.
#
# Flow:
#   1. Rewrite the image reference to 127.0.0.1:<node-port>/... (cluster nodes pull via NodePort).
#   2. `docker buildx build --load` on the host (optional --secret for Zscaler build hosts).
#   3. Ensure kubectl port-forward to the registry service (ensure-registry-port-forward.sh).
#   4. `docker push` from the host daemon through that port-forward.

set -euo pipefail

# DevSpace exports VERSION while running custom builds. Unset it so a stray $VERSION expansion
# cannot corrupt shell words (for example turning VERSION=... into ERSION=...).
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

kubectl_args=()
if [[ -n "${KUBE_CONTEXT:-}" ]]; then
    kubectl_args+=(--context "${KUBE_CONTEXT}")
fi

if [[ "${LOCAL_REGISTRY_BUILD:-}" == "1" ]]; then
    namespace="${DEVSPACE_NAMESPACE:-${NS:-c9s-dev}}"
    registry_name="${LOCAL_REGISTRY_NAME:-registry}"

    registry_port=$(
        "${KUBECTL:-kubectl}" "${kubectl_args[@]}" get svc "${registry_name}" \
            -n "${namespace}" \
            -o jsonpath='{.spec.ports[0].nodePort}'
    )
    if [[ -z "${registry_port}" ]]; then
        echo "failed to discover NodePort for local registry service ${registry_name} in namespace ${namespace}" >&2
        exit 1
    fi

    # Custom commands receive the logical REGISTRY image name; rewrite it to the managed registry.
    if [[ "${image}" != localhost:* && "${image}" != 127.0.0.1:* ]]; then
        if [[ "${image}" =~ ^[^/]+/(.+)$ ]]; then
            image="127.0.0.1:${registry_port}/${BASH_REMATCH[1]}"
        fi
    fi
    registry_port_forward="${script_dir}/ensure-registry-port-forward.sh"
fi

if [[ -n "${TARGET_PLATFORM:-}" ]]; then
    platform=${TARGET_PLATFORM}
else
    platform=$("${script_dir}/target-platform.sh")
fi

build_args=(--build-arg "BUILDPLATFORM=${platform}")
for arg in "$@"; do
    build_args+=(--build-arg "$arg")
done

# Mirror the VERSION build-arg used by the standard DevSpace image definitions.
case "$(basename "${dockerfile}")" in
    manager.Dockerfile|launcher.Dockerfile|clabverter.Dockerfile)
        commit_hash=$(git -C "${repo_root}" describe --always --abbrev=8 2>/dev/null || true)
        if [[ -n "${commit_hash}" ]]; then
            build_args+=(--build-arg "VERSION=0.0.0-${commit_hash}")
        fi
        ;;
esac

secret_args=()
secret_path="${LOCAL_REGISTRY_BUILD_SECRET:-}"
if [[ -z "${secret_path}" && "$(basename "${dockerfile}")" == "launcher.Dockerfile" ]]; then
    secret_path=/etc/ssl/certs/ca-certificates.crt
fi
if [[ -n "${secret_path}" && -f "${secret_path}" ]]; then
    secret_args+=(--secret "id=host_ca,src=${secret_path}")
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
    KUBE_CONTEXT="${KUBE_CONTEXT:-}" bash "${registry_port_forward}" "${namespace}" "${registry_name}" "${registry_port}"
fi

if [[ "${LOCAL_REGISTRY_BUILD:-}" == "1" ]]; then
    exec 8>"${script_dir}/.registry-push.lock"
    flock -x 8
fi

docker push "${image}:${tag}"

# Manager, launcher, and dev images also publish a dev-latest tag in the default DevSpace config.
case "$(basename "${dockerfile}")" in
    manager.Dockerfile|launcher.Dockerfile|dev.Dockerfile)
        docker tag "${image}:${tag}" "${image}:dev-latest"
        docker push "${image}:dev-latest"
        ;;
esac
