## MODIFIED Requirements

### Requirement: User workflows are behaviorally equivalent

Direct Node and Link manifests, Topology compilation, and clabverter output SHALL produce equivalent device plans and running labs for the same representable topology. Startup configuration, licenses, persistence, management reachability, DNS, Services, probes, exec, logs, save, events, and packet capture SHALL have direct-runtime conformance coverage. Persistence conformance SHALL cover saved-configuration survival across Pod restart, Pod replacement, and declared spec change, for both CLI-format and full-file startup configurations, plus the enforce-startup-config and reset opt-outs.

#### Scenario: Compare entry paths

- **WHEN** the same representable lab is submitted through each supported entry path
- **THEN** normalized plans are equivalent and the running labs pass the same applicable observations

#### Scenario: Input is not portable

- **WHEN** any entry path receives semantics the direct runtime cannot represent
- **THEN** it rejects them before workload creation with equivalent structured diagnostics

#### Scenario: Saved configuration survival matrix

- **WHEN** the persistence conformance suite runs a persistent node seeded from each startup-configuration format, saves a change, and replaces the Pod by deletion and by spec change
- **THEN** the saved change survives every replacement, and the enforce-startup-config and reset paths restore the declared startup configuration
