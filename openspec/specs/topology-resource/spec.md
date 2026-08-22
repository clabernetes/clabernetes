# topology-resource Specification

## Purpose

Define Topology as an optional high-level source that compiles deterministically into directly reconcilable Node, Link, and NodeProfile resources.

## Requirements

### Requirement: Topology is an auxiliary high-level resource

Topology SHALL remain an optional aggregate source that compiles into the direct primitive resources. Node, Link, and NodeProfile SHALL remain the authoritative runtime inputs after compilation.

#### Scenario: Define a lab with Topology

- **WHEN** a user creates a valid supported Topology
- **THEN** the Topology controller emits the primitive resources and plans needed to realize the direct lab

#### Scenario: Run without a Topology

- **WHEN** equivalent Node, Link, and NodeProfile manifests are created directly
- **THEN** the same Node and Link controllers realize an equivalent direct lab

### Requirement: Compilation emits self-contained Nodes

The compiler SHALL parse the source definition and expand topology defaults, kinds, and other inherited node settings into every emitted Node. It SHALL attach per-node payload declarations to the corresponding Node and SHALL preserve every representable input required by the direct planner.

#### Scenario: Source node inherits defaults

- **WHEN** a source topology Node omits values supplied by topology defaults or a kind definition
- **THEN** its emitted Node contains the fully resolved values needed to produce the same device plan

#### Scenario: Source includes per-node files

- **WHEN** source processing associates payload files with one Node
- **THEN** the emitted Node contains those payload attachment declarations

### Requirement: Compilation emits explicit NodeProfile references

The compiler SHALL convert topology-level Kubernetes realization policy into one or more NodeProfiles and SHALL stamp each emitted Node with the appropriate `profileRef`. It MUST preserve supported direct management policy and MUST NOT emit Docker, launcher-image, nested-CRI, or containerlab-version policy.

#### Scenario: Nodes share topology policy

- **WHEN** all source Nodes use the same direct-workload policy
- **THEN** the compiler emits one shared NodeProfile and references it from all emitted Nodes

#### Scenario: One Node has a profile override

- **WHEN** one source Node has distinct resources or other direct-workload policy
- **THEN** the compiler emits a complete dedicated NodeProfile for that Node and stamps its explicit reference

#### Scenario: Topology defines a custom management network

- **WHEN** an existing Topology defines management settings with supported direct semantics
- **THEN** the compiler preserves those settings in the generated resources that own them

### Requirement: Generated resources have deterministic identity and ownership

For a given Topology input, the compiler SHALL produce stable Node, Link, and NodeProfile names and specs. In-cluster generated resources SHALL carry a controller owner reference to the Topology and labels sufficient for observability and pruning. Before creating or updating any generated child, the Topology controller SHALL preflight every desired Node, Link, and NodeProfile name in the Topology namespace. An existing resource is compatible only when it is recognized as generated for the current Topology; any unrelated occupant SHALL block child reconciliation.

#### Scenario: Reconcile unchanged input

- **WHEN** an unchanged Topology is reconciled repeatedly
- **THEN** the compiler produces no semantic changes to its generated resources

#### Scenario: Remove a wire from the source

- **WHEN** a Link is removed from the Topology definition
- **THEN** the compiler prunes the formerly generated Link without deleting unrelated resources

#### Scenario: Generated resource drifts

- **WHEN** a user mutates a compiler-owned Node, Link, or NodeProfile away from compiled intent
- **THEN** the Topology controller restores the generated resource

#### Scenario: Detect an occupied generated child name

- **WHEN** a desired Node, Link, or NodeProfile name is already occupied in the Topology namespace by an unrelated resource
- **THEN** the Topology controller creates or updates none of the desired child resources and reports every conflict in Topology status

#### Scenario: Permit existing children of the same Topology

- **WHEN** a desired child resource already exists and is recognized as generated for the current Topology
- **THEN** the Topology controller treats it as available for normal drift reconciliation rather than reporting a conflict

#### Scenario: Reconcile after conflicts clear

- **WHEN** all previously conflicting resources are removed or the Topology definition is changed so that its desired child names are free
- **THEN** the Topology controller clears the conflict error and reconciles the complete desired child set

### Requirement: Dependencies are made available before Node realization

The compiler SHALL reconcile referenced NodeProfiles and Links before creating or updating Nodes that depend on them, so initial device plans include accepted interfaces. Node and Link controllers MUST nevertheless handle transiently unresolved references through status and later reconciliation.

#### Scenario: Create a new compiled lab

- **WHEN** the compiler emits resources for a new Topology
- **THEN** NodeProfiles and Links are submitted before the Nodes that reference or consume them

#### Scenario: API events are observed out of order

- **WHEN** a Node controller observes a generated Node before its NodeProfile or Links are readable
- **THEN** it reports unresolved dependencies and realizes the Node after their events arrive

