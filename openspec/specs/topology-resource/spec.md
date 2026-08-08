# topology-resource Specification

## Purpose

Define Topology as an optional high-level source that compiles deterministically into directly reconcilable Node, Link, and LauncherProfile resources.

## Requirements

### Requirement: Topology is an auxiliary high-level resource

The system SHALL retain Topology as an auxiliary resource for quickly defining a lab through a supported Containerlab or KNE definition. Its controller SHALL expand that high-level definition into Node, Link, and LauncherProfile resources. Node and Link reconciliation MUST NOT require a Topology resource or distinguish generated resources from equivalent directly authored resources.

#### Scenario: Define a lab with Topology

- **WHEN** a user creates a valid supported Topology
- **THEN** the Topology controller emits the primitive resources needed to realize the lab

#### Scenario: Run without a Topology

- **WHEN** equivalent Node, Link, and LauncherProfile manifests are created directly
- **THEN** the same Node and Link controllers realize the lab

### Requirement: Compilation emits self-contained Nodes

The compiler SHALL parse the source definition and expand topology defaults, kinds, and other inherited node settings into every emitted Node. It SHALL attach per-node payload declarations to the corresponding Node.

#### Scenario: Source node inherits defaults

- **WHEN** a source topology node omits values supplied by topology defaults or a kind definition
- **THEN** its emitted Node contains the fully resolved values

#### Scenario: Source includes per-node files

- **WHEN** source processing associates payload files with one node
- **THEN** the emitted Node contains those payload attachment declarations

### Requirement: Compilation emits explicit LauncherProfile references

The compiler SHALL convert topology-level launcher/Kubernetes policy into one or more LauncherProfiles and SHALL stamp each emitted Node with the appropriate `launcherProfileRef`. It MUST preserve existing custom management-network settings in the generated profile as a compatibility bridge, and MUST NOT depend on profile label selectors or priority merging.

#### Scenario: Nodes share topology policy

- **WHEN** all source Nodes use the same launcher policy
- **THEN** the compiler emits one shared LauncherProfile and references it from all emitted Nodes

#### Scenario: One Node has a launcher override

- **WHEN** one source Node has distinct launcher resources or other launcher policy
- **THEN** the compiler emits a complete dedicated LauncherProfile for that Node and stamps its explicit reference

#### Scenario: Topology defines a custom management network

- **WHEN** an existing Topology defines custom shared management-network settings
- **THEN** the compiler preserves those settings in every generated LauncherProfile needed by its Nodes

### Requirement: Compilation puts connectivity on Links

The compiler SHALL translate topology connectivity policy into the connectivity field of every emitted Link. It MUST NOT place connectivity on LauncherProfile or require endpoint Nodes to resolve matching connectivity independently.

#### Scenario: Topology selects slurpeeth

- **WHEN** a source Topology selects slurpeeth connectivity
- **THEN** each emitted cross-node Link explicitly selects slurpeeth

#### Scenario: Topology omits connectivity

- **WHEN** a source Topology does not select a connectivity flavor
- **THEN** emitted Links either omit the field or select VXLAN with equivalent default behavior

### Requirement: Generated resources have deterministic identity and ownership

For a given Topology input, the compiler SHALL produce stable Node, Link, and LauncherProfile names and specs. In-cluster generated resources SHALL carry a controller owner reference to the Topology and labels sufficient for observability and pruning.

#### Scenario: Reconcile unchanged input

- **WHEN** an unchanged Topology is reconciled repeatedly
- **THEN** the compiler produces no semantic changes to its generated resources

#### Scenario: Remove a wire from the source

- **WHEN** a Link is removed from the Topology definition
- **THEN** the compiler prunes the formerly generated Link without deleting unrelated resources

#### Scenario: Generated resource drifts

- **WHEN** a user mutates a compiler-owned Node, Link, or LauncherProfile away from compiled intent
- **THEN** the Topology controller restores the generated resource

### Requirement: Dependencies are made available before Node realization

The compiler SHALL reconcile referenced LauncherProfiles and Links before creating or updating Nodes that depend on them. Node and Link controllers MUST nevertheless handle transiently unresolved references through status and later reconciliation.

#### Scenario: Create a new compiled lab

- **WHEN** the compiler emits resources for a new Topology
- **THEN** LauncherProfiles and Links are submitted before the Nodes that reference or consume them

#### Scenario: API events are observed out of order

- **WHEN** a Node controller observes a generated Node before its LauncherProfile is readable
- **THEN** it reports an unresolved profile and realizes the Node after the profile event arrives

### Requirement: Direct manifest generation matches in-cluster compilation

The command-line conversion path SHALL use the same compile and render behavior as the Topology controller when emitting direct Node, Link, and LauncherProfile manifests, except for in-cluster owner references and status.

#### Scenario: Emit primitive manifests

- **WHEN** a user converts a supported source topology to direct custom resources
- **THEN** the resulting specs are semantically equivalent to those emitted by an in-cluster Topology

### Requirement: Topology status remains bounded

Topology status SHALL contain aggregate counts, lifecycle state, and conditions only. It MUST NOT embed all generated resource specs or statuses.

#### Scenario: Increase compiled topology size

- **WHEN** the compiler emits additional Nodes and Links
- **THEN** Topology status grows only by changes to fixed aggregate fields rather than one entry per child

### Requirement: Large labs can bypass the aggregate source object

The system SHALL document and support direct application of generated primitive manifests for labs whose source definition would exceed the acceptable Topology object size.

#### Scenario: Deploy a large generated lab

- **WHEN** a user applies independently generated Node, Link, and LauncherProfile manifests without a Topology
- **THEN** no persisted Clabernetes object contains the entire lab definition
