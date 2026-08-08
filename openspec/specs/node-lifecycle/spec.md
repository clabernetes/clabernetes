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

The Node spec SHALL contain a flattened Containerlab node definition, including node kind, image, device configuration, and per-device management addresses. Emitters MUST expand source topology defaults and kinds before creating a Node, and wiring MUST NOT be embedded in Node spec.

#### Scenario: Materialize a Node definition

- **WHEN** the launcher reads a Node containing a valid flattened Containerlab definition
- **THEN** it renders the equivalent Containerlab node entry without needing topology-level defaults or kinds

#### Scenario: Wiring is changed independently

- **WHEN** a Link terminating on a Node is added, changed, or removed
- **THEN** the Node spec remains unchanged

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

### Requirement: Individual Node object size is independent of topology size

A Node resource SHALL contain only its own definition, payload references, launcher profile reference, and status. The system MUST NOT copy all topology Nodes, Links, or aggregate child statuses into a Node.

#### Scenario: Add unrelated Nodes and Links

- **WHEN** additional Nodes and Links are added to the same lab
- **THEN** the serialized size of an existing unchanged Node does not grow
