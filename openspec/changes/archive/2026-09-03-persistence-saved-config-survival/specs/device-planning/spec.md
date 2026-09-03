## MODIFIED Requirements

### Requirement: Device planning is deterministic and side-effect free

Planning SHALL depend only on explicit versioned inputs, including the resolved Node definition, image metadata, payload metadata, certificate material metadata, management allocation, and interface inventory. Imported hook execution MUST occur in a short-lived, deadline-bounded c9s planning Pod rather than the manager process. The worker MUST have no service-account token, host path, privileged security context, or ambient capability; MUST use a read-only root filesystem plus private scratch; and MUST audit imported calls through a recorder boundary. It MAY retain only `CHOWN` and `FOWNER` after dropping `ALL`, because imported generic preparation records package-owned ownership and ACL metadata and every writable path is confined to private scratch. It MUST NOT pull an image, launch or inspect a running container, access an implicit host path, create a privileged network namespace, or mutate host or runtime state. Identical normalized inputs MUST produce byte-equivalent normalized plans.

Preparation SHALL keep proving reproduction: regenerated artifacts MUST match the accepted plan by path, mode, and digest before any publication. Publication onto a persistent artifact volume SHALL be state-aware: preparation records the digest of every artifact it publishes, and on later runs it MUST NOT overwrite a planned file whose current digest differs from the digest recorded at its last staging, unless the node definition enforces its startup configuration or a device-state reset was requested. A planned file whose current digest still matches its recorded staging digest SHALL be republished when the plan's content changed. On non-persistent volumes publication remains unconditional.

#### Scenario: Repeat a plan

- **WHEN** the same normalized planning inputs are evaluated more than once
- **THEN** the normalized serialized plans are byte-equivalent

#### Scenario: Required input is unavailable

- **WHEN** a kind decision requires image or payload metadata that was not supplied
- **THEN** planning returns a structured missing-input error instead of consulting a local Docker daemon or guessing a default

#### Scenario: Imported code bypasses the runtime interface

- **WHEN** an imported hook attempts direct filesystem, network, namespace, or host access outside the controlled generic boundary
- **THEN** the locked-down planning worker denies or confines the attempt, returns no accepted plan, and cannot mutate the manager or Kubernetes worker node

#### Scenario: Imported preparation applies filesystem metadata

- **WHEN** an imported hook creates directories, changes ownership, or applies bounded extended attributes inside its controlled workspace
- **THEN** the plan records those generic artifacts and metadata digests and preparation reproduces them without identifying the emitting kind

#### Scenario: Imported endpoint lifecycle has a post-deployment fixup

- **WHEN** an imported Node implements endpoint deployment and post-deployment hooks
- **THEN** the connectivity worker invokes both hooks in package order through the same controlled generic namespace/runtime boundary

#### Scenario: Imported lifecycle follows application logs

- **WHEN** an imported post-deployment hook requests a log stream for one of its planned containers
- **THEN** a Pod-UID- and plan-scoped generic broker streams that direct Kubernetes container's logs without exposing Kubernetes credentials or kind knowledge to the application

#### Scenario: Preparation finds a device-modified planned file

- **WHEN** preparation runs on a persistent artifact volume and a planned file's current digest differs from the digest recorded at its last staging
- **THEN** the regenerated artifact is still verified against the accepted plan, but the device-modified file is left in place and the skip is recorded

#### Scenario: Staging ledger is missing or unreadable

- **WHEN** preparation runs on a persistent volume that holds prior artifacts but no readable staging ledger, for example after an upgrade from a release without the ledger
- **THEN** planned files whose content differs from the plan are treated as device-modified and preserved, a fresh ledger is established, and the condition is reported
