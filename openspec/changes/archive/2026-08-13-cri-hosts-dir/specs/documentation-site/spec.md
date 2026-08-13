## ADDED Requirements

### Requirement: Custom containerd registry hosts configuration is documented

The user documentation SHALL explain how to configure a non-default containerd registry hosts
directory through the Config API, how the directory is mounted for pull-through launchers, and the
node-path and certificate-path constraints that operators must satisfy.

#### Scenario: Operator configures a custom hosts directory

- **WHEN** an operator consults the image-pull guide for a containerd installation with a non-default hosts directory
- **THEN** the guide provides the Config field, explains both read-only mount locations, and states that the directory must exist on every eligible containerd node

#### Scenario: Hosts configuration references certificates

- **WHEN** an operator uses certificate paths in containerd hosts configuration
- **THEN** the guide explains that absolute certificate paths outside the configured directory are not mounted automatically
