## MODIFIED Requirements

### Requirement: LauncherProfile owns launcher realization policy

The system SHALL retain the namespaced LauncherProfile resource as the reusable Kubernetes realization policy for direct Node workloads. It SHALL support Pod and primary application-container resources and scheduling, persistence, Kubernetes-native image pull policy and secrets, exposure behavior, and operational probes. Kind-owned security, privilege, devices, component layout, and required resources SHALL come from the imported package plan and MUST NOT be overridden through launcher-era policy. LauncherProfile MUST NOT configure a launcher image, inner runtime, Docker daemon, containerlab version, image-import path, or CRI socket.

#### Scenario: Reuse one launcher policy

- **WHEN** multiple Nodes explicitly reference one LauncherProfile
- **THEN** each Node's direct workload is realized using the same declared policy

### Requirement: Status probes compose generic and application readiness

When a LauncherProfile enables status probes, the system SHALL use direct Kubernetes container state and plan-defined readiness as the baseline signal. Configured TCP and SSH checks SHALL remain additional requirements, and the system MUST NOT infer application checks from an unplanned kind, image name, port, or credentials.

#### Scenario: Enabled profile supplies generic probes

- **WHEN** a Node references a LauncherProfile with `statusProbes.enabled: true` and no TCP or SSH configuration
- **THEN** its direct workload contains plan-defined startup and readiness probes and reports application-container readiness

#### Scenario: Enabled profile supplies application probes

- **WHEN** a LauncherProfile configures TCP or SSH checks
- **THEN** the workload requires direct container readiness and every configured application check before reporting ready

#### Scenario: Profile does not infer application behavior

- **WHEN** an enabled LauncherProfile targets an arbitrary Node kind or image without explicit or planned application-probe configuration
- **THEN** c9s performs no inferred TCP or SSH check

#### Scenario: Custom startup allowance is preserved

- **WHEN** a profile sets `statusProbes.probeConfiguration.startupSeconds` to a value that is not an exact multiple of the probe period
- **THEN** the rendered startup probe allows at least the requested duration by rounding up to a whole probe interval

#### Scenario: Fast startup is not delayed by the allowance

- **WHEN** all direct readiness checks succeed before the configured startup allowance expires
- **THEN** the startup probe succeeds and the readiness probe takes over without waiting for the remaining allowance

### Requirement: LauncherProfile excludes network topology intent except management compatibility

LauncherProfile MUST NOT define Node endpoints, Link connectivity flavor, per-device management addresses, or per-node payload attachments. Shared direct management subnets, allocation ranges, and gateways MAY remain. Docker network name, MTU, and external-access settings MUST be removed.

#### Scenario: Inspect LauncherProfile schema

- **WHEN** a Topology with supported custom management policy is compiled
- **THEN** its generated LauncherProfile carries only the portable policy required by direct workloads

#### Scenario: Inspect network topology fields

- **WHEN** a user creates or reads a LauncherProfile
- **THEN** its schema contains no Node endpoints, Link connectivity flavor, per-device management addresses, or Docker network settings

### Requirement: LauncherProfile deterministically overrides global defaults

Global Config SHALL provide base direct-workload defaults. For a Node with `launcherProfileRef`, fields explicitly set in that one LauncherProfile SHALL override corresponding Config defaults, while omitted fields SHALL retain Config values. The API MUST preserve unset versus explicit false, empty, or zero values wherever those values have distinct meanings.

#### Scenario: Profile overrides one field

- **WHEN** a referenced LauncherProfile changes application-container resources but omits image-pull and scheduling settings
- **THEN** the Node uses the profile resources and retains Config-derived image-pull and scheduling values

#### Scenario: Profile explicitly clears a collection

- **WHEN** a supported LauncherProfile collection is explicitly set to empty
- **THEN** the effective direct-workload policy uses the empty collection rather than inheriting the Config value

### Requirement: Missing or deleted referenced profiles fail closed

An explicit LauncherProfile reference that cannot be resolved SHALL prevent creation or mutation of the direct workload. The system MUST surface the resolution failure on the affected Node and MUST NOT silently fall back to Config defaults.

#### Scenario: Referenced profile is deleted

- **WHEN** a LauncherProfile still referenced by Nodes is deleted
- **THEN** affected Nodes report profile resolution failure and the controller does not roll them to an unintended default policy

#### Scenario: Missing profile is created

