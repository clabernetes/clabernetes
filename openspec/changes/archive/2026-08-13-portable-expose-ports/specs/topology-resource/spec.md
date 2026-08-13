## MODIFIED Requirements

### Requirement: Containerlab node labels become Kubernetes labels

The compiler SHALL carry Containerlab node labels onto the emitted Node's object metadata,
inheriting them from topology defaults and kinds the same way node environment variables are
inherited. The Node controller SHALL propagate a Node's labels to the launcher Deployment and its
Pods, excluding labels in the reserved `c9s.run/` namespace and controller-owned label keys,
without altering the Deployment's Pod selector.

A label that Kubernetes would reject, or that is in the reserved `c9s.run/` namespace or uses a
controller-owned label key, MUST be omitted with a warning naming it, so that a definition can
neither produce an unacceptable Node nor set labels the controllers act on. The sole recognized
source-directive exception is `c9s.run/exposePorts`: the compiler MUST consume its effective
node-label value into the emitted Node's `spec.ports` and MUST NOT copy the directive to object
metadata.

The `c9s.run/exposePorts` value MUST contain one or more comma-separated destination-port entries.
Each trimmed entry MUST use the established `port` or `port/{tcp,udp}` grammar, with TCP as the
default protocol. The compiler MUST canonicalize successful entries to numeric destination port
plus lowercase protocol and MUST deduplicate them semantically with ordinary topology ports and
other directive entries. Any empty or malformed entry MUST make compilation fail with a diagnostic
naming the node, directive, and invalid entry.

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

- **WHEN** a source topology node declares a label in the `c9s.run/` namespace other than a recognized source directive
- **THEN** it is omitted with a warning, because those labels carry meaning to the controllers

#### Scenario: Declare c9s-only service ports without publishing Docker host ports

- **WHEN** a source topology node declares `c9s.run/exposePorts: "9273/tcp,8125/udp"`
- **THEN** the emitted Node carries `9273/tcp` and `8125/udp` in `spec.ports`, the directive is absent from `metadata.labels`, and an equivalent ordinary port entry is not duplicated

#### Scenario: Inherit c9s-only service ports

- **WHEN** `c9s.run/exposePorts` is declared on topology defaults or a kind
- **THEN** every effective node inheriting that label receives its canonical ports, subject to normal node-label override semantics, and no emitted Node carries the directive in metadata

#### Scenario: Reject an invalid c9s expose ports directive

- **WHEN** a source topology node's `c9s.run/exposePorts` value contains an empty or malformed entry
- **THEN** compilation fails with a diagnostic naming the node, label, and invalid entry rather than silently omitting the requested Service port

#### Scenario: Preserve exposure policy

- **WHEN** a valid directive is compiled for a Topology whose effective LauncherProfile disables exposure or auto-exposure
- **THEN** the directive contributes only to Node port intent and the existing LauncherProfile policy continues to control whether a Service and automatic ports are realized

#### Scenario: Omit a controller-owned label key

- **WHEN** a source topology node declares a valid label using a key such as `app.kubernetes.io/name` that c9s uses for identity or selection
- **THEN** it is omitted with a warning, because user metadata must not overwrite controller invariants
