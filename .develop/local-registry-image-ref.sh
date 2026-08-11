#!/usr/bin/env bash

# Print a localhost:<node-port>/... image reference for the registry managed by this project.
# The registry is created by make dev before DevSpace evaluates these command-backed variables.

set -euo pipefail

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <image-without-tag> [tag]" >&2
    exit 1
fi

image=$1
tag=${2:-dev-latest}
namespace="${DEVSPACE_NAMESPACE:-${NS:-c9s-dev}}"
registry_name="${LOCAL_REGISTRY_NAME:-registry}"
kubectl_args=()
if [[ -n "${KUBE_CONTEXT:-}" ]]; then
    kubectl_args+=(--context "${KUBE_CONTEXT}")
fi

registry_port=$(
    "${KUBECTL:-kubectl}" "${kubectl_args[@]}" get svc "${registry_name}" \
        -n "${namespace}" \
        -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null || true
)
if [[ -z "${registry_port}" ]]; then
    # Registry not deployed (purge, partial teardown, or non-local-registry dev). Return the
    # logical image ref so DevSpace can still evaluate config; purge does not use this value.
    printf '%s:%s\n' "${image}" "${tag}"
    exit 0
fi

if [[ "${image}" =~ ^[^/]+/(.+)$ ]]; then
    image_path="${BASH_REMATCH[1]}"
else
    image_path="${image}"
fi

printf 'localhost:%s/%s:%s\n' "${registry_port}" "${image_path}" "${tag}"
