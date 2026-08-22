# node-lifecycle — Delta Specification

## MODIFIED Requirements

### Requirement: Node status contains only per-node observations and allocations

The controller SHALL record Node readiness, plan identity, exposed-port allocations, standard
conditions, direct container observations, and applied NodeProfile identity in Node status.
User intent and full plans MUST NOT be stored in status. Migration-era observation fields
(`probeStatuses`) SHALL NOT exist: probe outcomes surface through conditions and direct
container observations.

#### Scenario: NodeProfile is applied

- **WHEN** a Node is successfully realized with a referenced NodeProfile
- **THEN** status records the applied profile identity (name, uid, generation) and
  `NodeProfileResolved=True`
