## Purpose

Define Kubernetes-native realization of network devices as directly managed application containers without an inner container runtime or containerlab executable.

## ADDED Requirements

### Requirement: Device containers are first-class Pod containers

Every network-device image SHALL run as an application container in a c9s-managed Pod created from its device plan. A device Pod MUST NOT contain or access dockerd, a Docker socket used to launch devices, the containerlab executable, or any helper capable of launching a nested network-device container. The kubelet and cluster CRI SHALL own device image pulling, start, stop, restart, logs, resource accounting, and container status.

#### Scenario: Inspect a running Node

- **WHEN** a user inspects the Pod for a realized Node
- **THEN** the device image appears in `spec.containers` and its running state appears directly in `status.containerStatuses`

#### Scenario: Use Kubernetes device operations

- **WHEN** a user obtains logs, executes a command, or reads metrics for the device container through Kubernetes
- **THEN** the operation targets the actual device process without traversing a launcher or nested runtime

#### Scenario: Package lifecycle follows device logs

- **WHEN** an imported lifecycle hook requests its planned application container's log stream
- **THEN** the stream comes from the kubelet-owned Pod log endpoint through a target-scoped helper, while the device container receives no service-account token

#### Scenario: Inspect runtime dependencies

- **WHEN** a device Pod and its mounted sockets and binaries are inspected
- **THEN** no Docker daemon, device-launch Docker socket, containerlab executable, or nested network-device container is present

### Requirement: One logical Node owns one direct workload

A standalone Node SHALL own one workload containing its planned device application container. Nodes grouped through `network-mode: container:<primary>` SHALL be application containers in the primary Node's workload and SHALL share its Pod network namespace. Deleting or replacing one primary SHALL reconcile the whole group, while unrelated Nodes remain independent.

#### Scenario: Realize a standalone Node

- **WHEN** a valid standalone Node has a complete direct device plan
- **THEN** the controller creates one workload whose application container is that Node's device image

#### Scenario: Realize a grouped secondary

- **WHEN** a secondary Node names a valid primary through container network mode
- **THEN** both Nodes are distinct application containers in the primary workload and share the Pod network namespace

#### Scenario: Group membership changes

- **WHEN** a secondary is added to, removed from, or moved between groups
- **THEN** affected workloads are recreated from complete plans without changing unrelated workloads

### Requirement: Component Nodes remain directly visible

A component-based Node SHALL remain one logical c9s Node while every planned component runs as a named application container in its workload. The plan SHALL define component identity and namespace sharing; all required components MUST contribute to logical readiness and MUST be individually visible in Kubernetes container status, logs, resources, and exec.

#### Scenario: Boot a distributed chassis

- **WHEN** one Node plan contains a control plane and multiple component containers
- **THEN** Kubernetes starts each component directly and Node readiness requires every required component and the declared application probe

#### Scenario: Component fails

- **WHEN** one required component terminates or becomes unready
- **THEN** the component failure is visible in Pod status and the logical Node becomes unready

### Requirement: Preparation helpers do not become a runtime

c9s MAY use init containers, sidecars, CNI components, or node agents to stage files, apply lifecycle actions, reconcile connectivity, and report status. Helpers MUST use the finite containers declared by the device plan, MUST NOT create or supervise nested device containers, and MUST expose failure through Pod or Node conditions and events.

#### Scenario: Prepare kind-specific files

- **WHEN** a plan requires generated configuration, certificates, licenses, or downloaded payloads before boot
- **THEN** a c9s preparation component stages and verifies them before the affected application container starts

#### Scenario: Helper fails

- **WHEN** preparation or a required lifecycle action fails
- **THEN** the device does not report ready and Kubernetes-visible status identifies the failed helper and operation

### Requirement: Kubernetes realizes container policy natively

The direct workload SHALL translate planned image, pull secrets, pull policy, entrypoint, command, environment, sysctls, capabilities, privilege, security profiles, devices, tmpfs, shared memory, resources, DNS, mounts, and persistence into Kubernetes-native fields or a documented portable c9s behavior. Input that cannot be represented MUST be rejected before workload creation and MUST NOT be ignored.

#### Scenario: Pull a private image

- **WHEN** a Node uses a private image and its profile supplies pull secrets
- **THEN** the kubelet pulls the device image using `imagePullSecrets` without importing it through a launcher

#### Scenario: Apply security and resources

- **WHEN** a plan declares supported capabilities, privilege, security profiles, devices, sysctls, shared memory, tmpfs, or resource policy
- **THEN** the corresponding application container and Pod fields enforce that policy

#### Scenario: Policy is not portable

- **WHEN** a requested Docker bind or security option has no defined safe Kubernetes representation
- **THEN** the Node is rejected before its workload is created rather than starting with weaker semantics

### Requirement: Direct management and service behavior is preserved

The runtime SHALL preserve planned management IPv4 and IPv6 intent, DNS, hostname, certificate reachability, and exposure Services while keeping Kubernetes control-plane networking functional. Static address requests MUST either be realized exactly by the supported networking contract or rejected before workload creation.

#### Scenario: Assign management intent

- **WHEN** a Node requests supported static management IPv4 or IPv6 addresses
- **THEN** the device observes those addresses on the planned management interface and its Service remains reachable

#### Scenario: Expose device ports

- **WHEN** a Node declares supported destination ports
- **THEN** Services target the actual device Pod and traffic reaches the planned device port without Docker port publication

### Requirement: Lifecycle observation uses Kubernetes and plan-defined probes

Node readiness SHALL derive from application-container status, required component status, completed preparation and connectivity, and plan/profile-defined startup and readiness probes. Docker inspection and Docker health status MUST NOT be used. Save, events, packet capture, and post-start commands SHALL target direct containers and interfaces.

#### Scenario: Device starts successfully

- **WHEN** all required containers are running, initialization and connectivity are complete, and configured probes succeed
- **THEN** the Pod and every represented Node report ready

#### Scenario: Device container restarts

- **WHEN** an application container terminates and Kubernetes restarts it
- **THEN** the restart is visible in its container status and the represented Node remains unready until the direct readiness contract succeeds again

#### Scenario: Run a lifecycle operation

- **WHEN** a user requests exec, save, event inspection, or packet capture for a compatible Node
- **THEN** c9s addresses the direct application container or planned interface without invoking containerlab or Docker
