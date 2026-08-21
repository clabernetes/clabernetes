# direct-runtime-conformance Specification

## Purpose
Define the evidence required to claim complete kind and behavior compatibility for the direct Kubernetes runtime.

## Requirements

### Requirement: Compatibility matrix is an executable release gate

The compatibility report SHALL be generated from every kind and alias in the live imported registry and join those names with generic scenario results, image availability observations, and recorded status at verification time. c9s MUST NOT commit expected kind rows or require a matching per-kind fixture. A kind SHALL be reported compatible only when all applicable planning, boot, readiness, management, dataplane, lifecycle, and cleanup evidence passes for the declared baseline.

#### Scenario: Verify matrix completeness

- **WHEN** release verification runs
- **THEN** every registry kind and every supported topology behavior maps to conformance evidence and no entry is implicitly compatible

#### Scenario: Evidence is missing

- **WHEN** a kind lacks a required fixture, scenario, or current validation record
- **THEN** the release gate reports it as incompatible and fails any claim of full direct-runtime parity

### Requirement: Obtainable images have automated deployment coverage

Every generally obtainable test image SHALL have automated direct-Pod boot, management, and applicable dataplane coverage. The suite SHALL include representative native-container, VM-backed, and component-based devices, including Linux, Nokia SR Linux, Nokia SR OS or SR-SIM, Arista cEOS, a Cisco XR/vrnetlab family, and a Juniper VM family when required images and licenses are available.

#### Scenario: Test an obtainable image

- **WHEN** CI or the authorized integration environment has access to a generally obtainable baseline image
- **THEN** automated tests boot it directly and verify its applicable matrix behaviors

#### Scenario: Test architecture families

- **WHEN** the full vendor suite runs with required images
- **THEN** native, VM-backed, and component-based representative devices pass boot and dataplane checks

### Requirement: Legacy runtime repairs require direct evidence

A launcher- or nested-Docker-specific repair SHALL NOT be migrated merely because it existed in the previous runtime. Conformance MUST first run the affected device as an unmodified direct application container using imported containerlab behavior and the ordinary direct networking contract. For SR Linux, this evidence SHALL include management addressing, DNS resolution, exposure-Service reachability, and external reachability. If the direct observations pass, the old repair MUST be removed without replacement. If they fail, evidence MUST identify the missing generic runtime capability before implementation, and no result obtained through kind- or vendor-specific c9s logic may satisfy the gate.

#### Scenario: Historical repair is unnecessary in a direct Pod

- **WHEN** the direct device passes every observation that the nested repair previously protected
- **THEN** conformance records the direct evidence and requires no replacement plan action or helper behavior

#### Scenario: Direct Pod exposes a real capability gap

- **WHEN** the unmodified direct device fails an applicable management, DNS, Service, or external-reachability observation
- **THEN** conformance remains failed until a kind-opaque generic capability is specified, implemented once, and rerun successfully

### Requirement: Restricted images have repeatable recorded evidence

Commercial, private, export-controlled, or license-gated kinds SHALL have documented, repeatable conformance scenarios with pinned image identity, required secret inputs, expected observations, and dated validation evidence. Such a kind MUST NOT be marked compatible from manifest rendering or an unexecuted procedure alone.

#### Scenario: Validate a commercial kind

- **WHEN** an authorized environment runs its documented scenario against the pinned image identity
- **THEN** the resulting boot, management, dataplane, lifecycle, and cleanup evidence is recorded against the matrix entry

#### Scenario: Evidence is stale after behavior changes

- **WHEN** its device plan, relevant runtime behavior, or baseline version changes
- **THEN** prior recorded evidence becomes insufficient until the scenario is rerun

### Requirement: Multi-worker recovery suite covers runtime lifecycle and cleanup

The full conformance suite SHALL exercise cross-worker traffic, all Link flavors, live Link changes, partial topology updates, Pod deletion, Pod rescheduling, controller restart, connectivity-component restart, failure recovery, and complete cleanup. Every run SHALL use task-scoped namespaces, releases, owners, and labels. Once evidence is recorded, iterative runs MUST remove their completed or failed planning Pods, Jobs, stale workloads, and superseded diagnostics. Final acceptance MUST remove every task-created namespace and cluster resource unless a specific retained diagnostic is reported, and cleanup MUST NOT select unrelated completed Pods or user workloads.

#### Scenario: Run the recovery matrix

- **WHEN** the authorized multi-worker suite runs
- **THEN** traffic and readiness recover after every declared disruption and no owned runtime state remains after cleanup

#### Scenario: Clean an iterative validation run

- **WHEN** a task-scoped run has recorded the evidence needed for diagnosis or acceptance
- **THEN** its completed and failed Pods, Jobs, stale workloads, and superseded diagnostics are removed by task ownership without touching unrelated resources

#### Scenario: Finish full acceptance

- **WHEN** the final conformance run completes
- **THEN** every task-created namespace and cluster resource is absent, except for diagnostics whose exact identity and reason for retention are reported

### Requirement: User workflows are behaviorally equivalent

Direct Node and Link manifests, Topology compilation, and clabverter output SHALL produce equivalent device plans and running labs for the same representable topology. Startup configuration, licenses, persistence, management reachability, DNS, Services, probes, exec, logs, save, events, and packet capture SHALL have direct-runtime conformance coverage.

#### Scenario: Compare entry paths

- **WHEN** the same representable lab is submitted through each supported entry path
- **THEN** normalized plans are equivalent and the running labs pass the same applicable observations

#### Scenario: Input is not portable

- **WHEN** any entry path receives semantics the direct runtime cannot represent
- **THEN** it rejects them before workload creation with equivalent structured diagnostics

### Requirement: Documentation claims follow conformance state

User-facing compatibility documentation SHALL be generated from or verified against the matrix and SHALL identify every intentional semantic difference. It MUST NOT claim parity for an incompatible, untested, or stale kind or behavior.

#### Scenario: Publish compatibility documentation

- **WHEN** documentation verification or release packaging runs
- **THEN** documented kind status and limitations match the executable matrix


### Requirement: Sidecar connectivity conformance is executable release evidence

Sidecar-owned connectivity SHALL have per-kind executable conformance evidence covering: the device observing its allocated management address after adopting the synthetic interface, preservation of Pod transport throughout device boot and restart, outbound translation for the kind's traffic shape, inbound declared-port reachability with a real protocol session, cross-Pod fabric on the preserved underlay, and cleanup on Pod deletion including forced deletion. For same-namespace kinds the evidence SHALL additionally cover transport and fabric survival across device rewrites of shared namespace state. For topologies with host Links, evidence SHALL cover worker-side veth placement and its automatic disappearance with the Pod.

Kinds without recorded evidence SHALL be documented as unvalidated for the daemonless runtime; documentation MUST NOT claim their compatibility.

#### Scenario: Kind passes the sidecar connectivity matrix

- **WHEN** a supported kind's sidecar connectivity conformance run passes
- **THEN** the recorded evidence names the kind, image, adoption behavior, translation results, fabric results, and cleanup outcome

#### Scenario: Unvalidated kind is documented

- **WHEN** a kind has no recorded sidecar connectivity evidence
- **THEN** compatibility documentation lists it as unvalidated rather than claiming support
