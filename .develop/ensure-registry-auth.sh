#!/usr/bin/env bash

# Fail fast when LOCAL_REGISTRY=0 will push to an external registry but the Docker daemon has no
# credentials for that registry host.

set -euo pipefail

registry="${REGISTRY:-ghcr.io/clabernetes/clabernetes}"
registry_host="${registry%%/*}"
config="${DOCKER_CONFIG:-${HOME}/.docker}/config.json"

if [[ ! -f "${config}" ]]; then
    echo "LOCAL_REGISTRY=0 pushes to ${registry}, but ${config} does not exist." >&2
    echo "Log in first: echo \"\$GITHUB_PAT\" | docker login ${registry_host} -u YOUR_GITHUB_USER --password-stdin" >&2
    exit 1
fi

uv_bin="${UV:-uv}"

if "${uv_bin}" run python - "${config}" "${registry_host}" <<'PY'
import json
import sys

config_path, host = sys.argv[1:3]
with open(config_path, encoding="utf-8") as handle:
    cfg = json.load(handle)

auths = cfg.get("auths") or {}
if host in auths and auths[host].get("auth"):
    sys.exit(0)

cred_helpers = cfg.get("credHelpers") or {}
if host in cred_helpers:
    sys.exit(0)

if cfg.get("credsStore"):
    sys.exit(0)

sys.exit(1)
PY
then
    exit 0
fi

echo "LOCAL_REGISTRY=0 pushes to ${registry}, but Docker is not logged into ${registry_host}." >&2
echo "Log in first: echo \"\$GITHUB_PAT\" | docker login ${registry_host} -u YOUR_GITHUB_USER --password-stdin" >&2
exit 1
