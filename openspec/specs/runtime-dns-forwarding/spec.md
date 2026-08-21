# runtime-dns-forwarding Specification

## Purpose

Define the launcher runtime repair that allows SR Linux management namespaces to reach DNS and
remote Kubernetes launcher networking through the nested Docker management network.

## Requirements

### Requirement: Preserve standalone containerlab behavior

Direct management validation and any kind-opaque Kubernetes realization SHALL be owned by c9s and SHALL NOT require a containerlab patch, change standalone containerlab deployment semantics, or require topology-level execution commands. The absence of a c9s launcher repair SHALL be the default unless direct-Pod evidence demonstrates a generic platform gap.

#### Scenario: Standalone containerlab deployment

- **WHEN** a topology is deployed through standalone containerlab
- **THEN** its existing runtime and topology-defined execution behavior remains unchanged

### Requirement: Prove direct management reachability before remediation

The direct runtime SHALL first run a device image using the unmodified imported containerlab package behavior and the ordinary direct-Pod networking contract, without carrying forward a launcher-only repair. Applicable conformance SHALL verify management addressing, DNS resolution, exposure-Service reachability, and external reachability from the device's management plane. Historical nested-runtime behavior MUST NOT by itself create a direct plan action or readiness dependency.

#### Scenario: Direct SR Linux management works without launcher repair

- **WHEN** an SR Linux image is booted as the direct application container with its imported package-derived configuration
- **THEN** management addressing, DNS, Service, and external reachability pass without c9s inspecting or modifying device-internal namespace or interface names

#### Scenario: Direct reachability fails

- **WHEN** an applicable direct management observation fails before any compatibility repair is added
- **THEN** conformance records the exact failed observation and generic runtime boundary involved, and the Node remains incompatible until that generic capability is represented and verified

### Requirement: Management remediation is kind opaque and evidence derived

When direct evidence proves that c9s must realize additional management behavior, the behavior SHALL be represented as a generic runtime capability derived from explicit normalized inputs and imported containerlab behavior. Identical generic operations SHALL have identical semantics for every current or future kind. c9s MUST NOT infer remediation from a kind, vendor, image, environment variable, old launcher command, documentation string, or device-internal namespace or interface name. If imported behavior emits no representable operation and no kind-opaque Kubernetes platform capability can satisfy the failed observation, planning or conformance MUST report the missing generic capability rather than synthesize a vendor action.

#### Scenario: Imported behavior emits a supported generic operation

- **WHEN** imported behavior and explicit inputs produce a supported management route, sysctl, namespace, or readiness operation
- **THEN** the direct helper applies and verifies that typed operation idempotently without knowing which kind emitted it

#### Scenario: No generic operation describes the requested repair

- **WHEN** a failed management observation would require c9s to recognize a vendor or copy a device-internal name
- **THEN** c9s rejects the behavior as an unsupported generic capability and does not add a kind-specific plan, helper branch, or readiness gate

#### Scenario: A newly imported kind uses an existing management capability

- **WHEN** a containerlab module update adds a kind whose imported behavior emits only already-supported generic management operations
- **THEN** updating the module and dependency lock data is sufficient for c9s to plan and realize those operations

#### Scenario: Pod resolver identity completes unspecified node DNS

- **WHEN** a logical Node reaches an in-Pod lifecycle boundary without topology- or controller-declared DNS servers
- **THEN** runtime management completion fills the node's DNS configuration from the executing Pod's own resolv.conf exactly as containerlab fills node DNS from the host resolver, topology-declared DNS always wins, container-network-mode members are skipped, and every kind receives the identical completion
