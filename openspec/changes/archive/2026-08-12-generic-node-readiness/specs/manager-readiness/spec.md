## ADDED Requirements

### Requirement: Manager rollout readiness reflects controller startup

The manager Deployment SHALL expose readiness through the existing `/alive` HTTPS endpoint, and
the endpoint SHALL reflect the manager's existing readiness state: an elected leader becomes ready
after controller-runtime startup completes, while a non-leader may become ready after another
leader is observed.

#### Scenario: Manager is still starting

- **WHEN** the manager process is running but its controller-runtime cache has not synchronized or
  its controllers have not finished registering
- **THEN** `/alive` returns a non-success response and the manager Deployment is not ready

#### Scenario: Manager startup is complete

- **WHEN** the manager has synchronized its controller-runtime cache and completed controller
  registration
- **THEN** `/alive` returns success and Kubernetes may mark the manager Deployment ready

#### Scenario: Manager is not the elected leader

- **WHEN** the manager observes another replica as the elected leader
- **THEN** `/alive` returns success for that replica so the Deployment can keep it available

#### Scenario: Manager process fails its health endpoint

- **WHEN** the manager's `/alive` readiness probe fails
- **THEN** Kubernetes removes the manager Pod from ready endpoints and the Deployment rollout does
  not count that Pod as available
