# device-planning Specification

## Purpose

Define a deterministic c9s device-plan contract derived from an unmodified, version-pinned containerlab Go dependency and its live imported registry.
## Requirements
### Requirement: Containerlab dependency is exact and registry discovery is exhaustive

The repository SHALL pin one exact `github.com/srl-labs/containerlab` Go module version in its module graph and SHALL discover every registered kind name and alias from that imported release at runtime or test time. The repository MUST NOT maintain a second machine-readable kind/alias inventory or expected registry digest.

#### Scenario: Verify the pinned release

- **WHEN** device planning runs for the pinned containerlab release
- **THEN** the generated report or parameterized run contains every live registered kind name and alias exactly once without consulting a committed expected-name set

#### Scenario: Containerlab adds a kind or alias

- **WHEN** the pinned containerlab release changes and its live registry differs
- **THEN** registry-driven planning and conformance automatically exercise it without a c9s kind registration or implementation change

### Requirement: Containerlab remains an unmodified pinned dependency

c9s SHALL consume the declared containerlab release without a source patch, fork, local module replacement, or companion repository change. Containerlab SHALL remain the exclusive owner of kind knowledge. c9s SHALL own only the generic plan schema, operation recorder, controlled filesystem boundary, Kubernetes renderer, and registry-parameterized conformance. c9s MUST NOT contain kind-named planning switches, copied defaults or templates, allowlists, or manually registered fixtures required for a kind to work.

#### Scenario: Update the containerlab dependency

- **WHEN** c9s updates its pinned containerlab Go module version
- **THEN** every registered kind and alias flows through generic planning automatically, and verification fails only for a recorded generic operation with no direct-runtime representation

#### Scenario: Containerlab adds an ordinary kind

- **WHEN** the pinned module is updated to a release containing a new kind implemented through the imported interfaces
- **THEN** the dependency update alone makes that kind plan and run through the existing generic c9s integration

#### Scenario: Build against the pinned release

- **WHEN** c9s builds or verifies device planning for the pinned release
- **THEN** it requires no patched containerlab source or artifact from a sibling repository

### Requirement: Every supported kind produces a runtime-neutral device plan

For every kind in the live imported registry, the c9s planning adapter SHALL use the imported registry and applicable exported kind/configuration behavior to convert a fully resolved Node and its explicit inputs into a versioned runtime-neutral plan. The plan MUST describe all application and component containers, images, entrypoints, commands, environment, security, resources, files, mounts, devices, lifecycle actions, management behavior, readiness, and interface requirements needed to realize that Node. A kind MUST NOT be marked compatible if any required behavior is absent from the plan.

#### Scenario: Plan a registered kind

- **WHEN** a complete valid definition for any registered kind is submitted to the planner
- **THEN** planning returns either a complete direct-runtime plan or a structured rejection naming input that has no portable representation

#### Scenario: Kind requires several containers

- **WHEN** a component-based or distributed-chassis kind expands one logical Node into several cooperating containers
- **THEN** its plan identifies every component, network-namespace relationship, file, lifecycle action, and readiness contribution without hiding a nested container

#### Scenario: Plan is incomplete

- **WHEN** an imported hook emits a generic operation not represented by its plan
- **THEN** conformance fails with that operation class and the affected discovered kinds remain incompatible without adding a kind-specific mapping

### Requirement: Device planning is deterministic and side-effect free

