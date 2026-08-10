#!/usr/bin/env bash

# Expose the project-managed registry on 127.0.0.1:<node-port> for host-side docker push.
#
# Custom image builds bypass DevSpace's localregistry engine, so they set up port-forward
# themselves before pushing. Parallel builds share one forward via flock + pid file. The default
# 0.0.0.0 bind also makes the forward reachable by Docker daemons running in a VM (for example
# OrbStack); LOCAL_REGISTRY_PORT_FORWARD_ADDRESS can restrict it to a specific address.

set -euo pipefail

namespace=${1:?namespace required}
registry_name=${2:?registry service name required}
local_port=${3:?local port required}
bind_address="${LOCAL_REGISTRY_PORT_FORWARD_ADDRESS:-0.0.0.0}"
kubectl=${KUBECTL:-kubectl}

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
lock_file="${script_dir}/.registry-port-forward.lock"
pid_file="${script_dir}/.registry-port-forward.pid"
log_file="${script_dir}/.registry-port-forward.log"

registry_ready() {
    curl -sf "http://127.0.0.1:${local_port}/v2/" >/dev/null 2>&1
}

wait_for_registry() {
    local attempts=${1:-30}
    local attempt
    for attempt in $(seq 1 "${attempts}"); do
        if registry_ready; then
            return 0
        fi
        sleep 1
    done
    return 1
}

# A recorded forward is only reusable when it serves the port we were asked for; recreating the
# registry Service hands out a new NodePort, leaving the previous forward alive but useless.
reuse_forward() {
    [[ -f "${pid_file}" ]] || return 1

    local pid port
    read -r pid port <"${pid_file}" || return 1
    [[ -n "${pid}" && "${port}" == "${local_port}" ]] || return 1
    kill -0 "${pid}" 2>/dev/null || return 1

    wait_for_registry 5
}

kill_recorded_forward() {
    [[ -f "${pid_file}" ]] || return 0

    local pid
    read -r pid _ <"${pid_file}" || true
    if [[ -n "${pid:-}" ]]; then
        kill "${pid}" 2>/dev/null || true
    fi
    rm -f "${pid_file}"
}

start_port_forward() {
    if reuse_forward; then
        return 0
    fi

    kill_recorded_forward

    # Detach the forward from the short-lived custom build process. DevSpace may terminate the
    # build command's process group after one image finishes, while other parallel image pushes
    # still depend on this forward.
    nohup setsid "${kubectl}" port-forward \
        --address "${bind_address}" \
        -n "${namespace}" \
        "svc/${registry_name}" \
        "${local_port}:5000" \
        >"${log_file}" 2>&1 </dev/null 9>&- &
    echo "$! ${local_port}" >"${pid_file}"

    if ! wait_for_registry; then
        echo "timed out waiting for registry at 127.0.0.1:${local_port}; see ${log_file}" >&2
        return 1
    fi
}

if registry_ready; then
    exit 0
fi

exec 9>"${lock_file}"
if ! flock -x 9; then
    echo "failed to acquire registry port-forward lock" >&2
    exit 1
fi

if registry_ready; then
    exit 0
fi

start_port_forward
