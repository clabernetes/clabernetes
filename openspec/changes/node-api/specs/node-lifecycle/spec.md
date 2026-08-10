## MODIFIED Requirements

### Requirement: Node spec is self-contained Containerlab node intent

The Node spec SHALL contain a flattened Containerlab node definition drawn from a curated subset of Containerlab node vocabulary, including node kind, image, device configuration, and per-device management addresses. Emitters MUST expand source topology defaults and kinds before creating a Node, and wiring MUST NOT be embedded in Node spec.

#### Scenario: Materialize a Node definition

- **WHEN** the launcher reads a Node containing a valid flattened Containerlab definition
- **THEN** it renders the equivalent Containerlab node entry without needing topology-level defaults or kinds

#### Scenario: Wiring is changed independently

- **WHEN** a Link terminating on a Node is added, changed, or removed
- **THEN** the Node spec remains unchanged

## ADDED Requirements

### Requirement: Node vocabulary excludes fields a launcher cannot realize

The Node spec SHALL NOT expose Containerlab node fields that the launcher cannot realize for a single node in one launcher Pod. Excluded fields are those absent from the launcher's Containerlab version, those whose meaning spans several nodes of one Containerlab lab, and those describing Pod-level policy owned by LauncherProfile or the Pod spec.

#### Scenario: Field owned by launcher policy is absent from Node

- **WHEN** a user inspects the Node schema for container resource limits, image pull policy, or container healthchecks
- **THEN** those fields are absent from the Node spec and available on LauncherProfile instead

#### Scenario: Labels live in Node metadata, not in the spec

- **WHEN** a user inspects the Node schema for Containerlab node labels
- **THEN** no such spec field exists, because Kubernetes object metadata is where labels belong and only those are selectable

#### Scenario: Inter-node deployment ordering is absent from Node

- **WHEN** a user inspects the Node schema for Containerlab deployment stages or startup ordering against other nodes
- **THEN** those fields are absent, because each launcher Pod runs its own single-node lab and ordering across Pods is a Kubernetes concern

#### Scenario: Container escape hatch is available

- **WHEN** a Node declares supported container options such as devices, added capabilities, shared memory size, or privileged execution
- **THEN** the launcher renders them into the Containerlab node entry

#### Scenario: Certificate SANs are reachable

- **WHEN** a Node requests an issued certificate with subject alternative names
- **THEN** the names are declared on the Node's certificate configuration and rendered into the Containerlab node entry

### Requirement: Node declares destination ports, not host mappings

A Node SHALL declare additional ports to expose as a destination port with an optional protocol. The pod-side port carrying each destination port is an allocation owned by the controller and MUST NOT be user-specifiable. The schema MUST reject host-to-container mapping syntax, and port parsing MUST reject any form it cannot represent rather than reinterpreting it.

#### Scenario: Declare an additional port

- **WHEN** a Node lists a destination port with an optional protocol
- **THEN** the controller allocates a pod-side port, records the pair in status, programs the expose Service, and the launcher publishes the mapping in the rendered topology

#### Scenario: Reject a host-to-container mapping

- **WHEN** a user applies a Node listing a port as a host-to-container mapping
- **THEN** the API server rejects the manifest

#### Scenario: Reject an unrepresentable port entry

- **WHEN** a port entry specifies a host IP binding, a port range, or an unsupported protocol
- **THEN** the entry is reported as invalid instead of being parsed into a different port or protocol

#### Scenario: Compile a source topology using host mappings

- **WHEN** a Topology definition declares node ports as host-to-container mappings
- **THEN** the compiler emits Nodes carrying only the destination ports

### Requirement: Unsupported Node fields are rejected

The Node schema SHALL reject unknown and removed field names rather than accepting and ignoring them. The API MUST NOT preserve arbitrary unrecognized keys in the Node spec, except where a field's value is defined as arbitrary user data.

#### Scenario: Apply a Node using a removed field

- **WHEN** a user applies a Node manifest containing a field removed from the Node vocabulary
- **THEN** the API server rejects the manifest and names the offending field

#### Scenario: Arbitrary user data is preserved

- **WHEN** a Node declares Containerlab config engine variables with nested arbitrary values
- **THEN** those values are stored unchanged and rendered into the Containerlab node entry

### Requirement: Node vocabulary is parseable by the launcher's Containerlab

Every field in the Node containerlab vocabulary SHALL exist in the Containerlab version installed in the launcher image. The repository MUST verify this relationship automatically so that a Containerlab version change or a vocabulary addition cannot silently produce topologies the launcher refuses to parse.

#### Scenario: Vocabulary gains a field the launcher cannot parse

- **WHEN** a field absent from the launcher's Containerlab node definition is added to the Node vocabulary
- **THEN** the verification fails before release

#### Scenario: Rendered topology deploys

- **WHEN** a Node populating every supported vocabulary field is materialized by the launcher
- **THEN** Containerlab parses the rendered topology without unknown-field errors

### Requirement: Node grouping is declared only by container network mode

Grouping Nodes into one launcher Pod SHALL be declared exclusively by Containerlab `network-mode: container:<primary>`. The schema MUST reject other network mode values.

#### Scenario: Group a secondary Node onto its primary

- **WHEN** a Node sets `network-mode` to `container:<primary>` naming another Node in its namespace
- **THEN** the controller realizes it inside the primary Node's launcher Pod

#### Scenario: Reject an unrealizable network mode

- **WHEN** a user applies a Node whose `network-mode` is `host` or any value other than `container:<primary>`
- **THEN** the API server rejects the manifest
