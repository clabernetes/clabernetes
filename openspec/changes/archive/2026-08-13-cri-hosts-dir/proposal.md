## Why

Pull-through launchers use nerdctl against the node's containerd socket, but they cannot currently
reuse registry mirrors, TLS certificates, or host configuration stored outside nerdctl's default
`/etc/containerd/certs.d` path. This causes image re-pulls to fail on distributions that keep the
containerd hosts directory elsewhere even though the node runtime itself can pull the image.

## What Changes

- Add an optional global Config and Helm value for the node's containerd registry hosts directory.
- For enabled pull-through on the effective containerd CRI, mount that host directory read-only at
  its original absolute path and at `/etc/containerd/certs.d` inside launcher Pods.
- Do not add the hostPath when pull-through is disabled or the effective CRI is not containerd, so
  automatic pull-through fallback cannot prevent a launcher from starting.
- Validate and document the path, preserve it through bootstrap merge/overwrite behavior, and cover
  chart rendering and launcher Deployment rendering with focused tests.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `launcher-profiles`: Define how global containerd registry-host configuration participates in
  image pull-through and launcher Pod realization.
- `documentation-site`: Require user guidance for configuring a non-default containerd hosts
  directory and its path/certificate constraints.

## Impact

The change affects the Config API and generated CRD/OpenAPI artifacts, Config bootstrap and manager
accessors, Helm values/schema/rendering, Node Deployment rendering and tests, and the image-pull
guide. It adds no dependency and does not change existing behavior when the new setting is unset.
