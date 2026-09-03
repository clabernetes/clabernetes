# service-exposure Specification

## Purpose

Define how c9s selects, suppresses, and documents Kubernetes Services that expose direct network
Nodes while keeping internal fabric and alias discovery separate from user-facing exposure policy.

## Requirements

### Requirement: Expose type is the single Service exposure mode

The system SHALL use `exposeType` as the only API field controlling the kind or absence of a
Node's expose Service. The accepted values SHALL be `LoadBalancer`, `ClusterIP`, `Headless`, and
`None`; the Topology and NodeProfile schemas MUST reject the removed `disableExpose` field.

#### Scenario: Disable exposure

- **WHEN** a Node's effective profile sets `exposeType: None`
- **THEN** the system allocates no exposed ports and creates no expose Service for that Node

#### Scenario: Reject the removed boolean

- **WHEN** a user applies a Topology or NodeProfile manifest containing `disableExpose`
- **THEN** the structural schema rejects the unknown field

### Requirement: Exposure modes render their declared Kubernetes Service form

The system SHALL render `LoadBalancer` as a Kubernetes `LoadBalancer` Service, `ClusterIP` as an
ordinary Kubernetes `ClusterIP` Service, and `Headless` as a Kubernetes `ClusterIP` Service whose
`clusterIP` is `None`. When no explicit NodeProfile exposure mode is available, the system SHALL
use the built-in `LoadBalancer` default; global Config SHALL NOT be presented as an exposure-mode
configuration source.

#### Scenario: Use the built-in default

- **WHEN** a directly authored Node has no `profileRef`
- **THEN** its non-empty exposed-port allocation is realized by a `LoadBalancer` expose Service

#### Scenario: Render a ClusterIP expose Service

- **WHEN** the effective NodeProfile sets `exposeType: ClusterIP` and the Node has exposed ports
- **THEN** the Node's expose Service has Kubernetes type `ClusterIP` with an allocated virtual IP

#### Scenario: Render a headless expose Service

- **WHEN** the effective NodeProfile sets `exposeType: Headless` and the Node has exposed ports
- **THEN** the Node's expose Service has Kubernetes type `ClusterIP` and `clusterIP: None`

#### Scenario: Render a LoadBalancer expose Service

- **WHEN** the effective NodeProfile sets `exposeType: LoadBalancer` and the Node has exposed ports
- **THEN** the Node's expose Service has Kubernetes type `LoadBalancer`

### Requirement: Service roles remain independent

Exposure policy SHALL control only the per-Node expose Service. Fabric Services required for c9s
connectivity and headless Services created for declared network aliases MUST remain governed by
their own reconciliation rules and MUST NOT be removed merely because `exposeType` is `None`.

#### Scenario: Disable expose Services without disabling fabric discovery

- **WHEN** a Node's effective profile sets `exposeType: None`
- **THEN** its required fabric Service is still reconciled

#### Scenario: Disable expose Services without disabling aliases

- **WHEN** a Node declares a network alias and its effective profile sets `exposeType: None`
- **THEN** the alias's headless Service is still reconciled

### Requirement: Expose Services require declared port allocations

The system SHALL create an expose Service only when exposure is enabled and the Node has at least
one resolved exposed port. `disableAutoExpose` SHALL remain an independent port-selection control
and MUST NOT select the Kubernetes Service type.

#### Scenario: Enabled mode has no exposed ports

- **WHEN** exposure uses a Service-producing mode but automatic exposure is disabled and no
  explicit or imported port is selected
- **THEN** the system creates no expose Service

#### Scenario: Disable only automatic ports

- **WHEN** `disableAutoExpose` is true and the Node declares an explicit supported port
- **THEN** the selected port is exposed using the effective `exposeType`

### Requirement: Exposure mode transitions converge safely

The system SHALL reconcile an existing expose Service to the effective exposure mode. It MUST
recreate the Service when moving between ordinary and headless ClusterIP allocation modes, and it
SHALL delete an owned expose Service when the mode changes to `None`.

#### Scenario: Change from ordinary ClusterIP to Headless

- **WHEN** an existing ordinary ClusterIP expose Service changes to `exposeType: Headless`
- **THEN** the system replaces the Service so Kubernetes can apply `clusterIP: None`

#### Scenario: Change exposure mode to None

- **WHEN** a Node with an owned expose Service changes to `exposeType: None`
- **THEN** the system deletes that expose Service without deleting its fabric or alias Services

### Requirement: User documentation describes the effective exposure contract

The user documentation SHALL describe all accepted exposure modes, the built-in default, the
Topology and direct NodeProfile configuration paths, the distinction between expose and internal
Service roles, and the breaking manifest migration from `disableExpose: true` to
`exposeType: None`.

#### Scenario: Configure direct resources from documentation

- **WHEN** a user follows the service-exposure guide for directly authored resources
- **THEN** the example places `exposeType` on a NodeProfile and references it through the Node's
  same-namespace `profileRef`

#### Scenario: Migrate a disabled manifest

- **WHEN** a user reads the upgrade guidance for a manifest containing `disableExpose: true`
- **THEN** the guidance instructs them to replace it with `exposeType: None` before upgrading
