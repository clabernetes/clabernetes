## MODIFIED Requirements

### Requirement: Component-based Nodes retain one logical lifecycle

A Node that Containerlab expands into multiple component containers SHALL remain one logical c9s
Node and one launcher workload. The launcher SHALL discover every expanded component from the
logical root-node identity, require every component to satisfy generic readiness, and use the sole
component that owns the shared network namespace for application probes. Every
`container:<target>` network mode SHALL resolve to a discovered component in the same namespace.
Missing or ambiguous component identity, an external or cyclic namespace reference, or ambiguous
network-namespace ownership MUST fail launcher discovery.

#### Scenario: Materialize a component-based Node

- **WHEN** Containerlab expands one Node into multiple labeled component containers
- **THEN** the launcher tracks all components as the nested realization of that one Node

#### Scenario: One expanded component stops

- **WHEN** any component container of a component-based Node is stopped, paused, restarting, dead, or unhealthy
- **THEN** generic readiness for the logical Node fails

#### Scenario: Probe a shared component network namespace

- **WHEN** application probes are configured for a component-based Node
- **THEN** the launcher addresses the component that owns the network namespace shared by the chassis

#### Scenario: Component ownership is ambiguous

- **WHEN** component labels identify duplicate component names, no network-namespace owner, or multiple network-namespace owners
- **THEN** launcher discovery fails instead of selecting a component arbitrarily

#### Scenario: Component namespace references are invalid

- **WHEN** a component references an undiscovered container, forms a cycle, or does not resolve to the sole namespace owner
- **THEN** launcher discovery fails before readiness and application probes begin

### Requirement: Node owns per-node payload attachments

The Node spec SHALL describe files and other payload required to instantiate that network node,
including supported URL- and ConfigMap-backed sources. Launcher Pod policy MUST NOT be used merely
to associate a payload attachment with one Node. When Nodes share one launcher filesystem and
declare the same normalized destination for a shared payload, the controller SHALL render that
destination as one Pod mount only when the source ConfigMap, key, and file mode are identical.
Conflicting attachments at one destination MUST fail reconciliation before the launcher Deployment
is created or updated.

#### Scenario: Attach a ConfigMap-backed startup file

- **WHEN** a Node references a supported ConfigMap-backed payload file
- **THEN** the Node controller mounts that file into the launcher responsible for the Node

#### Scenario: Fetch a URL-backed payload file

- **WHEN** a Node references a supported URL-backed payload file
- **THEN** the launcher fetches the file before materializing the Containerlab node

#### Scenario: Group members share an identical license destination

- **WHEN** grouped Nodes reference the same normalized license destination and identical payload source
- **THEN** the controller renders one mount at that path and Kubernetes accepts the launcher Pod

#### Scenario: Group members conflict at one destination

- **WHEN** grouped Nodes reference the same normalized destination with different payload sources or modes
- **THEN** reconciliation reports the conflict and does not create or update the launcher Deployment