Planning SHALL depend only on explicit versioned inputs, including the resolved Node definition, image metadata, payload metadata, certificate material metadata, management allocation, resolved resolver DNS, and interface inventory. Imported hook execution MUST occur in one short-lived, deadline-bounded c9s planning Pod rather than the manager process, and MUST NOT occur in the preparation boundary. Observation-dependent lifecycle hooks remain at the in-Pod hook boundary and are bound to the accepted plan by input digest rather than by a re-derived deployment stream. The manager SHALL attach a bounded request/response stream to that worker so it can supply genuinely missing OCI metadata or issued certificate material without starting another Pod. The worker MUST have no service-account token, host path, privileged security context, network access, or ambient capability; MUST use a read-only root filesystem plus private scratch; and MUST audit imported calls through a recorder boundary. It MAY retain only `CHOWN` and `FOWNER` after dropping `ALL`, because imported generic preparation records package-owned ownership and ACL metadata and every writable path is confined to private scratch. It MUST NOT pull an image, launch or inspect a running container, access an implicit host path, create a privileged network namespace, or mutate host or runtime state. Identical normalized inputs MUST produce byte-equivalent normalized plans, including generated artifact bytes.

Preparation SHALL stage the accepted plan's artifacts and steps without evaluating imported code. Staged artifacts MUST match the plan by path, mode, ownership, and digest before any publication. Publication onto a persistent artifact volume SHALL be state-aware: preparation records the digest of every artifact it publishes, and on later runs it MUST NOT overwrite a planned file whose current digest differs from the digest recorded at its last staging, unless the node definition enforces its startup configuration or a device-state reset was requested. A planned file whose current digest still matches its recorded staging digest SHALL be republished when the plan's content changed. On non-persistent volumes publication remains unconditional.

#### Scenario: Repeat a plan

- **WHEN** the same normalized planning inputs are evaluated more than once
- **THEN** the normalized serialized plans are byte-equivalent, including artifact bytes

#### Scenario: Required metadata is not present initially

- **WHEN** imported setup discovers an image reference or certificate requirement absent from the initial input
- **THEN** the worker requests it from the manager, incorporates the validated response in memory, and continues in the same process without registry or Kubernetes access

#### Scenario: Required input cannot be supplied

- **WHEN** the manager cannot resolve requested image metadata or issue requested certificate material
- **THEN** planning returns a structured missing-input error instead of consulting a local Docker daemon, guessing a default, or starting another discovery worker

#### Scenario: Imported image role uses known metadata

- **WHEN** imported setup assigns another role to an image reference whose metadata is already in the input for that logical Node
- **THEN** the worker associates that role in memory without another controller request, registry lookup, or Pod

#### Scenario: Imported code bypasses the runtime interface

- **WHEN** an imported hook attempts direct filesystem, network, namespace, or host access outside the controlled generic boundary
- **THEN** the locked-down planning worker denies or confines the attempt, returns no accepted plan, and cannot mutate the manager or Kubernetes worker node

#### Scenario: Imported preparation applies filesystem metadata

- **WHEN** an imported hook creates directories, changes ownership, or applies bounded extended attributes inside its controlled workspace
- **THEN** the plan records those generic artifacts and metadata digests and preparation reproduces them without identifying the emitting kind

#### Scenario: Imported endpoint lifecycle has a post-deployment fixup

- **WHEN** an imported Node implements endpoint deployment and post-deployment hooks
- **THEN** the connectivity boundary invokes both hooks in package order against the live container, and neither hook is replayed or compared to a re-derived stream

#### Scenario: Imported lifecycle follows application logs

- **WHEN** an imported post-deployment hook requests a log stream for one of its planned containers
- **THEN** a Pod-UID- and plan-scoped generic broker streams that direct Kubernetes container's logs without exposing Kubernetes credentials or kind knowledge to the application

#### Scenario: Preparation finds a device-modified planned file

- **WHEN** preparation runs on a persistent artifact volume and a planned file's current digest differs from the digest recorded at its last staging
- **THEN** the plan's artifact is still verified against the plan, but the device-modified file is left in place and the skip is recorded

#### Scenario: Staging ledger is missing or unreadable

- **WHEN** preparation runs on a persistent volume that holds prior artifacts but no readable staging ledger, for example after an upgrade from a release without the ledger
- **THEN** planned files whose content differs from the plan are treated as device-modified and preserved, a fresh ledger is established, and the condition is reported

#### Scenario: Preparation has no imported code path

