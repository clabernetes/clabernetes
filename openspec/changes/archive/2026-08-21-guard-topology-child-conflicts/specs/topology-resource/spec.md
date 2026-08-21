## MODIFIED Requirements

### Requirement: Generated resources have deterministic identity and ownership

For a given Topology input, the compiler SHALL produce stable Node, Link, and LauncherProfile names
and specs. In-cluster generated resources SHALL carry a controller owner reference to the Topology
and labels sufficient for observability and pruning. Before creating or updating any generated
child, the Topology controller SHALL preflight every desired Node, Link, and LauncherProfile name
in the Topology namespace. An existing resource is compatible only when it is recognized as
generated for the current Topology; any unrelated occupant SHALL block child reconciliation.

#### Scenario: Reconcile unchanged input

- **WHEN** an unchanged Topology is reconciled repeatedly
- **THEN** the compiler produces no semantic changes to its generated resources

#### Scenario: Remove a wire from the source

- **WHEN** a Link is removed from the Topology definition
- **THEN** the Topology controller prunes the formerly generated Link without deleting unrelated
  resources

#### Scenario: Generated resource drifts

- **WHEN** a user mutates a compiler-owned Node, Link, or LauncherProfile away from compiled intent
- **THEN** the Topology controller restores the generated resource

#### Scenario: Detect an occupied generated child name

- **WHEN** a desired Node, Link, or LauncherProfile name is already occupied in the Topology
  namespace by an unrelated resource
- **THEN** the Topology controller creates or updates none of the desired child resources and
  reports every conflict in Topology status

#### Scenario: Permit existing children of the same Topology

- **WHEN** a desired child resource already exists and is recognized as generated for the current
  Topology
- **THEN** the Topology controller treats it as available for normal drift reconciliation rather
  than reporting a conflict

#### Scenario: Reconcile after conflicts clear

- **WHEN** all previously conflicting resources are removed or the Topology definition is changed
  so that its desired child names are free
- **THEN** the Topology controller clears the conflict error and reconciles the complete desired
  child set

### Requirement: Topology status remains bounded

Topology status SHALL contain aggregate counts, lifecycle state, conditions, and an optional bounded
controller-owned error string. It MUST NOT embed all generated resource specs, statuses, or an
unbounded per-child conflict structure.

#### Scenario: Increase compiled topology size

- **WHEN** the compiler emits additional Nodes and Links
- **THEN** Topology status grows only by changes to fixed aggregate fields or the bounded error
  string rather than one entry per child

#### Scenario: Report duplicate child resources

- **WHEN** one or more desired Node, Link, or LauncherProfile names conflict with unrelated
  resources in the Topology namespace
- **THEN** `status.error` contains the namespace, a deterministic sorted `type/name` list, and the
  guidance to create the Topology in a different namespace or disambiguate node names

#### Scenario: Clear resolved duplicate-resource status

- **WHEN** a later reconcile finds no child-resource conflicts
- **THEN** `status.error` is empty and normal aggregate status reconciliation resumes
