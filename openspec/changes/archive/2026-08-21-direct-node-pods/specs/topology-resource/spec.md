## MODIFIED Requirements

### Requirement: Topology is an auxiliary high-level resource

Topology SHALL remain an optional aggregate source that compiles into the direct primitive resources. Node, Link, and LauncherProfile SHALL remain the authoritative runtime inputs after compilation.

#### Scenario: Define a lab with Topology

- **WHEN** a user creates a valid supported Topology
- **THEN** the Topology controller emits the primitive resources and plans needed to realize the direct lab

#### Scenario: Run without a Topology

- **WHEN** equivalent Node, Link, and LauncherProfile manifests are created directly
- **THEN** the same Node and Link controllers realize an equivalent direct lab

### Requirement: Compilation emits self-contained Nodes

The compiler SHALL parse the source definition and expand topology defaults, kinds, and other inherited node settings into every emitted Node. It SHALL attach per-node payload declarations to the corresponding Node and SHALL preserve every representable input required by the direct planner.

#### Scenario: Source node inherits defaults

- **WHEN** a source topology Node omits values supplied by topology defaults or a kind definition
- **THEN** its emitted Node contains the fully resolved values needed to produce the same device plan

#### Scenario: Source includes per-node files

- **WHEN** source processing associates payload files with one Node
- **THEN** the emitted Node contains those payload attachment declarations

### Requirement: Compilation emits explicit LauncherProfile references

The compiler SHALL convert topology-level Kubernetes realization policy into one or more LauncherProfiles and SHALL stamp each emitted Node with the appropriate `launcherProfileRef`. It MUST preserve supported direct management policy and MUST NOT emit Docker, launcher-image, nested-CRI, or containerlab-version policy.

#### Scenario: Nodes share topology policy

- **WHEN** all source Nodes use the same direct-workload policy
- **THEN** the compiler emits one shared LauncherProfile and references it from all emitted Nodes

#### Scenario: One Node has a launcher override

- **WHEN** one source Node has distinct resources or other direct-workload policy
- **THEN** the compiler emits a complete dedicated LauncherProfile for that Node and stamps its explicit reference

#### Scenario: Topology defines a custom management network

- **WHEN** an existing Topology defines management settings with supported direct semantics
- **THEN** the compiler preserves those settings in the generated resources that own them

### Requirement: Compilation puts connectivity on Links

The compiler SHALL translate topology connectivity policy and endpoint type into the connectivity field of every emitted Link. It MUST NOT place connectivity on LauncherProfile or require endpoint Nodes to resolve matching connectivity independently.

#### Scenario: Topology selects slurpeeth

- **WHEN** a source Topology selects slurpeeth connectivity
- **THEN** each emitted cross-Pod Link explicitly selects slurpeeth

#### Scenario: Topology omits connectivity

- **WHEN** a source Topology does not select a connectivity flavor
- **THEN** emitted Links select the direct-runtime default appropriate to their endpoint placement

### Requirement: Dependencies are made available before Node realization

The compiler SHALL reconcile referenced LauncherProfiles and Links before creating or updating Nodes that depend on them, so initial device plans include accepted interfaces. Node and Link controllers MUST nevertheless handle transiently unresolved references through status and later reconciliation.

#### Scenario: Create a new compiled lab

- **WHEN** the compiler emits resources for a new Topology
- **THEN** LauncherProfiles and Links are submitted before the Nodes that reference or consume them

#### Scenario: API events are observed out of order

- **WHEN** a Node controller observes a generated Node before its LauncherProfile or Links are readable
- **THEN** it reports unresolved dependencies and realizes the Node after their events arrive

### Requirement: Direct manifest generation matches in-cluster compilation

The command-line conversion path SHALL use the same compile, validation, and planning behavior as the Topology controller when emitting direct Node, Link, and LauncherProfile manifests, except for in-cluster owner references and status.

#### Scenario: Emit primitive manifests

- **WHEN** a user converts a supported source topology to direct custom resources
- **THEN** the resulting specs produce normalized plans equivalent to those emitted by an in-cluster Topology

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

- **WHEN** a valid directive is compiled for a Topology whose effective LauncherProfile disables exposure or auto-exposure
- **THEN** the directive contributes only Node port intent and the profile continues to control whether a Service and automatic ports are realized

#### Scenario: Omit a controller-owned label key

- **WHEN** a source topology Node declares a key such as `app.kubernetes.io/name` that c9s owns for identity or selection
- **THEN** compilation fails rather than allowing it to overwrite a controller invariant