- **WHEN** the preparation boundary runs for any accepted plan
- **THEN** it produces identical staged content without a registry, entropy session, or kind evaluation, and no preparation failure can be caused by imported initialization

### Requirement: Registry-driven conformance covers imported behavior

Conformance SHALL enumerate the live imported registry and parameterize generic scenario classes from operations emitted by each kind. Adding a kind MUST NOT require a c9s fixture file or fixture registration. Recorded plan output and operation coverage SHALL remain reviewable and deterministic without becoming a second implementation catalog.

#### Scenario: Verify all imported kinds

- **WHEN** the planning conformance suite runs
- **THEN** it discovers every canonical kind and alias from the imported registry and exercises each without consulting a c9s kind list

#### Scenario: Planner output changes

- **WHEN** the c9s generic adapter or containerlab dependency changes normalized output
- **THEN** verification reports the changed generic operations and affected discovered kinds without requiring per-kind adapter edits

### Requirement: Unsupported semantics fail before workload creation

The c9s planning adapter SHALL reject Docker- or host-specific input that has no defined portable c9s behavior. Rejections MUST identify the Node, field or behavior, and reason, and the Node controller MUST surface the rejection without creating or updating its device workload.

#### Scenario: Input has no Kubernetes representation

- **WHEN** a Node requests a bind, device, security option, network mode, or lifecycle behavior for which c9s has no defined portable semantics
- **THEN** planning fails before workload creation with a condition and event naming the unrepresentable input

### Requirement: Plans carry a vendor-neutral management-interposition profile

For every supported kind, device planning SHALL derive a management-interposition profile from the
pinned containerlab dependency and carry it in the runtime-neutral plan. The profile SHALL declare
at least: the interface name and MAC behavior the device expects for its management port, the
management gateway inputs for generated configuration, the inbound port translations for the
Pod-address path, and the management mesh membership — the mesh tunnel identifier and the
deterministic gateway link-layer identity — all supplied to planning as controller-allocated
data. The profile MUST NOT carry a peer-discovery transport name: the peer set reaches the
sidecar through the published peer directory, not through the plan.

Consumers of the profile (renderer, sidecar, controllers) MUST NOT contain kind- or
vendor-conditional behavior; all vendor variance SHALL be expressed through profile data, and
universal hardening (checksum offload, forwarding scoping, translation precedence, ARP responder
scoping, gateway containment, state re-assertion) SHALL be unconditional runtime baseline rather
than profile flags. Where the pinned containerlab version does not expose a needed fact
declaratively, the fact SHALL live only in the version-pinned compatibility layer of device
planning and SHALL be tracked as an upstream containerlab contribution.

#### Scenario: Profile is derived, not hardcoded

- **WHEN** a supported kind's plan is produced
- **THEN** its interposition profile is derived from the pinned containerlab registry or the
  version-pinned compatibility layer, and no component outside device planning branches on kind or
  vendor identity to realize interposition

#### Scenario: Kind declares no explicit management interface

- **WHEN** a kind's evaluated containerlab configuration exposes no explicit management interface
  name
- **THEN** the profile uses containerlab's primary-interface contract, matching what the kind
  would observe under containerlab

#### Scenario: Pinned dependency changes

- **WHEN** the pinned containerlab version is updated
- **THEN** registry-driven conformance verifies every supported kind still yields a complete
  interposition profile, and any drift fails the compatibility gate before workloads are affected

#### Scenario: Mesh membership is planned data

- **WHEN** a namespace-owning Node with an allocated management identity is planned
- **THEN** its interposition profile carries the controller-allocated mesh tunnel identifier and
  the deterministic gateway identity, and planning rejects incomplete or invalid mesh input
  before any workload is created

### Requirement: Management artifacts render from allocated identities at plan time

