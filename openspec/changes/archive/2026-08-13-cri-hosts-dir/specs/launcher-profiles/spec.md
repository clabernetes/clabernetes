## ADDED Requirements

### Requirement: Global Config supplies containerd registry hosts to pull-through launchers

Global Config SHALL accept an optional absolute, non-root containerd registry hosts directory that
contains no `.` or `..` path segments. When pull-through is enabled and the effective CRI kind is
containerd, the launcher Pod SHALL mount that node host directory read-only at both its original
path and `/etc/containerd/certs.d`; when both paths are equal, it SHALL mount the directory only
once. The effective CRI kind SHALL honor the configured CRI kind override before cluster
auto-detection.

#### Scenario: Custom containerd hosts directory is configured

- **WHEN** pull-through is `auto` or `always`, the effective CRI is containerd, and Config declares a custom containerd hosts directory
- **THEN** the launcher Pod requires that host directory and mounts it read-only at both the configured path and `/etc/containerd/certs.d`

#### Scenario: Conventional containerd hosts directory is configured

- **WHEN** Config declares `/etc/containerd/certs.d` for an enabled containerd pull-through launcher
- **THEN** the launcher Pod mounts the directory read-only exactly once at `/etc/containerd/certs.d`

#### Scenario: Pull-through is disabled

- **WHEN** a launcher profile resolves pull-through mode to `never`
- **THEN** the launcher Pod does not include the configured containerd hosts hostPath or mounts

#### Scenario: Effective CRI is not containerd

- **WHEN** pull-through mode is `auto` or `always` but neither the CRI override nor cluster detection selects containerd
- **THEN** the launcher Pod does not include the configured containerd hosts hostPath or mounts