### Requirement: Direct manifest generation matches in-cluster compilation

The command-line conversion path SHALL use the same compile, validation, and planning behavior as the Topology controller when emitting direct Node, Link, and NodeProfile manifests, except for in-cluster owner references and status.

#### Scenario: Emit primitive manifests

- **WHEN** a user converts a supported source topology to direct custom resources
- **THEN** the resulting specs produce normalized plans equivalent to those emitted by an in-cluster Topology

### Requirement: Topology status remains bounded

Topology status SHALL contain aggregate counts, lifecycle state, conditions, and an optional bounded controller-owned error string. It MUST NOT embed all generated resource specs, statuses, or an unbounded per-child conflict structure.

#### Scenario: Increase compiled topology size

- **WHEN** the compiler emits additional Nodes and Links
- **THEN** Topology status grows only by changes to fixed aggregate fields rather than one entry per child

#### Scenario: Report duplicate child resources

- **WHEN** one or more desired Node, Link, or NodeProfile names conflict with unrelated resources in the Topology namespace
- **THEN** `status.error` contains the namespace, a deterministic sorted `type/name` list, and the guidance to create the Topology in a different namespace or disambiguate node names

#### Scenario: Clear resolved duplicate-resource status

- **WHEN** a later reconcile finds no child-resource conflicts
- **THEN** `status.error` is empty and normal aggregate status reconciliation resumes

### Requirement: Large labs can bypass the aggregate source object

The system SHALL document and support direct application of generated primitive manifests for labs whose source definition would exceed the acceptable Topology object size.

#### Scenario: Deploy a large generated lab

- **WHEN** a user applies independently generated Node, Link, and NodeProfile manifests without a Topology
- **THEN** no persisted Clabernetes object contains the entire lab definition

### Requirement: A source definition accepts native Containerlab vocabulary

The compiler SHALL accept native Containerlab vocabulary from the declared compatibility baseline only when it can preserve that vocabulary through direct resources and device plans. It MUST reject unrecognized or unrepresentable fields with deterministic structured diagnostics before rendering resources; it MUST NOT omit such fields under a compatibility warning mode.

Malformed input, a recognized field with an unusable value, an unsupported explicit link type, or a structure that cannot identify realizable direct resources SHALL also fail. Explicit `veth` links SHALL accept brief `node:interface` endpoints or structured node/interface mappings when both identify the same representable endpoints.

#### Scenario: Compile a definition carrying unimplemented vocabulary

- **WHEN** a Topology definition declares baseline Containerlab vocabulary the direct planner does not implement
- **THEN** compilation fails before resource creation with diagnostics naming every unsupported field and location

#### Scenario: Recognized vocabulary survives alongside unrecognized fields

- **WHEN** a source Node uses supported baseline vocabulary without unrepresentable fields
- **THEN** every field contributes its defined semantics to the emitted resources or device plan

#### Scenario: Direct manifest generation reports the same omissions

- **WHEN** direct manifest generation runs against a definition carrying unimplemented vocabulary
- **THEN** it fails with the same sorted diagnostics before the user applies anything

#### Scenario: Strict caller rejects lossy compatibility

- **WHEN** any caller compiles a definition containing unsupported fields, management settings, host-side port pinning, unusable labels, or Link metadata c9s cannot preserve
- **THEN** compilation fails with sorted diagnostics naming every unsupported location

#### Scenario: Compile an explicit veth link with structured endpoints

- **WHEN** a source definition declares an explicit `veth` Link whose endpoints are Node/interface mappings
- **THEN** the compiler emits the same c9s Link as the equivalent brief `node:interface` endpoint syntax

#### Scenario: Compile an explicit veth link with brief endpoints

- **WHEN** a source definition declares an explicit `veth` Link whose endpoints are non-empty brief strings
- **THEN** the compiler emits the same c9s Link as the equivalent structured endpoint syntax

#### Scenario: Reject malformed veth endpoints

- **WHEN** an explicit `veth` Link contains an empty endpoint, an endpoint with an empty Node or interface, or an unsupported YAML shape
- **THEN** parsing or compilation fails before a Link resource is emitted

#### Scenario: Structurally impossible link fails in compatibility mode

- **WHEN** a definition references an unsupported external bridge, `mgmt-net`, macvlan endpoint, nonexistent Node, or unsupported explicit Link type
- **THEN** compilation fails instead of creating partially working resources

#### Scenario: Reject a recognized field holding an unusable value

- **WHEN** a definition declares a recognized field whose value is of the wrong shape
- **THEN** compilation fails rather than omitting the field

#### Scenario: Reject a definition with no topology section

- **WHEN** a definition parses as YAML but declares no topology section
- **THEN** compilation fails with a parse error rather than crashing the controller

