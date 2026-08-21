## MODIFIED Requirements

### Requirement: Link represents one point-to-point wire

The system SHALL represent each point-to-point connection as one namespaced Link containing exactly two endpoints. Each endpoint SHALL identify a Node by metadata name in the Link namespace and an interface name, except for the reserved local `host` endpoint.

#### Scenario: Connect two Nodes

- **WHEN** a user creates a Link whose two endpoints identify existing Nodes and unused interfaces
- **THEN** the Link controller accepts the wire for direct realization in both endpoint namespaces

#### Scenario: Create a host Link

- **WHEN** one Link endpoint uses the reserved `host` node name
- **THEN** the connectivity runtime owning the other endpoint materializes the host-local connection without resolving a Node named `host`

### Requirement: Link endpoints are validated deterministically

The Link controller SHALL reject unresolved endpoints, invalid self-connections, and duplicate use of a Node interface. When multiple Links conflict, the controller MUST choose the valid winner deterministically and report an error on every loser.

#### Scenario: Referenced Node does not exist

- **WHEN** a Link endpoint references a Node that does not exist in the Link namespace
- **THEN** the controller reports the unresolved endpoint and no direct connectivity component materializes the Link

#### Scenario: Two Links claim one interface

- **WHEN** two Links claim the same interface on the same Node
- **THEN** the controller deterministically marks one Link valid and reports an interface conflict on the other

#### Scenario: Invalid Link becomes valid

- **WHEN** the missing endpoint or conflicting Link is corrected or removed
- **THEN** the controller revalidates the affected Link and makes it eligible for realization

### Requirement: Link owns connectivity flavor

Each Link SHALL contain the authoritative connectivity flavor used by both endpoints. Omitted cross-Pod connectivity SHALL default to `vxlan`; supported values SHALL cover VXLAN, slurpeeth, same-Pod, loopback, and host realization where applicable. LauncherProfile and Node MUST NOT independently override a Link's connectivity.

#### Scenario: Connectivity is omitted

- **WHEN** a cross-Pod Link omits connectivity
- **THEN** both endpoints realize it using VXLAN

#### Scenario: Slurpeeth is selected

- **WHEN** a cross-Pod Link selects `slurpeeth`
- **THEN** both endpoints realize the Link using the same slurpeeth segment

#### Scenario: Connectivity changes

- **WHEN** a valid Link changes from one supported connectivity flavor to another
- **THEN** both endpoints remove the obsolete realization and converge on the new flavor

### Requirement: Tunnel allocation belongs to Link status

For a valid Link whose selected direct realization needs a shared tunnel or segment identifier, the Link controller SHALL allocate it and store it in Link status. Users MUST NOT supply controller-owned allocation values in Link spec.

#### Scenario: Cross-launcher Link needs an allocation

- **WHEN** a valid Link terminates on Nodes realized by different Pods and its flavor needs a shared identifier
- **THEN** the controller allocates one identifier that both endpoint reconcilers consume

#### Scenario: Link is local to one launcher

- **WHEN** both endpoints share one Pod or one endpoint is `host`
- **THEN** the Link is materialized locally without an unnecessary tunnel allocation

## ADDED Requirements

### Requirement: Connectivity reconcilers observe only owned Links

The API SHALL expose selectable endpoint Node fields, and each connectivity reconciler SHALL list or watch only Links terminating on Nodes and Pods within its responsibility. A Link change MUST enqueue reconcilers for its old and new endpoints.

#### Scenario: Unrelated Link changes

- **WHEN** a Link changes without terminating on any Node owned by a reconciler
- **THEN** that reconciler does not restart or reconfigure because of the change

#### Scenario: Link endpoint is rewired

- **WHEN** a Link endpoint moves from one Node to another
- **THEN** reconcilers for both the former and new endpoint clean up or realize their owned state

## REMOVED Requirements

### Requirement: Launchers observe only terminating Links

**Reason**: The nested launcher is removed and no longer owns connectivity.

**Migration**: Direct connectivity reconcilers use the same endpoint indexes with Pod, Node, and Link UID ownership.