Management-parameterized configuration SHALL render from controller-allocated management inputs during planning. Runtime completion MUST NOT synthesize a management identity from the Pod address, and MUST NOT contribute any management-rendering input from the Pod's own environment. Resolver DNS used by generated configuration SHALL be resolved by the manager and supplied to planning as controller-allocated data. A plan whose management or resolver inputs are incomplete for any node SHALL fail planning with a diagnostic naming the node rather than degrading to a Pod-derived identity.

#### Scenario: Startup configuration is rendered

- **WHEN** a kind renders management-parameterized startup configuration
- **THEN** the render uses the allocated management address, prefix, gateway, and resolved resolver set available at plan time and is byte-identical between planning and preparation

#### Scenario: Allocation is missing

- **WHEN** planning encounters a node without a complete allocated management identity
- **THEN** planning fails closed with a diagnostic naming the node and the missing allocation

#### Scenario: Resolver DNS is unavailable

- **WHEN** generated configuration requires resolver DNS and the manager cannot resolve a consistent set
- **THEN** planning fails closed with a diagnostic naming the node and the affected artifact

#### Scenario: Prepared content does not diverge

- **WHEN** preparation stages generated artifacts
- **THEN** every staged byte comes from the plan, and no Pod-scoped identity or resolver value is substituted into staged content

### Requirement: Planner sessions are bounded and make observable progress

One content-addressed planner Pod SHALL conduct the complete planning conversation for a Node or
shared workload group. The controller SHALL resolve each declared image before creating the Pod.
The worker MAY request additional image metadata or certificate material over its attached stream,
but every accepted response MUST add a fact needed by evaluation. The controller and worker SHALL
reject repeated requests that add no facts, mismatched responses, protocol violations, and sessions
that exceed their round or time bounds.

#### Scenario: Ordinary single-image planning

- **WHEN** one logical Node uses its declared image and requires no certificate
- **THEN** the controller resolves that image once, one planner Pod returns the complete plan, and
  the controller creates the device workload without an image-discovery Pod

#### Scenario: Certificate material is required

- **WHEN** imported setup declares a certificate requirement
- **THEN** the manager issues or reuses the bundle and returns it over the attached stream, and the
  same worker reruns the relevant evaluation and completes the plan

#### Scenario: Session makes no progress

- **WHEN** evaluation repeats an equivalent request after the response added no required fact
- **THEN** the worker fails with a stable structured diagnostic before the total session deadline

### Requirement: Planner attempt artifacts have bounded, ownership-safe retention

Planner attempts SHALL use content-addressed worker identities and persist the finalized normalized
input and completed plan in immutable, Node-owned ConfigMaps. For each Node reconcile, the
controller SHALL retain the accepted workload's input, completed session output, and any in-flight
attempt needed to make progress. Superseded worker Pods, NetworkPolicies, input ConfigMaps, and
output ConfigMaps SHALL be garbage-collected by Node UID and component labels, without deleting
resources owned by another Node.

#### Scenario: Reconcile with a successful current attempt

- **WHEN** a planner session succeeds for the current input
- **THEN** its finalized input and persisted output remain available for the accepted workload or
  a later cached lookup, while superseded attempts are eligible for collection

#### Scenario: Reconcile with an in-flight attempt

- **WHEN** a worker Pod or its input has been created but has not produced a durable output
- **THEN** the controller retains that Pod and input so a later reconcile can observe or complete
  the attempt instead of deleting work that is still in progress

#### Scenario: Superseded attempts are collected

- **WHEN** a later attempt has superseded an older planning attempt
- **THEN** the older Node-owned Pod, NetworkPolicy, input ConfigMap, and output ConfigMap are removed
  while current and unrelated resources remain untouched

#### Scenario: Unchanged Node reuses the completed session

- **WHEN** the declared image digest and all other initial planning identities are unchanged
- **THEN** the controller accepts the cached finalized input and plan without creating a planner Pod

#### Scenario: A similarly named resource belongs to another Node

- **WHEN** cleanup encounters a worker artifact with a different owner UID
- **THEN** cleanup leaves that artifact unchanged

### Requirement: Imported code executes only at the planning and hook boundaries

