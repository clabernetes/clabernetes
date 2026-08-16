## ADDED Requirements

### Requirement: LauncherProfile controls launcher Pod affinity

`LauncherProfile.spec.scheduling` SHALL accept the native Kubernetes `Affinity` structure. When a
Node resolves an explicit LauncherProfile, the profile's configured affinity SHALL be copied to the
launcher Deployment Pod template. The affinity object SHALL be treated as one launcher policy
value; the controller MUST NOT merge it with another affinity source.

#### Scenario: Apply affinity to every Node using one profile

- **WHEN** multiple launcher Nodes in one namespace reference the same LauncherProfile containing
  node affinity, pod affinity, or pod anti-affinity
- **THEN** every launcher Deployment created for those Nodes has the same corresponding
  `spec.template.spec.affinity` structure

#### Scenario: Preserve all native affinity sections

- **WHEN** a LauncherProfile configures `nodeAffinity`, `podAffinity`, and `podAntiAffinity`
- **THEN** the rendered launcher Pod preserves each configured section, including required and
  preferred terms, weights, topology keys, and label selectors

#### Scenario: Omit affinity when the profile does not configure it

- **WHEN** a referenced LauncherProfile omits `spec.scheduling.affinity`
- **THEN** the rendered launcher Pod has no affinity policy from that profile

#### Scenario: Preserve an explicitly provided empty affinity object

- **WHEN** a referenced LauncherProfile explicitly provides an empty `affinity` object
- **THEN** profile resolution preserves the configured non-nil affinity value rather than treating it
  as an omitted profile field

#### Scenario: Grouped Nodes use the primary affinity policy

- **WHEN** secondary Nodes share the primary Node's launcher through
  `network-mode: container:<primary>`
- **THEN** the shared launcher Pod uses the primary Node's resolved LauncherProfile affinity

#### Scenario: Profile affinity changes reconcile the launcher

- **WHEN** a referenced LauncherProfile's affinity is added, removed, or changed
- **THEN** the affected launcher Deployment is detected as non-conforming and is updated to the new
  affinity structure
