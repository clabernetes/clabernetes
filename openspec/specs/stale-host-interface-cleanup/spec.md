## Purpose

Recover launcher deployments by removing stale host-side veth interfaces left in the pod network
namespace after a failed containerlab deployment.

## Requirements

### Requirement: Identify rendered host interfaces

The launcher SHALL inspect the rendered containerlab topology before deployment and identify every
interface endpoint whose node is the reserved `host` node.

#### Scenario: Host endpoints are extracted

- **WHEN** the rendered topology contains `router1:eth2` and `host:router1-eth2`
- **THEN** the launcher identifies `router1-eth2` as a cleanup candidate

#### Scenario: Non-host endpoints are ignored

- **WHEN** a link contains only nested node endpoints
- **THEN** none of those node interfaces are selected for pod-namespace cleanup

#### Scenario: Topology parsing fails

- **WHEN** the rendered topology cannot be read or parsed
- **THEN** the launcher emits a warning and does not delete any interface

### Requirement: Normalize candidate interface names

The launcher SHALL apply the same interface-name normalization used by containerlab when converting
host endpoint names into Linux interface names.

#### Scenario: Slash-containing interface is normalized

- **WHEN** a host endpoint is `host:sr1-1/1/c1/1`
- **THEN** the cleanup candidate is `sr1-1-1-c1-1`

#### Scenario: Duplicate candidates are collapsed

- **WHEN** the topology refers to the same normalized host interface more than once
- **THEN** the launcher checks and processes that interface at most once

### Requirement: Remove stale host interfaces before deployment

Before invoking `containerlab deploy`, the launcher SHALL check each normalized candidate in the pod
network namespace and SHALL delete it when it already exists.

#### Scenario: Stale interface is removed

- **WHEN** a topology-derived host interface exists before deployment
- **THEN** the launcher deletes that interface before invoking containerlab

#### Scenario: Missing interface is ignored

- **WHEN** a topology-derived host interface does not exist
- **THEN** the launcher performs no deletion for that interface and proceeds to deployment

#### Scenario: Cleanup failure does not suppress deployment

- **WHEN** an existing candidate cannot be deleted
- **THEN** the launcher emits a warning and still attempts the containerlab deployment

#### Scenario: Cleanup precedes deployment

- **WHEN** stale host interfaces are present
- **THEN** all cleanup checks and deletion attempts occur before the containerlab deploy command

### Requirement: Protect pod plumbing

The launcher MUST never delete protected pod interfaces, including `lo`, `eth0`, and `docker0`,
even when they appear as host endpoints in the topology.

#### Scenario: Protected interface is skipped

- **WHEN** the topology contains `host:eth0`
- **THEN** the launcher does not check it for deletion and does not invoke `ip link delete` for it

#### Scenario: Other topology-derived interface remains eligible

- **WHEN** the topology contains both `host:eth0` and `host:router1-eth2`
- **THEN** `eth0` is protected while `router1-eth2` is checked and removed if present