### Requirement: Containerlab node labels become Kubernetes labels

The compiler SHALL carry Containerlab Node labels onto the emitted Node's object metadata, inheriting them from topology defaults and kinds the same way Node environment variables are inherited. The Node controller SHALL propagate a Node's labels to its direct workload and Pods, excluding labels in the reserved `c9s.run/` namespace and controller-owned label keys, without altering the workload's Pod selector.

A label Kubernetes would reject, a reserved label, or a controller-owned key MUST be rejected under the direct runtime's no-semantic-loss compilation contract unless it is a recognized source directive. The `c9s.run/exposePorts` directive SHALL be consumed into `spec.ports` and SHALL NOT be copied to object metadata.

#### Scenario: Label a lab node

- **WHEN** a source topology Node declares a valid Containerlab label
- **THEN** the emitted Node and direct workload carry it so the Pod can be selected by it

#### Scenario: Labels inherit from defaults and kinds

- **WHEN** labels are declared at topology defaults, kind, and Node level
- **THEN** the emitted Node carries the merged set with the most specific value winning

#### Scenario: Omit a label Kubernetes cannot accept

- **WHEN** a source topology Node declares a label whose key or value is invalid as a Kubernetes label
- **THEN** compilation fails with a diagnostic naming it instead of silently dropping metadata

#### Scenario: Omit a label in the reserved namespace

- **WHEN** a source topology Node declares a label in the `c9s.run/` namespace other than a recognized source directive
- **THEN** compilation fails because user input cannot set controller behavior implicitly

#### Scenario: Declare c9s-only service ports without publishing Docker host ports

- **WHEN** a source topology Node declares `c9s.run/exposePorts: "9273/tcp,8125/udp"`
- **THEN** the emitted Node carries both entries in `spec.ports`, the directive is absent from metadata, and equivalent ordinary entries are not duplicated

#### Scenario: Reject an invalid c9s expose ports directive

- **WHEN** the directive contains an empty or malformed entry
- **THEN** compilation fails with a diagnostic naming the Node, label, and invalid entry

#### Scenario: Inherit c9s-only service ports

- **WHEN** `c9s.run/exposePorts` is declared on topology defaults or a kind
- **THEN** every effective Node receives its canonical ports subject to normal label override semantics and no emitted Node carries the directive in metadata

#### Scenario: Preserve exposure policy

- **WHEN** a valid directive is compiled for a Topology whose effective NodeProfile disables exposure or auto-exposure
- **THEN** the directive contributes only Node port intent and the profile continues to control whether a Service and automatic ports are realized

#### Scenario: Omit a controller-owned label key

- **WHEN** a source topology Node declares a key such as `app.kubernetes.io/name` that c9s owns for identity or selection
- **THEN** compilation fails rather than allowing it to overwrite a controller invariant

### Requirement: Topology status updates tolerate resource-version conflicts

The Topology controller SHALL retry aggregate status writes after a resource-version conflict and SHALL
avoid issuing an update when the current status already equals the desired status.

#### Scenario: Topology status races with a spec update

- **WHEN** a Topology status write receives a resource-version conflict
- **THEN** the controller refetches the current Topology and retries without failing the reconcile solely because of the conflict

### Requirement: Topology projects device Pod affinity into generated profiles

`Topology.spec.deployment.scheduling.affinity` SHALL accept the native Kubernetes `Affinity`
structure and SHALL be copied to every generated NodeProfile required by the Topology. The
Topology controller and the direct CR-manifest generation path MUST preserve the affinity structure
without placing it on Nodes or Links.

#### Scenario: Apply topology-wide affinity

- **WHEN** a Topology configures node affinity, pod affinity, or pod anti-affinity under
  `spec.deployment.scheduling`
- **THEN** its generated shared NodeProfile contains the same affinity structure and every
  generated Node references that profile

#### Scenario: Preserve affinity-only scheduling

- **WHEN** a Topology configures affinity but omits `nodeSelector` and `tolerations`
- **THEN** the generated NodeProfile still contains the scheduling block and its affinity

#### Scenario: Preserve affinity on dedicated profiles

- **WHEN** a Topology emits a dedicated NodeProfile for a Node with distinct resource policy
- **THEN** that dedicated profile retains the Topology-wide affinity from the shared device Pod policy

#### Scenario: Restore drifted generated affinity

- **WHEN** a generated NodeProfile's affinity differs from the Topology's declared affinity
- **THEN** the Topology controller restores the generated profile to the declared affinity

#### Scenario: Emit equivalent direct manifests

- **WHEN** `clabverter --emit-crs` processes a Topology definition with device Pod affinity
- **THEN** the emitted NodeProfile manifest contains the same affinity structure as in-cluster
  Topology compilation
