# link-lifecycle Delta

## RENAMED Requirements

- FROM: `### Requirement: Tunnel allocation belongs to Link status`
- TO: `### Requirement: Wire identifier allocation belongs to Link status`

## MODIFIED Requirements

### Requirement: Wire identifier allocation belongs to Link status

For a valid Link whose selected direct realization needs a shared wire identifier, the Link
controller SHALL allocate it and store it in Link status. The identifier SHALL be unique among
the Links of its namespace; allocation MUST NOT read or depend on Links in other namespaces,
because wire datagrams dispatch inside one receiving sidecar and carry a validated source, so
identical identifiers in different namespaces can never meet. Users MUST NOT supply
controller-owned allocation values in Link spec.

#### Scenario: Cross-pod Link needs an allocation

- **WHEN** a valid Link terminates on Nodes realized by different Pods and its flavor needs a
  shared identifier
- **THEN** the controller allocates one identifier that both endpoint reconcilers consume

#### Scenario: Link is local to one pod

- **WHEN** both endpoints share one Pod or one endpoint is `host`
- **THEN** the Link is materialized locally without an unnecessary wire identifier allocation

#### Scenario: Two namespaces allocate independently

- **WHEN** Links in two different namespaces are allocated wire identifiers
- **THEN** each namespace allocates from its own space, identical identifiers across namespaces
  are valid, and reconciling a Link never reads Links outside its namespace
