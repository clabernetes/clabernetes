# node-profiles Specification

## Purpose

Define reusable NodeProfile policy, attachment semantics, override behavior, reconciliation, and observability for workload realization.

## Requirements

### Requirement: NodeProfile owns realization policy

The system SHALL retain the namespaced NodeProfile resource as the reusable Kubernetes realization policy for direct Node workloads. It SHALL support Pod and primary application-container resources and scheduling, persistence, Kubernetes-native image pull policy and secrets, exposure behavior, and operational probes. Persistence policy SHALL include enablement, claim size, storage class, and a claim retention setting whose default garbage-collects the claim with its Node and whose retained setting lets the claim survive Node deletion for reattachment by an equivalent recreated Node. Kind-owned security, privilege, devices, component layout, and required resources SHALL come from the imported package plan and MUST NOT be overridden through launcher-era policy. NodeProfile MUST NOT configure a launcher image, inner runtime, Docker daemon, containerlab version, image-import path, or CRI socket.

#### Scenario: Reuse one profile policy

- **WHEN** multiple Nodes explicitly reference one NodeProfile
- **THEN** each Node's direct workload is realized using the same declared policy

#### Scenario: Profile declares claim retention

- **WHEN** the effective persistence policy enables retention and the Node is deleted
- **THEN** the claim and its data survive, and a recreated Node with the same identity and compatible persistence policy reattaches it

### Requirement: Status probes compose generic and application readiness

When a NodeProfile enables status probes, the system SHALL use direct Kubernetes container state and plan-defined readiness as the baseline signal. Configured TCP and SSH checks SHALL remain additional requirements, and the system MUST NOT infer application checks from an unplanned kind, image name, port, or credentials.

#### Scenario: Enabled profile supplies generic probes

- **WHEN** a Node references a NodeProfile with `statusProbes.enabled: true` and no TCP or SSH configuration
- **THEN** its direct workload contains plan-defined startup and readiness probes and reports application-container readiness

#### Scenario: Enabled profile supplies application probes

- **WHEN** a NodeProfile configures TCP or SSH checks
- **THEN** the workload requires direct container readiness and every configured application check before reporting ready

#### Scenario: Profile does not infer application behavior

- **WHEN** an enabled NodeProfile targets an arbitrary Node kind or image without explicit or planned application-probe configuration
- **THEN** c9s performs no inferred TCP or SSH check

#### Scenario: Custom startup allowance is preserved

- **WHEN** a profile sets `statusProbes.probeConfiguration.startupSeconds` to a value that is not an exact multiple of the probe period
- **THEN** the rendered startup probe allows at least the requested duration by rounding up to a whole probe interval

#### Scenario: Fast startup is not delayed by the allowance

- **WHEN** all direct readiness checks succeed before the configured startup allowance expires
- **THEN** the startup probe succeeds and the readiness probe takes over without waiting for the remaining allowance

### Requirement: NodeProfile excludes network topology intent except management compatibility

NodeProfile MUST NOT define Node endpoints, Link connectivity flavor, per-device management addresses, or per-node payload attachments. Shared direct management subnets, allocation ranges, and gateways MAY remain. Docker network name, MTU, and external-access settings MUST be removed.

#### Scenario: Inspect NodeProfile schema

- **WHEN** a Topology with supported custom management policy is compiled
- **THEN** its generated NodeProfile carries only the portable policy required by direct workloads

#### Scenario: Inspect network topology fields

- **WHEN** a user creates or reads a NodeProfile
- **THEN** its schema contains no Node endpoints, Link connectivity flavor, per-device management addresses, or Docker network settings

### Requirement: NodeProfile attachment is reference-only

NodeProfile SHALL NOT contain a Node selector or priority. The controller MUST apply a NodeProfile only when a Node or its pod-group primary explicitly references that profile.

#### Scenario: Profile exists but no Node references it

- **WHEN** a NodeProfile is created in a namespace and no Node references its name
- **THEN** the profile does not affect any existing Node

#### Scenario: Node labels change

- **WHEN** metadata labels on a Node change without changing `profileRef`
- **THEN** the effective NodeProfile remains unchanged

### Requirement: NodeProfile deterministically overrides global defaults

Global Config SHALL provide base direct-workload defaults. For a Node with `profileRef`, fields explicitly set in that one NodeProfile SHALL override corresponding Config defaults, while omitted fields SHALL retain Config values. The API MUST preserve unset versus explicit false, empty, or zero values wherever those values have distinct meanings.

#### Scenario: Profile overrides one field

- **WHEN** a referenced NodeProfile changes application-container resources but omits image-pull and scheduling settings
- **THEN** the Node uses the profile resources and retains Config-derived image-pull and scheduling values

#### Scenario: Profile explicitly clears a collection

- **WHEN** a supported NodeProfile collection is explicitly set to empty
- **THEN** the effective direct-workload policy uses the empty collection rather than inheriting the Config value

### Requirement: Missing or deleted referenced profiles fail closed

An explicit NodeProfile reference that cannot be resolved SHALL prevent creation or mutation of the direct workload. The system MUST surface the resolution failure on the affected Node and MUST NOT silently fall back to Config defaults.

#### Scenario: Referenced profile is deleted

- **WHEN** a NodeProfile still referenced by Nodes is deleted
- **THEN** affected Nodes report profile resolution failure and the controller does not roll them to an unintended default policy

