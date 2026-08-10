## ADDED Requirements

### Requirement: A source definition accepts native Containerlab vocabulary

The compiler SHALL accept a Containerlab definition that carries node vocabulary clabernetes does not implement, so an existing working Containerlab topology can be used unchanged. Fields the compiler does not recognize MUST be omitted from the emitted resources, and each one MUST be reported as a warning naming the field and its location in the definition. Unrecognized vocabulary MUST NOT fail compilation.

A definition that is malformed, or that declares a recognized field with an unusable value, SHALL still fail compilation rather than have that field silently omitted.

#### Scenario: Compile a definition carrying unimplemented vocabulary

- **WHEN** a Topology definition declares Containerlab node fields clabernetes does not implement
- **THEN** compilation succeeds, the emitted Nodes omit those fields, and each omitted field is reported as a warning naming it and where it appears

#### Scenario: Recognized vocabulary survives alongside unrecognized fields

- **WHEN** a source node mixes unrecognized fields with recognized ones
- **THEN** every recognized field is compiled into the emitted Node

#### Scenario: Direct manifest generation reports the same omissions

- **WHEN** direct manifest generation runs against a definition carrying unimplemented vocabulary
- **THEN** it reports the same omitted fields before the user applies anything

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
