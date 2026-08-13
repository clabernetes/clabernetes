# runtime-dns-forwarding Specification

## Purpose

Define the launcher runtime repair that allows SR Linux management namespaces to reach DNS and
remote Kubernetes launcher networking through the nested Docker management network.

## Requirements

### Requirement: Discover eligible SR Linux containers from runtime metadata

The launcher SHALL discover nested containers and their eligibility from structured Docker inspection
data after containerlab deployment. It SHALL identify SR Linux containers using containerlab's
`clab-node-kind` label, including the `srl` and `nokia_srlinux` kinds, and SHALL NOT re-parse the
rendered topology YAML.

#### Scenario: Select an eligible SR Linux container

- **WHEN** Docker inspection reports an SR Linux node-kind label, an independent network namespace,
  a management-network entry, a gateway, and an IPv4 address
- **THEN** the launcher selects that container for management forwarding

#### Scenario: Skip an unrelated container

- **WHEN** Docker inspection reports a container without an SR Linux node-kind label
- **THEN** the launcher skips that container without changing its routes or sysctls

#### Scenario: Skip a shared or unsupported network namespace

- **WHEN** Docker inspection reports an SR Linux container using `container:*`, `host`, or `none`
  network mode
- **THEN** the launcher skips that container without applying the nested management repair

### Requirement: Derive management network values from structured runtime data

The launcher SHALL obtain the management network name from structured management-network
configuration, defaulting to containerlab's `clab` network when no name is configured. It SHALL
obtain the gateway and node IPv4 address from the selected Docker inspection network entry and
SHALL treat missing or invalid values as an actionable runtime failure.

#### Scenario: Use a configured management network

- **WHEN** the launcher has a configured management network name and Docker inspection contains that
  network entry with a gateway and IPv4 address
- **THEN** the launcher uses those inspected values for the forwarding repair

#### Scenario: Use the default management network

- **WHEN** no management network name is configured and Docker inspection contains the `clab`
  network entry with valid values
- **THEN** the launcher uses the `clab` gateway and IPv4 address

#### Scenario: Reject incomplete runtime metadata

- **WHEN** the selected network entry has no gateway or IPv4 address
- **THEN** the launcher reports the node and missing value and does not apply partial forwarding
  configuration

### Requirement: Configure SR Linux management forwarding

The launcher MUST configure and verify the nested management forwarding path after the selected SR
Linux container exposes the `srbase-mgmt` namespace, its `mgmt0.0` peer, and the root namespace
`mgmt0` and `mgmt0-0` interfaces. It does so in the container root namespace by replacing the
gateway route and default route through `mgmt0`, replacing the node management route through
`mgmt0-0`, and enabling IPv4 forwarding.

#### Scenario: Apply forwarding after interface readiness

- **WHEN** `srbase-mgmt`, its `mgmt0.0`, and the root namespace `mgmt0` and `mgmt0-0` are present
- **THEN** the launcher applies the prescribed route replacements and enables IPv4 forwarding before
  reporting the node ready

#### Scenario: Reapply forwarding idempotently

- **WHEN** the forwarding helper runs again for a container with the same runtime values
- **THEN** route replacement and sysctl operations succeed without duplicate routes or errors caused
  by prior application

#### Scenario: Command verification fails

- **WHEN** any forwarding or verification command exits unsuccessfully
- **THEN** the launcher reports the node and failed operation and does not mark the node ready

### Requirement: Gate readiness on management forwarding

The launcher SHALL keep an eligible SR Linux node unready until management forwarding has completed
successfully. It SHALL retry transient namespace and interface absence for a bounded interval, then
report an actionable failure if the required runtime state never appears.

#### Scenario: Runtime interfaces appear during the retry window

- **WHEN** the required namespace or interface is initially absent but appears before the bounded
  timeout
- **THEN** the launcher applies and verifies forwarding and allows readiness to succeed

#### Scenario: Runtime interfaces never appear

- **WHEN** the required namespace or interface remains absent until the bounded timeout
- **THEN** the launcher keeps readiness failing and reports the node, missing runtime object, and
  timeout

### Requirement: Preserve standalone containerlab behavior

The forwarding repair SHALL be implemented only in the clabernetes launcher lifecycle and SHALL NOT
alter generic containerlab commands or require users to add topology-level execution commands.

#### Scenario: Standalone containerlab deployment

- **WHEN** a topology is deployed directly with standalone containerlab
- **THEN** containerlab behavior and its topology-defined execution commands remain unchanged
