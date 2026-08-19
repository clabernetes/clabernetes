## Purpose

Define direct, restart-safe connectivity for all supported Link flavors across grouped Pods and multiple Kubernetes workers.

## ADDED Requirements

### Requirement: Every Link flavor has one direct realization

The runtime SHALL directly realize same-Pod, loopback, host, and cross-Pod Links with the requested endpoint names and MTU. Cross-Pod transports terminate in the worker host network namespace, owned by the node-local daemon: the device receives a plain veth leg, so a kind that takes ownership of the Pod's primary interface cannot disturb the transport. The `vxlan` and `slurpeeth` connectivity values remain accepted input and both select this controller-owned realization (a local host-namespace patch when both endpoints share a worker, a node-addressed VTEP otherwise). No Link flavor MAY use a nested network-device container.

#### Scenario: Realize each supported flavor

- **WHEN** a valid Link selects VXLAN, slurpeeth, same-Pod, loopback, or host connectivity
- **THEN** the declared interfaces and dataplane are realized in the endpoint namespaces with the requested MTU

#### Scenario: Device owns the Pod primary interface

- **WHEN** a device kind renames or re-addresses the Pod's primary interface at boot
- **THEN** cross-Pod Link transports keep working because they terminate outside the Pod network namespace

#### Scenario: Flavor cannot be realized

- **WHEN** cluster or Node capabilities cannot satisfy the selected flavor
- **THEN** the Link reports a specific failure and no partially connected substitute is created

### Requirement: Required interfaces exist before device boot

For kinds whose compatibility plan requires interfaces at process start, the runtime SHALL create correctly named endpoint interfaces before the corresponding device application container starts. Initial connectivity readiness MUST be observable and MUST gate application start or readiness as declared by the plan.

#### Scenario: Cold-start a strict vendor kind

- **WHEN** a Node plan requires all dataplane interfaces before its device process starts
- **THEN** every accepted Link endpoint is present with its final name and MTU before that process starts

#### Scenario: Remote endpoint is not ready

- **WHEN** a cross-worker peer is absent during initial realization
- **THEN** the local interface is prepared as required, connectivity remains unready, and reconciliation completes after the peer appears

#### Scenario: Inspect connectivity helper authority

- **WHEN** a direct workload and its connectivity helper identity are rendered
- **THEN** the helper receives only namespace-scoped Link observation and Pod-log read authority, has no image-import or workload-mutation permission, and device containers receive no Kubernetes credential

### Requirement: Link reconciliation is live and idempotent

Creating, changing, rewiring, or deleting a Link SHALL converge from current state without recreating a device Pod unless the affected kind's declared link-apply mode requires restart or recreation. Repeated reconciliation MUST neither duplicate interfaces nor leak tunnel, process, route, or allocation state.

For an imported `Live` transition, c9s SHALL treat unchanged non-interface planning input as proof that freshly emitted container or artifact differences are creation-time consequences of the new endpoint inventory. The running Pod SHALL retain its accepted cold-only state while the connectivity revision applies the new interfaces and interface-wait actions. A later legitimate recreation SHALL use the complete current cold plan. c9s MUST NOT recognize a kind, vendor, environment name, file, or template to make this decision.

#### Scenario: Add a live Link

- **WHEN** a Link is added to running Nodes whose plans support live link application
- **THEN** traffic can pass without recreating their device containers

#### Scenario: Rewire an endpoint

- **WHEN** a Link changes one endpoint or connectivity flavor
- **THEN** obsolete state is removed and only the new endpoints converge

#### Scenario: Kind requires lifecycle action

- **WHEN** a changed Link terminates on a kind whose plan declares restart or recreation
- **THEN** c9s performs exactly that declared action and reports it through Kubernetes events and status

### Requirement: Cross-worker connectivity survives scheduling changes

Cross-worker Links SHALL derive peer identity from current Pod placement and SHALL converge after Pod deletion, Pod IP change, rescheduling, worker loss, or controller and connectivity-component restart. Link identity and allocations MUST remain bounded in Link status and MUST not depend on stale Pod-local state.

#### Scenario: Endpoint Pod is rescheduled

- **WHEN** one endpoint Pod is recreated on another worker with a different Pod IP
- **THEN** both ends update their dataplane state and traffic resumes without recreating the unaffected device Pod unless its plan requires it

#### Scenario: Connectivity component restarts

- **WHEN** a responsible controller, node agent, CNI component, or Pod helper restarts
- **THEN** it reconstructs desired state from Kubernetes resources and removes stale local state

### Requirement: Connectivity cleanup is ownership safe

Every interface, tunnel, helper process, route, and allocation created for a Link SHALL carry or derive stable ownership sufficient for exact cleanup. Deleting a Link, Node, or Pod MUST remove only its owned connectivity state and MUST tolerate already-absent state.

#### Scenario: Delete a Link

- **WHEN** a realized Link is deleted
- **THEN** all state owned exclusively by that Link is removed from both endpoints and every worker

#### Scenario: Delete one Node

- **WHEN** one Node is deleted while unrelated labs use the same workers
- **THEN** its Link state is removed without modifying unrelated namespaces, interfaces, tunnels, or allocations

### Requirement: Connectivity maintains namespace isolation

Standalone Nodes SHALL have distinct network namespaces. Only explicitly grouped Nodes and components whose device plans declare namespace sharing MAY share a Pod namespace. Connectivity components MUST scope operations to the resolved Pod UID, container namespace, Node UID, and Link UID so name reuse cannot mutate a replacement or unrelated workload.

#### Scenario: Node name is reused

- **WHEN** a Node and Pod are deleted and replacements are created with the same names but different UIDs
- **THEN** stale connectivity is removed and cannot be attached to the replacements without new resolution
