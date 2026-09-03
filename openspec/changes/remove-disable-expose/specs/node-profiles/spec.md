## MODIFIED Requirements

### Requirement: NodeProfile owns realization policy

The system SHALL retain the namespaced NodeProfile resource as the reusable Kubernetes realization
policy for direct Node workloads. It SHALL support Pod and primary application-container resources
and scheduling, persistence, Kubernetes-native image pull policy and secrets, exposure behavior,
and operational probes. NodeProfile exposure policy SHALL use `spec.expose.exposeType` as its sole
Service-mode control and MUST NOT expose a `disableExpose` field. Kind-owned security, privilege,
devices, component layout, and required resources SHALL come from the imported package plan and
MUST NOT be overridden through launcher-era policy. NodeProfile MUST NOT configure a launcher
image, inner runtime, Docker daemon, containerlab version, image-import path, or CRI socket.

#### Scenario: Reuse one profile policy

- **WHEN** multiple Nodes explicitly reference one NodeProfile
- **THEN** each Node's direct workload is realized using the same declared policy

#### Scenario: Reuse a disabled exposure policy

- **WHEN** multiple Nodes reference one NodeProfile with `spec.expose.exposeType: None`
- **THEN** none of those Nodes receives an expose Service
