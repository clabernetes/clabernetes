## MODIFIED Requirements

### Requirement: Preparation helpers do not become a runtime

c9s MAY use init containers, sidecars, CNI components, or node agents to stage files, apply lifecycle actions, reconcile connectivity, and report status. Helpers MUST use the finite containers declared by the device plan, MUST NOT create or supervise nested device containers, MUST NOT evaluate imported containerlab code, and MUST expose failure through Pod or Node conditions and events.

#### Scenario: Prepare kind-specific files

- **WHEN** a plan requires generated configuration, certificates, licenses, or downloaded payloads before boot
- **THEN** a c9s preparation component stages and verifies the planned artifacts without generating them, and the affected application container starts from those staged artifacts

#### Scenario: Helper fails

- **WHEN** preparation or a required lifecycle action fails
- **THEN** the device does not report ready and Kubernetes-visible status identifies the failed helper and operation

#### Scenario: Preparation has no imported code path

- **WHEN** the preparation helper is inspected or exercised
- **THEN** it contains no node registry, kind initialization, kind hook, or entropy session, and it cannot execute containerlab behavior

#### Scenario: Lifecycle hook fails

- **WHEN** an imported lifecycle hook fails inside a helper
- **THEN** the failure names the operation, its phase, and the affected logical Node, and the device does not report ready

### Requirement: Lifecycle observation uses Kubernetes and plan-defined probes

Node readiness SHALL derive from application-container status, required component status, completed preparation and connectivity, and plan/profile-defined startup and readiness probes. Docker inspection and Docker health status MUST NOT be used. Save, events, packet capture, post-start commands, and imported post-deploy, readiness, and endpoint behavior SHALL be executed against direct containers and interfaces through the accepted plan's declared action. The lifecycle boundary MUST NOT re-derive the plan's deployment stream, and MUST NOT replay or diff imported operations to decide whether to run the accepted hook.

#### Scenario: Device starts successfully

- **WHEN** all required containers are running, initialization and connectivity are complete, and configured probes succeed
- **THEN** the Pod and every represented Node report ready

#### Scenario: Device container restarts

- **WHEN** an application container terminates and Kubernetes restarts it
- **THEN** the restart is visible in its container status and the represented Node remains unready until the direct readiness contract succeeds again

#### Scenario: Run a lifecycle operation

- **WHEN** a user requests exec, save, event inspection, or packet capture for a compatible Node
- **THEN** c9s addresses the direct application container or planned interface without invoking containerlab or Docker

#### Scenario: Post-deploy behavior runs the accepted hook

- **WHEN** a plan declares post-deploy behavior for a kind
- **THEN** the lifecycle boundary invokes that hook against the live direct container without re-deriving the plan's deployment stream

#### Scenario: Save behavior runs the accepted hook

- **WHEN** a user or policy saves device state for a kind whose save behavior came from an imported hook
- **THEN** the boundary invokes the save hook against the live direct container

#### Scenario: Readiness observes the running device

- **WHEN** a plan's readiness behavior depends on device output
- **THEN** the readiness boundary observes the live device through the imported check and reports readiness only when it succeeds, without replaying construction operations
