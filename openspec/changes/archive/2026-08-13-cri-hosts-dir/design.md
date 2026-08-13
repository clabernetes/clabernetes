## Context

See `proposal.md` for motivation. Pull-through launchers call rootful nerdctl against the node's
containerd socket, so nerdctl reads registry host namespaces from `/etc/containerd/certs.d` inside
the launcher. Some Kubernetes distributions place the equivalent node-runtime directory elsewhere,
and `hosts.toml` may contain absolute certificate paths rooted in that distribution-specific tree.

Global Config already owns cluster-wide CRI socket/kind overrides and forms the base of every
resolved launcher profile. Config changes enqueue Nodes for reconciliation, and Helm bootstraps the
Config singleton through a ConfigMap.

## Goals / Non-Goals

**Goals:**

- Reuse one node directory without copying or rewriting registry configuration.
- Preserve absolute certificate paths rooted in that directory.
- Keep automatic pull-through fallback from depending on containerd-only host paths when the
  effective CRI is not containerd.
- Keep API, Helm, generated artifacts, runtime rendering, tests, and documentation aligned.

**Non-Goals:**

- Supporting CRI-O registry configuration or converting it to containerd hosts format.
- Mounting certificate paths outside the configured directory.
- Adding a per-Node or per-LauncherProfile node-filesystem path.
- Creating a missing directory on Kubernetes nodes.

## Decisions

### 1. Keep the setting in global Config

The directory describes node filesystem layout and must agree with scheduling and CRI selection, so
it belongs beside the existing global CRI socket and kind overrides. A namespaced LauncherProfile
override would imply workload owners can safely choose arbitrary node host paths, which is not a
portable launcher policy.

### 2. Mount one HostPath at two read-only destinations

The source uses the Kubernetes `Directory` HostPath type so a typo fails explicitly rather than
creating an empty directory. The same volume is mounted at its original absolute path for rooted
certificate references and at `/etc/containerd/certs.d` for nerdctl discovery. The conventional
path is emitted only once when it is also the source path.

Copying files into an init-container volume was rejected because it would require extra privilege,
startup work, update semantics, and certificate-path rewriting without improving the contract.

### 3. Resolve one effective CRI kind for CRI-dependent rendering

Deployment rendering selects the configured CRI kind override first and otherwise uses cluster
detection. The socket mount, launcher environment, and registry-host mounts use that same effective
kind. Registry-host mounts are omitted for non-containerd kinds and when pull-through is `never`.

This keeps explicit mixed-cluster overrides functional and preserves `auto` fallback on clusters
where containerd pull-through is unavailable.

### 4. Validate the API and Helm path consistently

The value must be an absolute, non-root path with no `.` or `..` segments. Rendering still cleans
and defensively rejects an invalid path in case it receives data that predates validation. Helm
quotes the ConfigMap scalar so spaces and comment characters remain part of the path.

## Risks / Trade-offs

- **The path must exist on every eligible node** → Use `Directory` HostPath and document the
  scheduling constraint so failures are explicit.
- **HostPath exposes node files to a privileged launcher** → Limit the mount to a configured
  directory and mark every mount read-only; Config remains an administrator-owned global setting.
- **External certificates may live elsewhere** → Do not broaden host access automatically;
  document that those certificate paths need separate provisioning.
- **A Config update rolls launcher Deployments** → This matches existing global launcher-default
  reconciliation and is required for the new mount to take effect.

## Migration Plan

The field is optional and defaults to unset, so upgrades retain existing Deployments until an
operator configures it. Generated CRDs/OpenAPI and the Helm schema are shipped with the controller
change. Rollback consists of clearing the field, which reconciles the extra HostPath and mounts out
of launcher Deployments.
