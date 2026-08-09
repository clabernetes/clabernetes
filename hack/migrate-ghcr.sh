#!/usr/bin/env bash

set -euo pipefail

ORAS="${ORAS:-oras}"
SOURCE_BASE="${SOURCE_BASE:-ghcr.io/srl-labs/clabernetes}"
DESTINATION_BASE="${DESTINATION_BASE:-ghcr.io/clabernetes/clabernetes}"
DRY_RUN="${DRY_RUN:-true}"

packages=(
  clabernetes-manager
  clabernetes-launcher
  clabernetes-ui
  clabverter
  clabernetes
  clicker
)

is_migration_tag() {
  local tag="$1"

  [[ "$tag" == "latest" || "$tag" == "dev-latest" ]] ||
    [[ "$tag" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]
}

for package in "${packages[@]}"; do
  source_repository="${SOURCE_BASE}/${package}"
  destination_repository="${DESTINATION_BASE}/${package}"
  declare -A destination_tags=()

  while IFS= read -r existing_tag; do
    if [[ -n "$existing_tag" ]]; then
      destination_tags["$existing_tag"]=1
    fi
  done < <("$ORAS" repo tags "$destination_repository" 2>/dev/null || true)

  while IFS= read -r tag; do
    if ! is_migration_tag "$tag"; then
      continue
    fi

    if [[ -n "${destination_tags[$tag]+x}" ]]; then
      echo "skipping existing ${destination_repository}:${tag}"
      continue
    fi

    echo "migrating ${source_repository}:${tag} -> ${destination_repository}:${tag}"

    if [[ "$DRY_RUN" == "true" ]]; then
      continue
    fi

    "$ORAS" copy --recursive \
      "${source_repository}:${tag}" \
      "${destination_repository}:${tag}"
  done < <("$ORAS" repo tags "$source_repository")
done
