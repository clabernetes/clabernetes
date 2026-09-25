## ADDED Requirements

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
