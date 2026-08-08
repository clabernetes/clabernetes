# link-lifecycle Specification

## Purpose

Define independently managed point-to-point Links, including endpoint validation, lifecycle ownership, connectivity, allocation, and launcher observation.

## Requirements

### Requirement: Link represents one point-to-point wire

The system SHALL represent each point-to-point connection as one namespaced Link containing exactly two endpoints. Each endpoint SHALL identify a Node by metadata name in the Link namespace and an interface name, except for the reserved local `host` endpoint.

#### Scenario: Connect two Nodes

- **WHEN** a user creates a Link whose two endpoints identify existing Nodes and unused interfaces
- **THEN** the Link controller accepts the wire for realization by both endpoint launchers

#### Scenario: Create a host Link

- **WHEN** one Link endpoint uses the reserved `host` node name
- **THEN** the launcher owning the other endpoint materializes the host-local connection without resolving a Node named `host`

### Requirement: Link endpoints are validated deterministically

The Link controller SHALL reject unresolved endpoints, invalid self-connections, and duplicate use of a Node interface. When multiple Links conflict, the controller MUST choose the valid winner deterministically and report an error on every loser.

#### Scenario: Referenced Node does not exist

- **WHEN** a Link endpoint references a Node that does not exist in the Link namespace
- **THEN** the controller reports the unresolved endpoint and no launcher materializes the Link

#### Scenario: Two Links claim one interface

- **WHEN** two Links claim the same interface on the same Node
- **THEN** the controller deterministically marks one Link valid and reports an interface conflict on the other

#### Scenario: Invalid Link becomes valid

- **WHEN** the missing endpoint or conflicting Link is corrected or removed
- **THEN** the controller revalidates the affected Link and makes it eligible for realization

### Requirement: Link lifecycle follows resolved endpoint Nodes

The Link controller SHALL monitor the non-host Nodes named by each Link. After both Node endpoints have resolved, the controller MUST bind the Link to their UIDs and MUST delete the Link if either bound Node is deleted or replaced. A Link that has never resolved all of its Node endpoints MUST remain pending so resources can be created in any order. The controllers SHALL emit explicit lifecycle logs when a Node deletion is observed and when each associated Link is deleted.

#### Scenario: Referenced Node is deleted

- **WHEN** a Node bound to a resolved Link is deleted
- **THEN** the Link controller deletes every Link that references that Node
- **THEN** logs identify the deleted Node, each deleted Link, and the endpoint lifecycle reason

#### Scenario: Referenced Node is replaced

- **WHEN** a Node bound to a resolved Link is deleted and another Node is created with the same name but a different UID
- **THEN** the controller deletes the old Link instead of attaching it implicitly to the replacement Node

#### Scenario: Link is created before its Nodes

- **WHEN** a Link has never resolved all of its non-host Node endpoints
- **THEN** the controller retains the Link with an unresolved-endpoint status so later Node creation can make it valid

#### Scenario: Unrelated Node is deleted

- **WHEN** a Node that is not referenced by a Link is deleted
- **THEN** that Link remains unchanged

### Requirement: Link owns connectivity flavor

Each Link SHALL contain the authoritative connectivity flavor used by both endpoints. Omitted connectivity SHALL default to `vxlan`; supported explicit values SHALL initially include `vxlan` and `slurpeeth`. LauncherProfile and Node MUST NOT independently override a Link's connectivity.

#### Scenario: Connectivity is omitted

- **WHEN** a cross-launcher Link omits connectivity
- **THEN** both endpoint launchers realize it using VXLAN

#### Scenario: Slurpeeth is selected

- **WHEN** a cross-launcher Link selects `slurpeeth`
- **THEN** both endpoint launchers realize the Link using the same slurpeeth segment

#### Scenario: Connectivity changes

- **WHEN** a valid Link changes from one supported connectivity flavor to another
- **THEN** both endpoint launchers remove the obsolete realization and converge on the new flavor

### Requirement: Tunnel allocation belongs to Link status

For a valid cross-launcher Link, the Link controller SHALL allocate the shared tunnel or segment identifier and store it in Link status. Users MUST NOT supply controller-owned allocation values in Link spec.

#### Scenario: Cross-launcher Link needs an allocation

- **WHEN** a valid Link terminates on Nodes realized by different launcher Pods
- **THEN** the controller allocates one identifier that both endpoints consume

#### Scenario: Link is local to one launcher

- **WHEN** both endpoints are realized by the same launcher or one endpoint is `host`
- **THEN** the Link is materialized locally without a tunnel allocation

### Requirement: Launchers observe only terminating Links

The API SHALL expose selectable endpoint Node fields, and each launcher SHALL list or watch only Links terminating on Nodes in its launcher group. A Link change MUST enqueue the launchers for its old and new endpoints.

#### Scenario: Unrelated Link changes

- **WHEN** a Link changes without terminating on any Node in a launcher group
- **THEN** that launcher does not restart or reconfigure because of the change

#### Scenario: Link endpoint is rewired

- **WHEN** a Link endpoint moves from one Node to another
- **THEN** launchers for both the former and new endpoints reconcile their connectivity

### Requirement: Individual Link object size is constant with topology growth

A Link SHALL contain only its two endpoints, link-local options, and resource-local status. It MUST NOT embed Node definitions, all topology links, or aggregate child status.

#### Scenario: Grow the topology

- **WHEN** unrelated Nodes and Links are added
- **THEN** the serialized size of an existing unchanged Link does not grow
