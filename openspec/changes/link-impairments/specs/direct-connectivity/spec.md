## ADDED Requirements

### Requirement: The connectivity sidecar programs endpoint impairments

The connectivity sidecar SHALL program each declared endpoint impairment as a root `netem` qdisc
on an interface c9s owns that carries the endpoint's packets through the kernel, so no device can
bypass it: for a cross-Pod wire, the sidecar of the receiving Pod SHALL program it on its own
sidecar leg, which the wire delivers the endpoint's packets through; for a link within one Pod
or a host link, which has no sidecar leg, the endpoint's sidecar SHALL program it on the endpoint
interface, in the namespace that interface lives in. The sidecar SHALL re-assert it on every pass
while the Pod runs. The sidecar MUST mark the qdiscs it installs so that it removes only its own
impairment when a Link stops declaring one, and MUST NOT remove a root qdisc it did not install.

#### Scenario: Device sends around its interface's qdisc

- **WHEN** a cross-Pod Link declares an impairment on the endpoint of a device that sends around its own interface's qdisc, such as SR-SIM
- **THEN** the peer Pod's sidecar programs it on its sidecar leg and the device's packets are impaired

#### Scenario: Link is recabled

- **WHEN** a Link with an impaired endpoint is recabled so that its other endpoint moves to another Node, or turns between a local link and a cross-Pod wire
- **THEN** the sidecar that now carries the endpoint's packets programs the impairment, and the sidecar that no longer does removes its own

#### Scenario: Interface is recreated

- **WHEN** an impaired endpoint interface or sidecar leg is recreated while the Pod runs
- **THEN** the sidecar programs the impairment on the new interface on its next pass

#### Scenario: Impairment is removed

- **WHEN** a Link stops declaring an impairment the sidecar programmed
- **THEN** the sidecar removes its qdisc, and a root qdisc the device or a user installed by hand is left in place
