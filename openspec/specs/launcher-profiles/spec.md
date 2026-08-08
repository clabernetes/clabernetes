# launcher-profiles Specification

## Purpose

Define reusable LauncherProfile policy, attachment semantics, override behavior, reconciliation, and observability for launcher realization.

## Requirements

### Requirement: LauncherProfile owns launcher realization policy

The system SHALL provide a namespaced LauncherProfile resource for reusable Kubernetes and launcher runtime configuration. It SHALL support launcher Pod resources and scheduling, security and privilege, persistence, launcher image/runtime settings, node-image pull integration, Kubernetes exposure behavior, and operational probes.

#### Scenario: Reuse one launcher policy

- **WHEN** multiple Nodes explicitly reference one LauncherProfile
- **THEN** each Node is realized using the same declared launcher policy

### Requirement: LauncherProfile excludes network topology intent except management compatibility

LauncherProfile MUST NOT define Node endpoints, Link connectivity flavor, per-device management addresses, or per-node payload attachments. It MAY retain shared Containerlab management-network configuration as a temporary compatibility field so existing Topology manifests preserve their behavior until that configuration receives a dedicated owner.

#### Scenario: Inspect LauncherProfile schema

- **WHEN** a Topology with custom management-network settings is compiled
- **THEN** its generated LauncherProfile carries those settings without placing them on Nodes or Links

#### Scenario: Inspect network topology fields

- **WHEN** a user creates or reads a LauncherProfile
- **THEN** its schema contains no Node endpoints, Link connectivity flavor, or per-device management addresses

### Requirement: LauncherProfile attachment is reference-only

LauncherProfile SHALL NOT contain a Node selector or priority. The controller MUST apply a LauncherProfile only when a Node or its launcher-group primary explicitly references that profile.

#### Scenario: Profile exists but no Node references it

- **WHEN** a LauncherProfile is created in a namespace and no Node references its name
- **THEN** the profile does not affect any existing Node

#### Scenario: Node labels change

- **WHEN** metadata labels on a Node change without changing `launcherProfileRef`
- **THEN** the effective LauncherProfile remains unchanged

### Requirement: LauncherProfile deterministically overrides global defaults

Global Config SHALL provide base launcher defaults. For a Node with `launcherProfileRef`, fields explicitly set in that one LauncherProfile SHALL override corresponding Config defaults, while omitted fields SHALL retain Config values. The API MUST preserve unset versus explicit false, empty, or zero values wherever those values have distinct meanings.

#### Scenario: Profile overrides one field

- **WHEN** a referenced LauncherProfile changes launcher resources but omits image and scheduling settings
- **THEN** the Node uses the profile resources and retains Config-derived image and scheduling values

#### Scenario: Profile explicitly clears a collection

- **WHEN** a supported LauncherProfile collection is explicitly set to empty
- **THEN** the effective launcher policy uses the empty collection rather than inheriting the Config value

### Requirement: Missing or deleted referenced profiles fail closed

An explicit LauncherProfile reference that cannot be resolved SHALL prevent creation or mutation of the launcher workload. The system MUST surface the resolution failure on the affected Node and MUST NOT silently fall back to Config defaults.

#### Scenario: Referenced profile is deleted

- **WHEN** a LauncherProfile still referenced by Nodes is deleted
- **THEN** affected Nodes report profile resolution failure and the controller does not roll them to an unintended default policy

#### Scenario: Missing profile is created

- **WHEN** the referenced LauncherProfile is subsequently created
- **THEN** the affected Nodes automatically reconcile and clear the resolution failure

### Requirement: Profile events reconcile only referencing Nodes

The controller SHALL index Nodes by namespace and LauncherProfile reference. A LauncherProfile create, update, or delete event SHALL enqueue only launcher groups containing Nodes that reference that profile.

#### Scenario: Update an unused profile

- **WHEN** a LauncherProfile with no references is updated
- **THEN** no Node launcher workload is reconciled because of that update

#### Scenario: Update a shared profile

- **WHEN** a LauncherProfile referenced by several Nodes is updated
- **THEN** all and only affected launcher groups reconcile to the new profile generation

### Requirement: Profile application is observable

The Node controller SHALL expose whether LauncherProfile resolution succeeded and which profile UID and generation were applied. Status MUST distinguish no explicit profile from a resolved explicit profile.

#### Scenario: Node uses only global defaults

- **WHEN** a Node without `launcherProfileRef` is successfully realized
- **THEN** status reports successful profile resolution without claiming an applied LauncherProfile

#### Scenario: Referenced profile generation changes

- **WHEN** a LauncherProfile update is successfully applied to a Node
- **THEN** Node status reports the new applied generation
