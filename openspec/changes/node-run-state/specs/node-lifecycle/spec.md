## ADDED Requirements

### Requirement: Node run state is operational state

A Node SHALL declare an operational run state, `Running` or `Stopped`, defaulting to `Running`. `Stopped` SHALL scale the Node's direct workload to zero Pods while the Node, its workload object, its Services, its PersistentVolumeClaims, and the Links terminating on it remain. Setting the run state back to `Running` SHALL restore exactly one Pod realizing the current plan. The run state MUST NOT enter the device plan.

#### Scenario: Stop a Node

- **WHEN** a user sets a running Node's run state to `Stopped`
- **THEN** its workload Pod is removed, its Node, workload object, Services, PersistentVolumeClaim, and Links remain, its readiness is `notready`, and its `Stopped` condition becomes `True` once no Pod of the workload remains

#### Scenario: Links to a stopped Node go down

- **WHEN** a Node linked to running peers is stopped
- **THEN** the peers keep running and observe loss of carrier on the linked interfaces, and carrier returns once the Node runs again

#### Scenario: Start a stopped Node

- **WHEN** a user sets a stopped Node's run state to `Running`
- **THEN** one Pod realizing the Node's current plan starts, the `Stopped` condition becomes `False` with reason `Running`, and the Node becomes ready when its containers do

### Requirement: Grouped Nodes follow the primary's run state

For grouped Nodes, the primary Node's run state SHALL govern the shared workload, and a secondary Node's run state SHALL have no effect.

#### Scenario: Stop a grouped primary

- **WHEN** the primary Node of a group is stopped
- **THEN** the shared Pod is removed and every Node of the group reports `Stopped` as `True`

#### Scenario: Stop a grouped secondary

- **WHEN** only a secondary Node of a running group is set to `Stopped`
- **THEN** the shared Pod keeps running

### Requirement: Stopped Nodes keep reconciling

The Node and Topology controllers SHALL keep reconciling while Nodes are stopped. A change to a stopped Node's definition, its Links, or its profile SHALL be planned and rendered into its workload, and the Node SHALL stay stopped. Startup admission SHALL neither wait for nor count stopped groups, and a stopped group that starts again SHALL be admitted like a new workload.

#### Scenario: Apply a Topology while a Node is stopped

- **WHEN** a Topology whose emitted Node is stopped is changed so that the Node's definition changes
- **THEN** the Node is updated to the compiled definition, its plan is re-derived, it stays stopped, and starting it boots the updated definition

#### Scenario: Stopped group under startup admission

- **WHEN** startup batching is enabled and one group is stopped
- **THEN** the stopped group does not take a batch slot or block other groups, and starting it waits for admission

### Requirement: Emitters preserve operational Node state

An emitter SHALL NOT set a Node's run state, and the Topology controller SHALL treat a difference in run state as conforming. When it updates a drifted Node, it SHALL carry over the run state and pending `c9s.run/restart-requested` and `c9s.run/device-state-reset` requests.

#### Scenario: Run state alone differs from the compiled Node

- **WHEN** an emitted Node differs from the compiled Node only in its run state
- **THEN** the Topology controller does not update the Node

#### Scenario: A stopped Node drifts

- **WHEN** the Topology controller updates a drifted Node that is stopped and carries a restart request
- **THEN** the updated Node is still stopped and still carries the request

### Requirement: Restart is requested by token

A Node SHALL accept the annotation `c9s.run/restart-requested` with an opaque token. The controller SHALL replace the Node's workload Pod once for each token that differs from the last token it handled for that Node, and SHALL report the token the current Pod was started with in `status.observedRestartToken`. Removing the annotation or setting a token already handled MUST NOT replace the Pod.

#### Scenario: Request a restart

- **WHEN** a user sets `c9s.run/restart-requested` on a running Node to a new token
- **THEN** the Pod is replaced once and `status.observedRestartToken` equals the token once the replacement Pod is observed

#### Scenario: Restart a stopped Node

- **WHEN** a user sets a new token on a stopped Node and later starts it
- **THEN** the started Pod handles the token and is not replaced again for it

#### Scenario: Restart a grouped Node

- **WHEN** a user sets a new token on any Node of a group
- **THEN** the shared Pod is replaced once

#### Scenario: Remove a handled restart request

- **WHEN** a user removes the annotation after its token was handled
- **THEN** the Pod is not replaced

### Requirement: Device Pods identify their containerlab node

A direct device Pod SHALL carry the `c9s.run/topologyNode` label naming its primary Node and, when the primary Node carries `c9s.run/originalNodeName`, that label with the same value. No other `c9s.run/` Node label SHALL be copied onto the Pod.

#### Scenario: Select a Pod by containerlab node name

- **WHEN** a Topology node named `R1` is compiled to Node `r1`
- **THEN** its device Pod is selectable with `c9s.run/originalNodeName=R1`
