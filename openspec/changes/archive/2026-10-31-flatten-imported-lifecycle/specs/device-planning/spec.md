## ADDED Requirements

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

## MODIFIED Requirements

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
