## Purpose

Define a deterministic c9s device-plan contract derived from an unmodified, version-pinned containerlab Go dependency and its live imported registry.

## ADDED Requirements

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

Planning SHALL depend only on explicit versioned inputs, including the resolved Node definition, image metadata, payload metadata, certificate material metadata, management allocation, and interface inventory. Imported hook execution MUST occur in a short-lived, deadline-bounded c9s planning Pod rather than the manager process. The worker MUST have no service-account token, host path, privileged security context, or ambient capability; MUST use a read-only root filesystem plus private scratch; and MUST audit imported calls through a recorder boundary. It MAY retain only `CHOWN` and `FOWNER` after dropping `ALL`, because imported generic preparation records package-owned ownership and ACL metadata and every writable path is confined to private scratch. It MUST NOT pull an image, launch or inspect a running container, access an implicit host path, create a privileged network namespace, or mutate host or runtime state. Identical normalized inputs MUST produce byte-equivalent normalized plans.

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
