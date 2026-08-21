## ADDED Requirements

### Requirement: Topology projects launcher affinity into generated profiles

`Topology.spec.deployment.scheduling.affinity` SHALL accept the native Kubernetes `Affinity`
structure and SHALL be copied to every generated LauncherProfile required by the Topology. The
Topology controller and the direct CR-manifest generation path MUST preserve the affinity structure
without placing it on Nodes or Links.

#### Scenario: Apply topology-wide affinity

- **WHEN** a Topology configures node affinity, pod affinity, or pod anti-affinity under
  `spec.deployment.scheduling`
- **THEN** its generated shared LauncherProfile contains the same affinity structure and every
  generated Node references that profile

#### Scenario: Preserve affinity-only scheduling

- **WHEN** a Topology configures affinity but omits `nodeSelector` and `tolerations`
- **THEN** the generated LauncherProfile still contains the scheduling block and its affinity

#### Scenario: Preserve affinity on dedicated profiles

- **WHEN** a Topology emits a dedicated LauncherProfile for a Node with distinct resource policy
- **THEN** that dedicated profile retains the Topology-wide affinity from the shared launcher policy

#### Scenario: Restore drifted generated affinity

- **WHEN** a generated LauncherProfile's affinity differs from the Topology's declared affinity
- **THEN** the Topology controller restores the generated profile to the declared affinity

#### Scenario: Emit equivalent direct manifests

- **WHEN** `clabverter --emit-crs` processes a Topology definition with launcher affinity
- **THEN** the emitted LauncherProfile manifest contains the same affinity structure as in-cluster
  Topology compilation