Imported containerlab code SHALL be evaluated only inside the short-lived planning worker and, for
observation-dependent lifecycle behavior, inside the accepted Node's own in-Pod hook boundary. The
preparation init container MUST NOT construct a node registry, decode kind-specific configuration,
initialize or deploy a kind, or run a kind hook; it SHALL realize a plan that is complete without
imported evaluation. The in-Pod hook boundary SHALL invoke only the hook the accepted plan declares,
against the live direct container, and MUST NOT re-derive or verify the plan's deployment stream.

#### Scenario: Preparation stages a plan

- **WHEN** a preparation container runs for an accepted plan
- **THEN** it stages and verifies plan-declared content only, with no registry or kind initialization
  available to it

#### Scenario: Hook boundary runs the accepted hook

- **WHEN** a Node's lifecycle boundary runs imported post-deploy, readiness, save, or endpoint
  behavior
- **THEN** it reconstructs the imported Node graph without side effects and invokes that hook against
  the live direct container

#### Scenario: Hook boundary does not replay the deployment

- **WHEN** the hook boundary reconstructs a Node graph that no longer matches the accepted plan
- **THEN** the boundary still runs the accepted hook, because the plan binds its input by digest and
  the removed comparison added no evidence

#### Scenario: Plan is self-sufficient for preparation

- **WHEN** an accepted plan is prepared on a worker that cannot evaluate imported code
- **THEN** every file and payload the Node needs before boot is present in the plan, and preparation
  succeeds without imported evaluation

### Requirement: Plans carry generated artifact content

Content a package generates during planning SHALL reach the Pod as content-addressed plan artifacts
carrying the planned path, mode, ownership, extended attributes, digest, and bytes. A plan MUST
declare the total generated-artifact size and MUST fail planning when that bound is exceeded.
Artifact material a package treats as sensitive SHALL be delivered through a Secret-backed source and
MUST NOT be placed in a ConfigMap-backed source.

#### Scenario: Generated configuration is planned

- **WHEN** a kind's preparation generates a configuration file
- **THEN** the plan carries the file's bytes and metadata, and preparation stages them without
  re-running the generator

#### Scenario: Generated content is bounded

- **WHEN** a plan's generated artifacts exceed the declared total bound
- **THEN** planning fails with a structured diagnostic naming the Node instead of producing a partial
  plan

#### Scenario: Sensitive artifact source

- **WHEN** a generated file derives from certificate or key material
- **THEN** its plan artifact uses the Secret-backed source and no byte of it appears in a
  ConfigMap-backed artifact or payload

#### Scenario: Device modified a planned file

- **WHEN** preparation finds a planned file whose current digest differs from its recorded staging
  digest on a persistent volume
- **THEN** the device-modified file is preserved, the skip is recorded, and the plan-borne bytes are
  not used to overwrite it

### Requirement: Every imported interface method is classified

The repository SHALL classify every method of each imported interface the planning boundary relies on
(`nodes.Node`, `nodes.NodeOverwrites`, and any other interface the boundary type-asserts) as
`exercised`, `refused`, or `unused`, and each non-`exercised` classification MUST state a reason and
the affected kinds. Verification SHALL fail when a method is unclassified, and when a method
classified `exercised` is no longer invoked by the planning boundary. Refused methods SHALL be
reported as incompatibilities by conformance rather than counting as coverage.

#### Scenario: Imported interface gains a method

- **WHEN** the pinned containerlab release adds a method to an interface the boundary relies on
- **THEN** verification fails until that method is classified, and the failure names the method

#### Scenario: Classification no longer matches the adapter

- **WHEN** the planning boundary stops invoking a method classified `exercised`
- **THEN** verification fails instead of accepting stale coverage

#### Scenario: Method is refused

- **WHEN** a method is classified `refused` with a reason and affected kinds
- **THEN** conformance reports those kinds as incompatible for that behavior and does not treat the
  requirement as satisfied

