# topology-resource Specification

## Purpose

Define Topology as an optional high-level source that compiles deterministically into directly reconcilable Node, Link, and LauncherProfile resources.

## Requirements

### Requirement: Topology is an auxiliary high-level resource

The system SHALL retain Topology as an auxiliary resource for quickly defining a lab through a supported Containerlab definition. Its controller SHALL expand that high-level definition into Node, Link, and LauncherProfile resources. Node and Link reconciliation MUST NOT require a Topology resource or distinguish generated resources from equivalent directly authored resources.

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

### Requirement: A source definition accepts native Containerlab vocabulary

The compiler SHALL accept a Containerlab definition that carries node vocabulary c9s does not implement, so an existing working Containerlab topology can be used unchanged. Fields the compiler does not recognize MUST be omitted from the emitted resources, and each one MUST be reported as a warning naming the field and its location in the definition. Unrecognized vocabulary MUST NOT fail compilation.

The compiler SHALL expose an unsupported-field policy. The compatibility Topology controller uses
the warning policy above. Strict callers MAY select an error policy, in which case all otherwise
lossy warnings are collected into deterministic structured diagnostics and compilation fails before
resources are rendered. This allows a CLI runtime to share the compiler's capability matrix without
silently changing topology semantics.

A definition that is malformed, or that declares a recognized field with an unusable value, SHALL still fail compilation rather than have that field silently omitted.
Structures that cannot identify realizable c9s resources, including external bridge/host pseudo
nodes, unresolved endpoint Nodes, `mgmt-net` or macvlan endpoints, and unsupported explicit link
types other than `veth`, SHALL fail under every policy. An explicit `veth` link SHALL accept brief
`node:interface` endpoints or structured node/interface mappings when both forms identify the same
representable c9s Link endpoints.

#### Scenario: Compile a definition carrying unimplemented vocabulary

- **WHEN** a Topology definition declares Containerlab node fields c9s does not implement
- **THEN** compilation succeeds, the emitted Nodes omit those fields, and each omitted field is reported as a warning naming it and where it appears

#### Scenario: Recognized vocabulary survives alongside unrecognized fields

- **WHEN** a source node mixes unrecognized fields with recognized ones
- **THEN** every recognized field is compiled into the emitted Node

#### Scenario: Direct manifest generation reports the same omissions

- **WHEN** direct manifest generation runs against a definition carrying unimplemented vocabulary
- **THEN** it reports the same omitted fields before the user applies anything

#### Scenario: Strict caller rejects lossy compatibility

- **WHEN** a strict caller compiles a definition containing unsupported fields, native management
  network settings, host-side port pinning, unusable labels, or link labels and vars that c9s cannot
  preserve
- **THEN** compilation fails with sorted diagnostics naming every unsupported location

#### Scenario: Compile an explicit veth link with structured endpoints

- **WHEN** a source definition declares an explicit `veth` link whose endpoints are node/interface mappings
- **THEN** the compiler emits the same c9s Link as the equivalent brief `node:interface` endpoint syntax

#### Scenario: Structurally impossible link fails in compatibility mode

- **WHEN** a definition references an external bridge, `mgmt-net`, macvlan, a nonexistent Node, or
  an unsupported explicit link type other than `veth`
- **THEN** compilation fails instead of creating resources that can only fail after deployment

#### Scenario: Reject a recognized field holding an unusable value

- **WHEN** a definition declares a recognized field whose value is of the wrong shape
- **THEN** compilation fails rather than omitting the field

#### Scenario: Reject a definition with no topology section

- **WHEN** a definition parses as YAML but declares no topology section
- **THEN** compilation fails with a parse error rather than crashing the controller

### Requirement: Containerlab node labels become Kubernetes labels

The compiler SHALL carry Containerlab node labels onto the emitted Node's object metadata, inheriting them from topology defaults and kinds the same way node environment variables are inherited. The Node controller SHALL propagate a Node's labels to the launcher Deployment and its Pods, excluding labels in the reserved `c9s.run/` namespace and controller-owned label keys, without altering the Deployment's Pod selector.

A label that Kubernetes would reject, or that is in the reserved `c9s.run/` namespace or uses a controller-owned label key, MUST be omitted with a warning naming it, so that a definition can neither produce an unacceptable Node nor set labels the controllers act on.

#### Scenario: Label a lab node

- **WHEN** a source topology node declares a Containerlab label
- **THEN** the emitted Node carries it in `metadata.labels`, and its launcher Deployment and Pods carry it too, so the Pods can be selected by it

#### Scenario: Labels inherit from defaults and kinds

- **WHEN** labels are declared at topology defaults, kind, and node level
- **THEN** the emitted Node carries the merged set, with the most specific value winning

#### Scenario: Omit a label Kubernetes cannot accept

- **WHEN** a source topology node declares a label whose key or value is invalid as a Kubernetes label
- **THEN** it is omitted with a warning naming it, and the Node is still emitted

#### Scenario: Omit a label in the reserved namespace

- **WHEN** a source topology node declares a label in the `c9s.run/` namespace
- **THEN** it is omitted with a warning, because those labels carry meaning to the controllers

#### Scenario: Omit a controller-owned label key

- **WHEN** a source topology node declares a valid label using a key such as `app.kubernetes.io/name` that c9s uses for identity or selection
- **THEN** it is omitted with a warning, because user metadata must not overwrite controller invariants

### Requirement: Topology status updates tolerate resource-version conflicts

The Topology controller SHALL retry aggregate status writes after a resource-version conflict and SHALL
avoid issuing an update when the current status already equals the desired status.

#### Scenario: Topology status races with a spec update

- **WHEN** a Topology status write receives a resource-version conflict
- **THEN** the controller refetches the current Topology and retries without failing the reconcile solely because of the conflict
