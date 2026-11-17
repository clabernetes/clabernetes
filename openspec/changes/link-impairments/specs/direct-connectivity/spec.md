## ADDED Requirements

### Requirement: The connectivity sidecar programs endpoint impairments

The connectivity sidecar SHALL program each declared endpoint impairment as the root `netem`
qdisc of the endpoint interface, in the namespace the interface lives in, and SHALL re-assert it
on every pass while the Pod runs. The sidecar MUST mark the qdiscs it installs so that it removes
only its own impairment when a Link stops declaring one, and MUST NOT remove a root qdisc it did
not install.

#### Scenario: Interface is recreated

- **WHEN** an impaired endpoint interface is recreated while the Pod runs
- **THEN** the sidecar programs the impairment on the new interface on its next pass

#### Scenario: Impairment is removed

- **WHEN** a Link stops declaring an impairment the sidecar programmed
- **THEN** the sidecar removes its qdisc, and a root qdisc the device or a user installed by hand is left in place
