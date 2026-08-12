# node-lifecycle Specification

## Purpose

Define independently reconcilable network Nodes, their self-contained intent and payloads, launcher policy references, grouping, and bounded status.

## Requirements

### Requirement: Node is an independently reconcilable network node

The system SHALL represent each Containerlab network node as one namespaced Node resource whose metadata name is the Containerlab node name. Creating a valid Node SHALL be sufficient to request realization without a Topology resource.

#### Scenario: Create a standalone Node directly

- **WHEN** a user creates a valid Node without a Topology owner
- **THEN** the Node controller reconciles the launcher resources for that Node

#### Scenario: Delete one Node independently

- **WHEN** a user deletes one Node from a namespace containing other Nodes
- **THEN** the controller removes only resources owned by that Node and reconciles affected grouped nodes and Links without deleting unrelated Nodes

### Requirement: Node spec is self-contained Containerlab node intent

The Node spec SHALL contain a flattened Containerlab node definition drawn from a curated subset of Containerlab node vocabulary, including node kind, image, device configuration, and per-device management addresses. Emitters MUST expand source topology defaults and kinds before creating a Node, and wiring MUST NOT be embedded in Node spec.

#### Scenario: Materialize a Node definition

- **WHEN** the launcher reads a Node containing a valid flattened Containerlab definition
- **THEN** it renders the equivalent Containerlab node entry without needing topology-level defaults or kinds

#### Scenario: Wiring is changed independently

- **WHEN** a Link terminating on a Node is added, changed, or removed
- **THEN** the Node spec remains unchanged

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

A Node SHALL declare additional ports as destination ports with an optional protocol. The pod-side port carrying each destination port is an allocation owned by the controller and MUST NOT be user-specifiable. The schema MUST reject host-to-container mapping syntax, and port parsing MUST reject any form it cannot represent rather than reinterpreting it.

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

### Requirement: Node owns per-node payload attachments

The Node spec SHALL describe files and other payload required to instantiate that network node, including supported URL- and ConfigMap-backed sources. Launcher Pod policy MUST NOT be used merely to associate a payload attachment with one Node.

#### Scenario: Attach a ConfigMap-backed startup file

- **WHEN** a Node references a supported ConfigMap-backed payload file
- **THEN** the Node controller mounts that file into the launcher responsible for the Node

#### Scenario: Fetch a URL-backed payload file

- **WHEN** a Node references a supported URL-backed payload file
- **THEN** the launcher fetches the file before materializing the Containerlab node

### Requirement: LauncherProfile reference is optional and explicit

The Node spec SHALL expose an optional, same-namespace `launcherProfileRef`. The system MUST resolve at most one LauncherProfile for a Node and MUST NOT attach LauncherProfiles through labels, selectors, priorities, or implicit multi-profile merging.

#### Scenario: Node omits launcherProfileRef

- **WHEN** a Node has no `launcherProfileRef`
- **THEN** the controller realizes it using global Config defaults

#### Scenario: Node references an existing LauncherProfile

- **WHEN** a Node references a LauncherProfile in its namespace
- **THEN** the controller realizes it using that profile layered over global Config defaults

#### Scenario: Node references a missing LauncherProfile

- **WHEN** a Node names a LauncherProfile that does not exist
- **THEN** the controller does not create or update the launcher workload and reports `LauncherProfileResolved=False`

### Requirement: Grouped Nodes use one launcher policy

Nodes sharing a launcher through Containerlab `network-mode: container:<primary>` SHALL use the primary Node's effective LauncherProfile. A secondary Node MUST NOT select a conflicting LauncherProfile.

#### Scenario: Secondary omits a profile reference

- **WHEN** a secondary Node omits `launcherProfileRef` and its primary references a LauncherProfile
- **THEN** the secondary is realized in the primary launcher using the primary's effective profile

#### Scenario: Group members reference conflicting profiles

- **WHEN** a secondary Node explicitly references a different LauncherProfile than its primary
- **THEN** the controller reports the group as invalid and does not realize the inconsistent launcher workload

### Requirement: Node status contains only per-node observations and allocations

The controller SHALL record Node readiness, probe observations, exposed-port allocations, standard conditions, and applied LauncherProfile identity in Node status. User intent MUST NOT be stored in status.

#### Scenario: LauncherProfile is applied

- **WHEN** a Node is successfully realized with a referenced LauncherProfile
- **THEN** Node status records the applied profile name, UID, and generation

#### Scenario: Node readiness changes

- **WHEN** the launcher Pod or configured probes change readiness
- **THEN** the controller updates only the affected Node readiness and conditions

### Requirement: Enabled Node readiness reflects generic launcher state

When status probes are enabled for a non-excluded Node, the launcher SHALL report readiness only
when the represented nested Docker container is running and is not paused, restarting, or dead. If
the nested image defines a Docker healthcheck, that healthcheck SHALL also report `healthy`.

#### Scenario: Generic Node has no application-specific probe

- **WHEN** a Node uses an enabled status-probe configuration without TCP or SSH settings
- **THEN** its launcher Deployment renders startup and readiness probes and reports the generic
  nested-container readiness through the launcher status marker

#### Scenario: Running container without a healthcheck

- **WHEN** the represented nested container is running, not paused, not restarting, not dead, and
  has no Docker healthcheck
- **THEN** the Node reports ready under the generic readiness contract

#### Scenario: Nested container is not runnable

- **WHEN** the represented nested container is stopped, paused, restarting, or dead
- **THEN** the Node reports not ready

#### Scenario: Image healthcheck is not healthy

- **WHEN** the represented nested container is running but its Docker healthcheck is `starting` or
  `unhealthy`
- **THEN** the Node reports not ready

#### Scenario: Image healthcheck becomes healthy

- **WHEN** a running nested container's Docker healthcheck reports `healthy`
- **THEN** the generic readiness condition succeeds

#### Scenario: Explicit application probe fails

- **WHEN** the nested container satisfies the generic readiness contract but a configured TCP or
  SSH probe fails
- **THEN** the Node remains not ready

#### Scenario: Status probes are disabled or excluded

- **WHEN** status probes are disabled for a Node or the Node is listed in `excludedNodes`
- **THEN** the controller does not render launcher status probes for that Node

### Requirement: Individual Node object size is independent of topology size

A Node resource SHALL contain only its own definition, payload references, launcher profile reference, and status. The system MUST NOT copy all topology Nodes, Links, or aggregate child statuses into a Node.

#### Scenario: Add unrelated Nodes and Links

- **WHEN** additional Nodes and Links are added to the same lab
- **THEN** the serialized size of an existing unchanged Node does not grow