#### Scenario: Missing profile is created

- **WHEN** the referenced NodeProfile is subsequently created
- **THEN** the affected Nodes automatically reconcile and clear the resolution failure

### Requirement: Profile events reconcile only referencing Nodes

The controller SHALL index Nodes by namespace and NodeProfile reference. A NodeProfile create, update, or delete event SHALL enqueue only direct workloads containing Nodes that reference that profile.

#### Scenario: Update an unused profile

- **WHEN** a NodeProfile with no references is updated
- **THEN** no Node workload is reconciled because of that update

#### Scenario: Update a shared profile

- **WHEN** a NodeProfile referenced by several Nodes is updated
- **THEN** all and only affected direct workloads reconcile to the new profile generation

### Requirement: Profile application is observable

The Node controller SHALL expose whether NodeProfile resolution succeeded and which profile UID and generation were applied. Status MUST distinguish no explicit profile from a resolved explicit profile.

#### Scenario: Node uses only global defaults

- **WHEN** a Node without `profileRef` is successfully realized
- **THEN** status reports successful profile resolution without claiming an applied NodeProfile

#### Scenario: Referenced profile generation changes

- **WHEN** a NodeProfile update is successfully applied to a Node
- **THEN** Node status reports the new applied generation

### Requirement: Image-pull defaults have Kubernetes semantics

`NodeProfile.spec.imagePull.pullSecrets` SHALL become the direct Pod's same-namespace `imagePullSecrets`. `NodeProfile.spec.imagePull.policy` SHALL be an optional Kubernetes application-container pull-policy default. An explicit Node/containerlab image-pull policy SHALL take precedence over the profile default, and the profile default SHALL take precedence over `Config.spec.imagePull.policy`. Controller registry metadata trust SHALL remain a separate global Config policy and MUST NOT be weakened by a namespaced profile.

#### Scenario: Profile supplies pull Secrets

- **WHEN** a profile lists same-namespace pull Secrets
- **THEN** the direct Pod references exactly those Secrets for kubelet image pulls and c9s does not mount their credentials into application containers

#### Scenario: Explicit Node pull policy wins

- **WHEN** a Node's supported containerlab definition declares an image-pull policy and its profile declares a different default
- **THEN** the planner-preserved Node policy is applied to that Node's application container

#### Scenario: Legacy pull-through value is present

- **WHEN** a manifest sets the removed `pullThroughOverride` path
- **THEN** the structural schema rejects the field rather than converting the value into `Always`, `IfNotPresent`, or `Never`

### Requirement: Breaking alpha API migration is explicit and fail closed

The breaking direct-runtime release SHALL remove launcher, Docker, nested-CRI, per-kind c9s policy, and Docker-management fields from NodeProfile, Config, and mirrored Topology sources in one generated-schema boundary. It SHALL retain only fields with defined direct semantics and add explicit global/profile Kubernetes pull-policy defaults. No removed field may be silently retargeted to a device application container.

The upgrade is a documented clean cutover: the release SHALL NOT ship in-place migration or preflight tooling, and the new structural schema SHALL reject removed and unknown fields after the cut.

#### Scenario: Apply removed field after the cut

- **WHEN** a user applies a manifest containing an old launcher, Docker, CRI, kind-keyed, or Docker-management path to the new CRD
- **THEN** the API server rejects the unknown field rather than preserving or ignoring it


### Requirement: NodeProfile controls direct device Pod affinity

`NodeProfile.spec.scheduling` SHALL accept the native Kubernetes `Affinity` structure. When a
Node resolves an explicit NodeProfile, the profile's configured affinity SHALL be copied to the
direct device Deployment Pod template. The affinity object SHALL be treated as one scheduling policy
value; the controller MUST NOT merge it with another affinity source.

#### Scenario: Apply affinity to every Node using one profile

- **WHEN** multiple Nodes in one namespace reference the same NodeProfile containing
  node affinity, pod affinity, or pod anti-affinity
- **THEN** every direct device Deployment created for those Nodes has the same corresponding
  `spec.template.spec.affinity` structure

#### Scenario: Preserve all native affinity sections

- **WHEN** a NodeProfile configures `nodeAffinity`, `podAffinity`, and `podAntiAffinity`
- **THEN** the rendered direct device Pod preserves each configured section, including required and
  preferred terms, weights, topology keys, and label selectors

#### Scenario: Omit affinity when the profile does not configure it

- **WHEN** a referenced NodeProfile omits `spec.scheduling.affinity`
- **THEN** the rendered direct device Pod has no affinity policy from that profile

#### Scenario: Preserve an explicitly provided empty affinity object

- **WHEN** a referenced NodeProfile explicitly provides an empty `affinity` object
- **THEN** profile resolution preserves the configured non-nil affinity value rather than treating it
  as an omitted profile field

#### Scenario: Grouped Nodes use the primary affinity policy

- **WHEN** secondary Nodes share the primary Node's Pod through
  `network-mode: container:<primary>`
- **THEN** the shared direct device Pod uses the primary Node's resolved NodeProfile affinity

#### Scenario: Profile affinity changes reconcile the direct workload

- **WHEN** a referenced NodeProfile's affinity is added, removed, or changed
- **THEN** the affected direct device Deployment is detected as non-conforming and is updated to the new
  affinity structure
