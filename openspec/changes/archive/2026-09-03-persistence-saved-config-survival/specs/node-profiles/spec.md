## MODIFIED Requirements

### Requirement: NodeProfile owns realization policy

The system SHALL retain the namespaced NodeProfile resource as the reusable Kubernetes realization policy for direct Node workloads. It SHALL support Pod and primary application-container resources and scheduling, persistence, Kubernetes-native image pull policy and secrets, exposure behavior, and operational probes. Persistence policy SHALL include enablement, claim size, storage class, and a claim retention setting whose default garbage-collects the claim with its Node and whose retained setting lets the claim survive Node deletion for reattachment by an equivalent recreated Node. Kind-owned security, privilege, devices, component layout, and required resources SHALL come from the imported package plan and MUST NOT be overridden through launcher-era policy. NodeProfile MUST NOT configure a launcher image, inner runtime, Docker daemon, containerlab version, image-import path, or CRI socket.

#### Scenario: Reuse one profile policy

- **WHEN** multiple Nodes explicitly reference one NodeProfile
- **THEN** each Node's direct workload is realized using the same declared policy

#### Scenario: Profile declares claim retention

- **WHEN** the effective persistence policy enables retention and the Node is deleted
- **THEN** the claim and its data survive, and a recreated Node with the same identity and compatible persistence policy reattaches it