- **WHEN** the referenced LauncherProfile is subsequently created
- **THEN** the affected Nodes automatically reconcile and clear the resolution failure

### Requirement: Profile events reconcile only referencing Nodes

The controller SHALL index Nodes by namespace and LauncherProfile reference. A LauncherProfile create, update, or delete event SHALL enqueue only direct workloads containing Nodes that reference that profile.

#### Scenario: Update an unused profile

- **WHEN** a LauncherProfile with no references is updated
- **THEN** no Node workload is reconciled because of that update

#### Scenario: Update a shared profile

- **WHEN** a LauncherProfile referenced by several Nodes is updated
- **THEN** all and only affected direct workloads reconcile to the new profile generation

## ADDED Requirements

### Requirement: Image-pull defaults have Kubernetes semantics

`LauncherProfile.spec.imagePull.pullSecrets` SHALL become the direct Pod's same-namespace `imagePullSecrets`. `LauncherProfile.spec.imagePull.policy` SHALL be an optional Kubernetes application-container pull-policy default. An explicit Node/containerlab image-pull policy SHALL take precedence over the profile default, and the profile default SHALL take precedence over `Config.spec.imagePull.policy`. Controller registry metadata trust SHALL remain a separate global Config policy and MUST NOT be weakened by a namespaced profile.

#### Scenario: Profile supplies pull Secrets

- **WHEN** a profile lists same-namespace pull Secrets
- **THEN** the direct Pod references exactly those Secrets for kubelet image pulls and c9s does not mount their credentials into application containers

#### Scenario: Explicit Node pull policy wins

- **WHEN** a Node's supported containerlab definition declares an image-pull policy and its profile declares a different default
- **THEN** the planner-preserved Node policy is applied to that Node's application container

#### Scenario: Legacy pull-through value is present

- **WHEN** upgrade preflight finds `pullThroughOverride`
- **THEN** it reports the exact path and cluster-runtime migration without converting the value into `Always`, `IfNotPresent`, or `Never`

### Requirement: Breaking alpha API migration is explicit and fail closed

The breaking direct-runtime release SHALL remove launcher, Docker, nested-CRI, per-kind c9s policy, and Docker-management fields from LauncherProfile, Config, and mirrored Topology sources in one generated-schema boundary. It SHALL retain only fields with defined direct semantics and add explicit global/profile Kubernetes pull-policy defaults. No removed field may be silently retargeted to a device application container.

Before CRD replacement, a read-only preflight SHALL inspect stored resources using their unstructured old-schema representation. For every present removed field, including explicit empty, false, or zero values, it SHALL emit a sorted, value-free diagnostic containing object identity, exact JSON path, disposition, and replacement guidance. Any such diagnostic SHALL make preflight fail. The new structural schema SHALL reject removed and unknown fields after the cut.

#### Scenario: Launcher-only privilege is configured

- **WHEN** preflight finds `LauncherProfile.spec.deployment.privilegedLauncher`
- **THEN** it reports that device privilege comes only from the imported plan and does not copy the value into application-container security context

#### Scenario: Config contains kind-keyed resource policy

- **WHEN** preflight finds `Config.spec.deployment.resourcesByContainerlabKind`
- **THEN** it reports the field as removed and directs the user to generic defaults or explicit profile resources without retaining c9s kind matching

#### Scenario: Removed field is explicitly empty

- **WHEN** an old resource stores a removed path with an empty, false, or zero value
- **THEN** preflight still reports that path deterministically because typed zero values cannot prove omission

#### Scenario: Preflight finds no removed paths

- **WHEN** all stored Config, LauncherProfile, and Topology resources use only retained or replacement fields
- **THEN** preflight succeeds without mutating any resource

#### Scenario: Apply removed field after the cut

- **WHEN** a user applies a manifest containing an old launcher, Docker, CRI, kind-keyed, or Docker-management path to the new CRD
- **THEN** the API server rejects the unknown field rather than preserving or ignoring it

## REMOVED Requirements

### Requirement: Global Config supplies containerd registry hosts to pull-through launchers

**Reason**: Device images are pulled by the kubelet; mounting a node CRI socket and registry-host configuration into a launcher is obsolete and violates the direct-runtime boundary.

**Migration**: Configure registries in the cluster runtime and supply Kubernetes `imagePullSecrets` and image pull policy through c9s profile fields.
