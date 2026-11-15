# Spec Delta

## ADDED Requirements

### Requirement: Link endpoints carry optional address prefixes
Each non-host Link endpoint SHALL optionally carry one IPv4 CIDR prefix and one IPv6 CIDR prefix alongside its Node and interface identity. The fields SHALL be additive, preserve host bits, and remain constant-size per Link.

#### Scenario: Direct dual-stack Link
- **WHEN** a user authors a Link with valid IPv4 and IPv6 prefixes on either or both non-host endpoints
- **THEN** the Link retains those prefixes for direct planning without changing Link or wire identity

#### Scenario: Unaddressed Links remain compatible
- **WHEN** an existing Link omits both endpoint address families
- **THEN** its meaning, acceptance, allocation, and lifecycle remain unchanged

### Requirement: Endpoint address prefixes are validated
The Link controller SHALL accept only syntactically valid CIDR prefixes of the declared address family on non-host endpoints. It SHALL reject host-endpoint addresses rather than discard intent that has no direct realization.

#### Scenario: Reject malformed or wrong-family prefixes
- **WHEN** an endpoint supplies a malformed prefix, a bare address, an IPv4 value as `ipv6`, or an IPv6 value as `ipv4`
- **THEN** the Link is rejected with a condition naming the invalid endpoint field and no connectivity component materializes it

#### Scenario: Accept point-to-point and host-prefix lengths
- **WHEN** an endpoint supplies a valid prefix including `/31`, `/32`, `/127`, or `/128` in the matching family
- **THEN** family and prefix-length validation accepts it without masking its host bits

#### Scenario: Reject an addressed host endpoint
- **WHEN** the reserved `host` endpoint carries either address family
- **THEN** the Link is rejected with a diagnostic explaining that endpoint addresses require a Node endpoint

### Requirement: Endpoint address changes recreate realized Nodes
After a Node has been realized, any addition, modification, or removal of its effective endpoint IPv4/IPv6 configuration SHALL recreate that Node. Initial cold planning SHALL apply addresses without an additional recreation, and no live endpoint-address configuration SHALL be attempted.

#### Scenario: Initial addressed cold start
- **WHEN** a Node's first accepted plan contains addressed Link endpoints
- **THEN** the imported kind receives those addresses during ordinary cold-start planning and no follow-up recreation is triggered solely by their presence

#### Scenario: Add, change, or remove an endpoint address
- **WHEN** a running Node's endpoint gains, changes, or loses an IPv4 or IPv6 prefix
- **THEN** the existing recreate lifecycle replaces that Node's device Pod and the new cold plan carries the complete desired address set

#### Scenario: One-sided address update
- **WHEN** only one endpoint of a Link changes its address set
- **THEN** only the Node owning that endpoint is required to recreate, while shared-Pod and grouping rules retain their existing blast radius

#### Scenario: Existing persistence policy survives recreation
- **WHEN** an address-driven recreation targets a Node with saved persistent device state
- **THEN** existing preservation, enforced-startup, and explicit device-state-reset rules remain unchanged and recreation does not implicitly erase that state
