# direct-connectivity — Delta Specification

## MODIFIED Requirements

### Requirement: Management identity is allocated and realized Pod-locally

The direct runtime SHALL realize containerlab's always-addressed management model with
controller-allocated identities: every logical Node's management address comes from the topology's
management policy, or, when no policy is declared, from containerlab's default management subnet
and gateway convention. The runtime MUST NOT use the Pod address as a management identity. The
Pod's connectivity sidecar SHALL interpose a synthetic management interface carrying the allocated
address before any device container starts, and management configuration SHALL render at plan time
through each kind's own imported templates using the allocated address, prefix, and sidecar
gateway. The interposed management interface SHALL be a member of the topology's management L2
mesh, so the allocated identity is reachable from every peer device by address, and the sidecar
SHALL maintain mesh peer state on the same reconciliation tick that re-asserts its other owned
state.

#### Scenario: Imported hook dials the management address

- **WHEN** an imported package hook running inside an application container dials the logical
  Node's management address after the device adopted the interface presented to it
- **THEN** the dial reaches the device's management plane pod-locally without any kind- or
  vendor-specific handling

#### Scenario: Operator declares a management policy

- **WHEN** the operator allocates explicit management addresses through a management policy
- **THEN** the controller-allocated addresses are used unchanged

#### Scenario: Topology declares no management policy

- **WHEN** a topology is deployed without a management policy
- **THEN** the controller allocates each node's management identity from containerlab's default
  management subnet with containerlab's gateway convention, and devices observe those addresses
  exactly as they would under containerlab

#### Scenario: Package templates render the management identity

- **WHEN** an imported kind generates configuration from its own management-parameterized templates
- **THEN** the render uses the allocated management address, prefix, and sidecar gateway at plan
  time, so a topology with no startup-config reaches full management without kind-specific handling

#### Scenario: Peer device dials the allocated identity

- **WHEN** a peer device in the same topology dials the allocated management address from its own
  Pod
- **THEN** the dial reaches this device over the management mesh without translation, and the
  reply returns the same way
