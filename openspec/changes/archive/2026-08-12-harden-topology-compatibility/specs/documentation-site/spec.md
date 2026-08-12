## ADDED Requirements

### Requirement: Runtime compatibility and readiness behavior is documented

The user documentation SHALL explain that Topology compilation warns for lossy fields, fails for
structurally unrealizable resources, and does not expose strict compilation as a clabverter CLI
flag. It SHALL also explain that enabled grouped-node readiness is atomic across nested members,
Docker image healthchecks are honored, process-level readiness is not application readiness, and
TCP/SSH checks apply to the primary Node.

#### Scenario: Reader diagnoses a rejected Topology

- **WHEN** a reader consults the Topology or architecture documentation after a compilation failure
- **THEN** the documentation identifies unsupported pseudo-nodes, special endpoints, unresolved endpoints, unsupported link types, and invalid grouping as fatal compatibility cases

#### Scenario: Reader configures grouped readiness

- **WHEN** a reader consults the Node or LauncherProfile documentation
- **THEN** the documentation explains all-member readiness, Docker healthcheck requirements, process-level limitations, and primary-only TCP/SSH probes
