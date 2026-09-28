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

### Requirement: LoadBalancer expose Services can request the realized management address

When a Node's effective exposure policy enables `useNodeMgmtIpv4Address` or
`useNodeMgmtIpv6Address` and renders a `LoadBalancer` expose Service, the system SHALL request the
Node's management address of that family from the device plan applied to the Node, without its
prefix length, as the Service's LoadBalancer address. The requested address SHALL be the address
reported in the Node's `status.directManagement` for that family, whether the Node pinned it with
`mgmt-ipv4` or `mgmt-ipv6` or c9s allocated it.

The system SHALL select the IPv4 family when `useNodeMgmtIpv4Address` is enabled, and otherwise
the IPv6 family when `useNodeMgmtIpv6Address` is enabled. When the selected family has no realized
address, the Service MUST NOT request a LoadBalancer address. Expose types other than
`LoadBalancer` MUST NOT request one.

#### Scenario: Request an allocated IPv4 address

- **WHEN** a Node without `mgmt-ipv4` has an effective policy with `exposeType: LoadBalancer` and
  `useNodeMgmtIpv4Address: true`, and its applied plan realizes management address
  `10.20.30.57/24`
- **THEN** its expose Service requests LoadBalancer address `10.20.30.57`

#### Scenario: Request a pinned IPv4 address

- **WHEN** a Node pins `mgmt-ipv4: 10.20.30.11` and its effective policy enables
  `useNodeMgmtIpv4Address`
- **THEN** its expose Service requests LoadBalancer address `10.20.30.11`

#### Scenario: Request an allocated IPv6 address

- **WHEN** a Node's effective policy enables only `useNodeMgmtIpv6Address` and its applied plan
  realizes an IPv6 management address
- **THEN** its expose Service requests that IPv6 address as its LoadBalancer address

#### Scenario: IPv4 takes precedence

- **WHEN** a Node's effective policy enables both `useNodeMgmtIpv4Address` and
  `useNodeMgmtIpv6Address` and its applied plan realizes addresses of both families
- **THEN** its expose Service requests the IPv4 address

#### Scenario: Management is disabled

- **WHEN** a Node's effective policy enables `useNodeMgmtIpv4Address` and management allocation is
  disabled for the Node
- **THEN** its expose Service requests no LoadBalancer address

#### Scenario: Non-LoadBalancer exposure ignores the option

- **WHEN** a Node's effective policy enables `useNodeMgmtIpv4Address` with `exposeType: ClusterIP`
- **THEN** its expose Service requests no LoadBalancer address

#### Scenario: Follow a changed management address

- **WHEN** the address realized by a Node's applied plan changes while the option is enabled
- **THEN** reconciliation updates the expose Service to request the new address

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

### Requirement: Expose Service ports carry application-protocol hints

For every selected Service port matching a member of the default management-port set by
destination port and transport, the system SHALL set `Service.spec.ports[].appProtocol` to the
following value. The mapping SHALL apply regardless of whether the selected port originated from
automatic exposure, an explicit Node declaration, or imported image metadata.

| Destination port | Transport | `appProtocol` |
| ---: | --- | --- |
| 21 | TCP | `ftp` |
| 22 | TCP | `ssh` |
| 23 | TCP | `telnet` |
| 80 | TCP | `http` |
| 161 | UDP | `snmp` |
| 443 | TCP | `https` |
| 830 | TCP | `netconf-ssh` |
| 5000 | TCP | `telnet` |
| 5900 | TCP | `rfb` |
| 6030 | TCP | `c9s.run/gnmi` |
| 9339 | TCP | `c9s.run/gnmi` |
| 9340 | TCP | `c9s.run/gribi` |
| 9559 | TCP | `c9s.run/p4runtime` |
| 57400 | TCP | `c9s.run/gnmi` |

The system MUST use unprefixed names only for the corresponding IANA service, MUST use the
Kubernetes-defined `kubernetes.io/h2c` value only when explicitly selected for cleartext HTTP/2,
and MUST retain an implementation-defined prefix for the default device gRPC application hints.
Application-protocol hints MUST NOT change port selection, transport, routing, TLS termination, or
device traffic.

#### Scenario: Render the standard protocol defaults

- **WHEN** an expose Service contains ports from the default management-port set
- **THEN** each matching Service port carries the exact `appProtocol` value in the default mapping

#### Scenario: Preserve a hint when an explicit source claims a default port

- **WHEN** a Node declaration or imported image port claims the same destination and transport as
  a default management port
- **THEN** the resulting single Service port retains the mapped default `appProtocol`

#### Scenario: Identify the QEMU console by its actual protocol

- **WHEN** the expose Service contains the vrnetlab QEMU console on port 5000/TCP
- **THEN** its `appProtocol` is `telnet` rather than the IANA service assigned to port 5000

#### Scenario: Identify SSH File Transfer traffic as SSH

- **WHEN** the expose Service contains port 22/TCP
- **THEN** its `appProtocol` is `ssh` and no separate SFTP application protocol is asserted

#### Scenario: Leave an unknown port unspecified

- **WHEN** a selected Service port has no built-in mapping and no Node override
- **THEN** the Service port omits `appProtocol`

### Requirement: Node application-protocol intent overrides defaults

The system SHALL resolve a Node's application-protocol entry for a selected destination port and
transport after merging all port sources. A non-empty entry SHALL replace the built-in value, while
an explicitly empty entry SHALL suppress `appProtocol` for that Service port. An entry for a port
that is not selected SHALL have no effect and MUST NOT cause that port to be exposed.

#### Scenario: Mark cleartext gRPC

- **WHEN** a selected gNMI port has a Node override of `kubernetes.io/h2c`
- **THEN** the rendered Service port uses `kubernetes.io/h2c` instead of `c9s.run/gnmi`

#### Scenario: Mark terminable TLS

- **WHEN** a selected gRPC port has a Node override of `https`
- **THEN** the rendered Service port uses `https`

#### Scenario: Suppress a built-in hint

- **WHEN** a selected default port has an explicitly empty Node override
- **THEN** the rendered Service port omits `appProtocol`

#### Scenario: Override does not select a port

- **WHEN** a Node has an application-protocol entry for a destination and transport that is not
  otherwise selected for exposure
- **THEN** no Service port is added for that entry

### Requirement: Application-protocol drift is reconciled

The system SHALL treat each rendered Service port's `appProtocol` as controller-owned desired
state. It MUST update an owned expose Service when the current hint differs from the resolved hint,
including adding, changing, or removing the field, without discarding Kubernetes-assigned Service
fields that are preserved during ordinary Service updates.

#### Scenario: Add hints to an existing Service

- **WHEN** an owned expose Service created before this capability lacks the desired application-
  protocol hints
- **THEN** reconciliation updates its matching Service ports with those hints

#### Scenario: Apply a changed override

- **WHEN** a Node changes a port override from `c9s.run/gnmi` to `kubernetes.io/h2c`
- **THEN** reconciliation changes the matching Service port's `appProtocol` and preserves its
  Kubernetes-assigned NodePort when applicable
