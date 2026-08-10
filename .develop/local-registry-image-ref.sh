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
namespace="${DEVSPACE_NAMESPACE:-${NS:-clabernetes}}"
registry_name="${LOCAL_REGISTRY_NAME:-registry}"

registry_port=$(
    kubectl get svc "${registry_name}" \
        -n "${namespace}" \
        -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null
)
if [[ -z "${registry_port}" ]]; then
    echo "failed to discover NodePort for local registry service ${registry_name} in namespace ${namespace}" >&2
    exit 1
fi

if [[ "${image}" =~ ^[^/]+/(.+)$ ]]; then
    image_path="${BASH_REMATCH[1]}"
else
    image_path="${image}"
fi

printf 'localhost:%s/%s:%s\n' "${registry_port}" "${image_path}" "${tag}"
