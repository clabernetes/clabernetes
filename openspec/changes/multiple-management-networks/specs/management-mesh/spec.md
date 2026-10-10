## ADDED Requirements

### Requirement: A namespace carries the management networks its topology declares

The direct runtime SHALL realize every management network a topology declares, not only the
first one. Each declared network SHALL have its own address policy, its own deterministic
allocation pool, and its own mesh segment, and a node SHALL attach to exactly the network its
`mgmt-net` key (inherited through the regular node > group > kinds > defaults order) selects.
Nodes that declare no `mgmt-net` SHALL attach to the namespace's default network, which is the
first declared management network.

#### Scenario: Two management networks in one lab

- **WHEN** a topology declares two management networks and assigns one node to each
- **THEN** each node receives its management address from its selected network's policy and
  reaches the other members of its own network device-to-device, while no management traffic
  crosses between the two networks

#### Scenario: A node inherits its network from the kind block

- **WHEN** a topology sets `mgmt-net` under `topology.kinds.<kind>` and the node declares none
- **THEN** the node attaches to that kind's network, exactly as the inheritance order selects
  every other node setting

#### Scenario: A management network with no member

- **WHEN** a topology declares a management network that no node selects
- **THEN** compilation succeeds, the network consumes no pool addresses, and the lab deploys on
  the remaining networks

### Requirement: Unrepresentable management drivers fail with a substitution note

The compiler SHALL reject `driver: macvlan` management networks and management `tailscale`
integration with structured diagnostics that name the substitute (the shared management mesh)
and the accepted address-policy fields. Docker-only management fields that carry no cluster
meaning (`network`, `bridge`, `mtu`, `driver-opts`, `ipam`, and the macvlan interface options)
SHALL be accepted and ignored with a warning diagnostic.

#### Scenario: A macvlan management network

- **WHEN** a topology declares `mgmt.driver: macvlan` with a parent interface
- **THEN** compilation fails with a diagnostic explaining that the management mesh replaces
  macvlan and naming the accepted address-policy fields

#### Scenario: A single-entry management list

- **WHEN** a topology writes the management network as a one-element list
- **THEN** compilation carries the network unchanged, exactly as the mapping form does
