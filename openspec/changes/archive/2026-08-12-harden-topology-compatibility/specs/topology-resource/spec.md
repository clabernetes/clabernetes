## MODIFIED Requirements

### Requirement: A source definition accepts native Containerlab vocabulary

The compiler SHALL accept a Containerlab definition that carries node vocabulary c9s does not implement, so an existing working Containerlab topology can be used unchanged. Fields the compiler does not recognize MUST be omitted from the emitted resources, and each one MUST be reported as a warning naming the field and its location in the definition. Unrecognized vocabulary MUST NOT fail compilation.

The compiler SHALL expose an unsupported-field policy. The compatibility Topology controller uses
the warning policy above. Strict callers MAY select an error policy, in which case all otherwise
lossy warnings are collected into deterministic structured diagnostics and compilation fails before
resources are rendered. This allows a CLI runtime to share the compiler's capability matrix without
silently changing topology semantics.

A definition that is malformed, or that declares a recognized field with an unusable value, SHALL
still fail compilation rather than have that field silently omitted. Structures that cannot
identify realizable c9s resources, including external bridge/host pseudo-nodes, unresolved
endpoint Nodes, `mgmt-net` or macvlan endpoints, and unsupported explicit link types, SHALL fail
under every policy.

#### Scenario: Compile a definition carrying unimplemented vocabulary

- **WHEN** a Topology definition declares Containerlab node fields c9s does not implement
- **THEN** compilation succeeds under the compatibility policy, emitted Nodes omit those fields, and each omitted field is reported as a warning naming it and where it appears

#### Scenario: Strict caller rejects lossy compatibility

- **WHEN** a strict caller compiles a definition containing unsupported fields, native management network settings, host-side port pinning, unusable labels, or link labels and vars that c9s cannot preserve
- **THEN** compilation fails with deterministically sorted diagnostics naming every unsupported location

#### Scenario: Structurally impossible input fails in compatibility mode

- **WHEN** a definition references an external bridge, `mgmt-net`, macvlan, a nonexistent Node, an unsupported explicit link type, or an invalid launcher-group network mode
- **THEN** compilation fails before generated resources are rendered

## ADDED Requirements

### Requirement: Topology status updates tolerate resource-version conflicts

The Topology controller SHALL retry aggregate status writes after a resource-version conflict and SHALL
avoid issuing an update when the current status already equals the desired status.

#### Scenario: Topology status races with a spec update

- **WHEN** a Topology status write receives a resource-version conflict
- **THEN** the controller refetches the current Topology and retries without failing the reconcile solely because of the conflict
