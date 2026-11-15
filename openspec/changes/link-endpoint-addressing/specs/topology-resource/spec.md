# Spec Delta

## ADDED Requirements

### Requirement: Compilation preserves Link endpoint addresses
The compiler SHALL map containerlab link-level IPv4/IPv6 arrays and structured veth-endpoint IPv4/IPv6 fields onto optional CIDR prefixes on the corresponding direct Link endpoints. Invalid or unrepresentable address input SHALL fail before resource creation.

#### Scenario: Compile brief Link address arrays
- **WHEN** a Topology declares brief endpoints and an `ipv4` or `ipv6` array with one or two entries
- **THEN** each non-empty array entry is preserved on the endpoint at the same index in the emitted Link

#### Scenario: Compile structured veth endpoint addresses
- **WHEN** an explicit veth Link declares `ipv4` or `ipv6` within structured node/interface endpoint mappings
- **THEN** the emitted Link carries the same endpoint addresses as the equivalent brief link-address arrays

#### Scenario: Compile one-sided addressing
- **WHEN** a link-address array assigns a prefix to only one endpoint or leaves the other entry empty
- **THEN** only the selected endpoint carries an address and compilation succeeds

#### Scenario: Reject unusable Link addresses
- **WHEN** a link-address value is not a CIDR prefix, uses the wrong address family, or supplies more entries than endpoints
- **THEN** compilation fails with a deterministic source-located diagnostic and emits no partially addressed resources

#### Scenario: Direct conversion preserves addresses
- **WHEN** command-line conversion processes a Topology containing either supported Link address syntax
- **THEN** its direct Link manifests preserve the same endpoint addresses as in-cluster compilation
