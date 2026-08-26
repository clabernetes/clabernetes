# direct-connectivity — Delta Specification

## MODIFIED Requirements

### Requirement: Every Link flavor has one Pod-local realization

The runtime SHALL directly realize same-Pod, loopback, host, and cross-Pod Links with the
requested endpoint names and MTU, entirely from within the Pod: cross-Pod transports terminate
inside the Pod network namespace on the sidecar-preserved Kubernetes underlay, and host Links
place one veth end into the worker network namespace through the sidecar's read-only
host-namespace handle. The device receives a plain interface leg and never owns the transport
underlay, so a kind that adopts its presented interfaces cannot disturb any transport. The
realization is derived from endpoint shape alone; Links carry no connectivity selector. No Link
flavor MAY use a nested network-device container and no Link flavor MAY require a node-resident
agent.

#### Scenario: Realize each supported flavor

- **WHEN** a valid Link resolves to the cross-Pod, same-Pod, loopback, or host flavor
- **THEN** the declared interfaces and dataplane are realized in the endpoint namespaces with
  the requested MTU by the Pod's own connectivity sidecar

#### Scenario: Rewire an endpoint

- **WHEN** a Link changes one endpoint
- **THEN** obsolete state is removed and only the new endpoints converge
