## MODIFIED Requirements

### Requirement: Node is an independently reconcilable network node

The system SHALL represent each Containerlab network node as one namespaced Node resource whose metadata name is the Containerlab node name. Creating a valid Node SHALL be sufficient to request direct realization without a Topology resource.

#### Scenario: Create a standalone Node directly

- **WHEN** a user creates a valid Node without a Topology owner
- **THEN** the Node controller plans the Node and reconciles its direct device workload

#### Scenario: Delete one Node independently

- **WHEN** a user deletes one Node from a namespace containing other Nodes
- **THEN** the controller removes only resources owned by that Node and reconciles affected grouped Nodes and Links without deleting unrelated Nodes

### Requirement: Node spec is self-contained Containerlab node intent

The Node spec SHALL contain a flattened Containerlab node definition drawn from the supported compatibility vocabulary, including node kind, image, device configuration, and per-device management addresses. Emitters MUST expand source topology defaults and kinds before creating a Node, wiring MUST NOT be embedded in Node spec, and the direct planner MUST NOT require a complete topology document.

#### Scenario: Materialize a Node definition

- **WHEN** the controller receives a Node containing a valid flattened Containerlab definition and its referenced inputs
- **THEN** it produces the equivalent direct device plan without topology-level defaults, kinds, a rendered containerlab topology, or a containerlab executable

#### Scenario: Wiring is changed independently

- **WHEN** a Link terminating on a Node is added, changed, or removed
- **THEN** the Node spec remains unchanged

### Requirement: Node declares destination ports, not host mappings

A Node SHALL declare additional ports as destination ports with an optional protocol. The Pod-side port carrying each destination port is an allocation owned by the controller and MUST NOT be user-specifiable. The schema MUST reject host-to-container mapping syntax, and port parsing MUST reject any form it cannot represent rather than reinterpreting it.

#### Scenario: Declare an additional port

- **WHEN** a Node lists a destination port with an optional protocol
- **THEN** the controller allocates a Pod-side port, records the pair in status, and programs a Service that targets the direct device Pod and destination port

#### Scenario: Reject a host-to-container mapping

- **WHEN** a user applies a Node listing a port as a host-to-container mapping
- **THEN** the API server rejects the manifest

#### Scenario: Reject an unrepresentable port entry

- **WHEN** a port entry specifies a host IP binding, a port range, or an unsupported protocol
- **THEN** the entry is reported as invalid instead of being parsed into a different port or protocol

#### Scenario: Compile a source topology using host mappings

- **WHEN** a Topology definition declares node ports as host-to-container mappings
- **THEN** the compiler emits Nodes carrying only the destination ports

### Requirement: Unsupported Node fields are rejected

The Node schema SHALL reject unknown and removed field names rather than accepting and ignoring them. The API MUST NOT preserve arbitrary unrecognized keys in the Node spec, except where a field's value is defined as arbitrary user data.

#### Scenario: Apply a Node using a removed field

- **WHEN** a user applies a Node manifest containing a field removed from the Node vocabulary
- **THEN** the API server rejects the manifest and names the offending field

#### Scenario: Arbitrary user data is preserved

- **WHEN** a Node declares Containerlab config-engine variables with nested arbitrary values
- **THEN** those values are stored unchanged and supplied to the direct device planner

### Requirement: Node grouping is declared only by container network mode

Grouping Nodes into one direct Pod SHALL be declared exclusively by Containerlab `network-mode: container:<primary>`. The schema MUST reject other network-mode values.

#### Scenario: Group a secondary Node onto its primary

- **WHEN** a Node sets `network-mode` to `container:<primary>` naming another Node in its namespace
- **THEN** the controller renders it as another application container in the primary Node's Pod

#### Scenario: Reject an unrealizable network mode

- **WHEN** a user applies a Node whose `network-mode` is `host` or any value other than `container:<primary>`
- **THEN** the API server rejects the manifest

### Requirement: Component-based Nodes retain one logical lifecycle

A Node that the baseline planner expands into multiple component containers SHALL remain one logical c9s Node and one direct workload. Every component SHALL be declared as a Kubernetes application container, every required component SHALL satisfy generic readiness, and the plan SHALL identify the sole owner of a shared network namespace for application probes. Every component namespace reference MUST resolve within that workload; missing or ambiguous identity, external or cyclic references, or ambiguous ownership MUST fail planning.

#### Scenario: Materialize a component-based Node

- **WHEN** the planner expands one Node into several identified component containers
- **THEN** the controller renders every component directly in the logical Node's Pod

#### Scenario: One expanded component stops

- **WHEN** any required component is terminated, waiting, restarting, or unready
- **THEN** generic readiness for the logical Node fails

#### Scenario: Probe a shared component network namespace

- **WHEN** application probes are configured for a component-based Node
- **THEN** the plan addresses the component declared to own the network namespace shared by the chassis

#### Scenario: Component ownership is ambiguous

- **WHEN** planned components contain duplicate identities, no network-namespace owner, or multiple network-namespace owners
- **THEN** planning fails instead of selecting a component arbitrarily

#### Scenario: Component namespace references are invalid

- **WHEN** a component references an undeclared component, forms a cycle, or does not resolve to the sole namespace owner
- **THEN** planning fails before the workload is created

### Requirement: Node owns per-node payload attachments

The Node spec SHALL describe files and other payload required to instantiate that network node, including supported URL- and ConfigMap-backed sources. Profile policy MUST NOT be used merely to associate a payload with one Node. When grouped Nodes declare the same normalized destination for a shared payload, the controller SHALL stage that destination once only when source identity and mode are identical. Conflicting attachments at one destination MUST fail planning before workload creation or update.

