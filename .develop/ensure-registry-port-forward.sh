#!/usr/bin/env bash

# Expose the in-cluster DevSpace registry on 127.0.0.1:<node-port> for host-side docker push.
#
# DevSpace starts this port-forward automatically for its own localregistry builder, but custom
# image builds (used by LOCAL_REGISTRY=1) bypass that engine. Parallel custom builds share one
# forward: a flock lock prevents duplicate port-forwards, and a pid file reuses an existing one.
#
# Usage: ensure-registry-port-forward.sh <namespace> <service-name> <local-port>
#   local-port must match the registry service NodePort (for example 31548).

set -euo pipefail

namespace=${1:?namespace required}
registry_name=${2:?registry service name required}
local_port=${3:?local port required}

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
lock_file="${script_dir}/.registry-port-forward.lock"
pid_file="${script_dir}/.registry-port-forward.pid"

registry_ready() {
    curl -sf "http://127.0.0.1:${local_port}/v2/" >/dev/null 2>&1
}

wait_for_registry() {
    local attempt
    for attempt in $(seq 1 30); do
        if registry_ready; then
            return 0
        fi
        sleep 1
    done
    echo "timed out waiting for registry at 127.0.0.1:${local_port}" >&2
    return 1
}

start_port_forward() {
    if [[ -f "${pid_file}" ]]; then
        local pid
        pid=$(<"${pid_file}")
        if kill -0 "${pid}" 2>/dev/null; then
            wait_for_registry
            return 0
        fi
        rm -f "${pid_file}"
    fi

    kubectl port-forward \
        -n "${namespace}" \
        "svc/${registry_name}" \
        "${local_port}:5000" \
        >/dev/null 2>&1 &
    echo $! >"${pid_file}"
    wait_for_registry
}

if registry_ready; then
    exit 0
fi

exec 9>"${lock_file}"
if ! flock -x 9; then
    echo "failed to acquire registry port-forward lock" >&2
    exit 1
fi

# Another parallel build may have started the forward while we waited for the lock.
if registry_ready; then
    exit 0
fi

start_port_forward
