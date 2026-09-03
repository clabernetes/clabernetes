## MODIFIED Requirements

### Requirement: Compilation emits explicit NodeProfile references

The compiler SHALL convert topology-level Kubernetes realization policy into one or more
NodeProfiles and SHALL stamp each emitted Node with the appropriate `profileRef`. It SHALL copy
`Topology.spec.expose.exposeType` into every generated NodeProfile that applies the Topology's
exposure policy, and neither the Topology nor generated NodeProfiles SHALL contain a
`disableExpose` field. It MUST preserve supported direct management policy and MUST NOT emit
Docker, launcher-image, nested-CRI, or containerlab-version policy.

#### Scenario: Nodes share topology policy

- **WHEN** all source Nodes use the same direct-workload policy
- **THEN** the compiler emits one shared NodeProfile and references it from all emitted Nodes

#### Scenario: One Node has a profile override

- **WHEN** one source Node has distinct resources or other direct-workload policy
- **THEN** the compiler emits a complete dedicated NodeProfile for that Node and stamps its
  explicit reference

#### Scenario: Topology defines a custom management network

- **WHEN** an existing Topology defines management settings with supported direct semantics
- **THEN** the compiler preserves those settings in the generated resources that own them

#### Scenario: Topology disables expose Services

- **WHEN** a Topology sets `spec.expose.exposeType: None`
- **THEN** every generated NodeProfile carrying its exposure policy sets `exposeType: None`
