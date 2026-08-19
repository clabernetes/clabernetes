## MODIFIED Requirements

### Requirement: Registered CRD kinds

The system SHALL register exactly these namespaced CRDs under `c9s.run/v1alpha1`: Topology, Node,
Link, LauncherProfile, and Config. The direct runtime SHALL NOT register ImageRequest because the
kubelet pulls application images from each rendered PodSpec.

#### Scenario: Manager installs CRDs on startup

- **WHEN** the c9s manager starts with CRD initialization enabled
- **THEN** all five `c9s.run` CRDs are present in the cluster and no ImageRequest CRD is installed

#### Scenario: Direct workload needs a private image

- **WHEN** a direct device Pod references a private image and same-namespace pull Secret
- **THEN** its PodSpec carries the image and `imagePullSecrets` directly without creating an ImageRequest, puller Pod, CRI-socket mount, export, or import operation
