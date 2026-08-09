#!/usr/bin/env bash

# Resolve the single OCI platform used for DevSpace image builds.
#
# .develop/devspace.yaml invokes this script to populate DETECTED_TARGET_PLATFORM, which becomes
# the default for TARGET_PLATFORM and is passed to BuildKit as --platform=<os>/<architecture>.
# An explicit TARGET_PLATFORM environment variable takes precedence. Otherwise, every node in the
# current kubectl context is inspected. Mixed-platform clusters must select one platform explicitly
# because the development images are single-platform images.

set -euo pipefail

if [[ -n "${TARGET_PLATFORM:-}" ]]; then
    printf '%s' "${TARGET_PLATFORM}"
    exit 0
fi

platforms="$(
    kubectl get nodes \
        -o jsonpath='{range .items[*]}{.status.nodeInfo.operatingSystem}/{.status.nodeInfo.architecture}{"\n"}{end}' |
        sort -u
)"

if [[ -z "${platforms}" ]]; then
    echo "failed to detect a target platform from the current Kubernetes cluster" >&2
    exit 1
fi

if [[ "${platforms}" == *$'\n'* ]]; then
    echo "multiple cluster platforms detected; set TARGET_PLATFORM explicitly:" >&2
    echo "${platforms}" >&2
    exit 1
fi

printf '%s' "${platforms}"
