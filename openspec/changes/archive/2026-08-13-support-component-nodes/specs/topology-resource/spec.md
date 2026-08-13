## MODIFIED Requirements

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