#### Scenario: Attach a ConfigMap-backed startup file

- **WHEN** a Node references a supported ConfigMap-backed payload file
- **THEN** the controller mounts or stages that file for the direct device container at the planned destination

#### Scenario: Fetch a URL-backed payload file

- **WHEN** a Node references a supported URL-backed payload file
- **THEN** a preparation component fetches and verifies it before the affected device starts

#### Scenario: Group members share an identical license destination

- **WHEN** grouped Nodes reference the same normalized license destination and identical payload source
- **THEN** the controller stages one shared file and every planned consumer receives it

#### Scenario: Group members conflict at one destination

- **WHEN** grouped Nodes reference the same normalized destination with different payload sources or modes
- **THEN** reconciliation reports the conflict and does not create or update the direct workload

### Requirement: LauncherProfile reference is optional and explicit

The Node spec SHALL expose an optional, same-namespace `launcherProfileRef`. The system MUST resolve at most one LauncherProfile for a Node and MUST NOT attach LauncherProfiles through labels, selectors, priorities, or implicit multi-profile merging.

#### Scenario: Node omits launcherProfileRef

- **WHEN** a Node has no `launcherProfileRef`
- **THEN** the controller realizes it using global Config defaults

#### Scenario: Node references an existing LauncherProfile

- **WHEN** a Node references a LauncherProfile in its namespace
- **THEN** the controller realizes its direct workload using that profile layered over global Config defaults

#### Scenario: Node references a missing LauncherProfile

- **WHEN** a Node names a LauncherProfile that does not exist
- **THEN** the controller does not create or update the direct workload and reports `LauncherProfileResolved=False`

### Requirement: Grouped Nodes use one launcher policy

Nodes sharing a direct Pod through Containerlab `network-mode: container:<primary>` SHALL use the primary Node's effective LauncherProfile. A secondary Node MUST NOT select a conflicting LauncherProfile.

#### Scenario: Secondary omits a profile reference

- **WHEN** a secondary Node omits `launcherProfileRef` and its primary references a LauncherProfile
- **THEN** the secondary is realized in the primary workload using the primary's effective profile

#### Scenario: Group members reference conflicting profiles

- **WHEN** a secondary Node explicitly references a different LauncherProfile than its primary
- **THEN** the controller reports the group as invalid and does not realize the inconsistent workload

### Requirement: Node status contains only per-node observations and allocations

The controller SHALL record Node readiness, plan identity, probe observations, exposed-port allocations, standard conditions, and applied LauncherProfile identity in Node status. User intent and full plans MUST NOT be stored in status.

#### Scenario: LauncherProfile is applied

- **WHEN** a Node is successfully realized with a referenced LauncherProfile
- **THEN** Node status records the applied profile name, UID, and generation

#### Scenario: Node readiness changes

- **WHEN** a direct application container, preparation/connectivity condition, or configured probe changes readiness
- **THEN** the controller updates only the affected Node readiness and conditions

## ADDED Requirements

### Requirement: Node vocabulary excludes fields the direct runtime cannot realize

The Node spec SHALL NOT expose Containerlab node fields the direct runtime cannot represent with defined portable semantics. Excluded fields are those absent from the declared compatibility baseline, those whose meaning spans several Nodes and belongs to another c9s resource, and those describing Pod-level policy owned by LauncherProfile or the Pod plan. Supported fields MUST be planned and enforced rather than ignored.

#### Scenario: Field owned by realization policy is absent from Node

- **WHEN** a user inspects the Node schema for container resource limits, image pull policy, or operational probes
- **THEN** those fields are absent from the Node spec and available on LauncherProfile instead

#### Scenario: Labels live in Node metadata, not in the spec

- **WHEN** a user inspects the Node schema for Containerlab node labels
- **THEN** no such spec field exists, because Kubernetes object metadata is where labels belong and only those are selectable

#### Scenario: Inter-node deployment ordering is absent from Node

- **WHEN** a user inspects the Node schema for Containerlab deployment stages or startup ordering against other Nodes
- **THEN** those fields are absent because c9s owns workload ordering and reports dependencies explicitly

#### Scenario: Container policy is enforced directly

- **WHEN** a Node declares supported devices, added capabilities, shared memory size, tmpfs, security options, or privileged execution
- **THEN** its plan enforces them on the direct application container and Pod

#### Scenario: Certificate SANs are reachable

- **WHEN** a Node requests an issued certificate with subject alternative names
- **THEN** the names are declared on the Node's certificate configuration and the direct preparation plan stages the certificate for the device

## REMOVED Requirements

### Requirement: Node vocabulary excludes fields a launcher cannot realize

**Reason**: Renamed for the direct runtime: the launcher boundary no longer defines realizable Node vocabulary; the direct runtime does. The replacement requirement is added in this change.

**Migration**: Use the added requirement "Node vocabulary excludes fields the direct runtime cannot realize".

### Requirement: Node vocabulary is parseable by the launcher's Containerlab

**Reason**: Direct device Pods contain no launcher container or containerlab executable; compatibility is instead verified against the exact baseline registry and runtime-neutral planner.

**Migration**: Pin the baseline and validate Node vocabulary through the registry-driven `device-planning` compatibility gate.

### Requirement: Enabled Node readiness reflects generic launcher state

**Reason**: Docker inspection and nested image healthchecks do not exist in the direct runtime.

**Migration**: Use Kubernetes application-container status plus plan/profile-defined startup and readiness probes as specified by `direct-device-runtime`.
