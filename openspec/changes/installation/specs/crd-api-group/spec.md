## MODIFIED Requirements

### Requirement: Breaking cutover from legacy API group
The release SHALL require a full uninstall and reinstall. The manager SHALL NOT delete legacy CRDs or migrate resources automatically. The repository SHALL provide a `make uninstall-c9s` target that uninstalls the Helm release, deletes all `*.c9s.run` and `*.clabernetes.containerlab.dev` CRDs, and removes the c9s namespace. Before invoking Helm, the installation workflow SHALL derive the selected chart's CRD API group, inspect the cluster for existing c9s CRDs, and refuse an in-place installation when the existing and selected groups differ.

#### Scenario: Manager does not auto-delete legacy CRDs
- **WHEN** the c9s manager starts on a cluster that still has `clabernetes.containerlab.dev` CRDs installed
- **THEN** the manager does not delete those legacy CRDs

#### Scenario: Installer detects legacy-to-new cutover
- **WHEN** the target cluster contains `clabernetes.containerlab.dev` CRDs and the selected chart contains `c9s.run` CRDs
- **THEN** installation exits before Helm and directs the user to the destructive uninstall/reinstall procedure

#### Scenario: Installer detects new-to-legacy cutover
- **WHEN** the target cluster contains `c9s.run` CRDs and the selected chart contains `clabernetes.containerlab.dev` CRDs
- **THEN** installation exits before Helm and directs the user to the destructive uninstall/reinstall procedure

#### Scenario: Installer permits same-group version change
- **WHEN** the target cluster and selected chart use the same c9s CRD API group
- **THEN** the API-group preflight permits installation to continue

#### Scenario: Uninstall removes CRDs and instances
- **WHEN** a user runs `make uninstall-c9s` against a cluster with c9s installed
- **THEN** the Helm release is removed, all `*.c9s.run` and `*.clabernetes.containerlab.dev` CRDs are deleted, and the c9s namespace is deleted

#### Scenario: Legacy CR instances are removed on uninstall
- **WHEN** a c9s CRD is deleted during `make uninstall-c9s`
- **THEN** all custom resource instances of that kind are removed from the cluster
