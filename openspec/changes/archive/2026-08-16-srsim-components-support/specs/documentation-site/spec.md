## MODIFIED Requirements

### Requirement: Runtime compatibility and readiness behavior is documented

The user documentation SHALL explain that Topology compilation warns for lossy fields, fails for
structurally unrealizable resources, supports explicit `veth` links with brief or structured
node/interface endpoints, and does not expose strict compilation as a clabverter CLI flag. It SHALL
also explain that enabled grouped-node readiness is atomic across nested members, component-based
Nodes are evaluated across all expanded containers, application probes use the sole component that
owns the shared network namespace, Docker image healthchecks are honored, process-level readiness
is not application readiness, and duplicate shared payload destinations are mounted once only when
their sources agree. Conflicting shared destinations and invalid component namespace ownership SHALL
be documented as deployment failures.

#### Scenario: Reader diagnoses a rejected Topology

- **WHEN** a reader consults the Topology or architecture documentation after a compilation failure
- **THEN** the documentation identifies unsupported pseudo-nodes, special endpoints, unresolved endpoints, unsupported link types other than `veth`, invalid endpoint values, and invalid grouping as fatal compatibility cases

#### Scenario: Reader uses an explicit veth link

- **WHEN** a reader supplies an explicit `veth` link with brief or structured node/interface endpoints
- **THEN** the documentation identifies both forms as equivalent for c9s compilation

#### Scenario: Reader configures component-based SR-SIM

- **WHEN** a reader consults the SR-SIM documentation for a component-based or `components: []` topology
- **THEN** the documentation explains root-node expansion, all-component readiness, the sole shared-network owner used for application probes, and the boundary between Containerlab expansion and c9s orchestration

#### Scenario: Reader configures grouped readiness

- **WHEN** a reader consults the architecture or SR-SIM documentation
- **THEN** the documentation explains all-container readiness, Docker healthcheck requirements, shared network-namespace probing, process-level limitations, and shared payload mount behavior

#### Scenario: Reader diagnoses a conflicting shared payload

- **WHEN** grouped Nodes declare different payload sources for one normalized destination
- **THEN** the documentation explains that reconciliation rejects the conflict instead of selecting one source silently
