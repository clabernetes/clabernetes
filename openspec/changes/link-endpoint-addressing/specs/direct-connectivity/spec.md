# Spec Delta

## MODIFIED Requirements

### Requirement: Link reconciliation is live and idempotent

Creating, changing, rewiring, or deleting a Link SHALL converge from current state without recreating a device Pod unless the affected kind's declared link-apply mode requires restart or recreation or the effective IPv4/IPv6 configuration of one of its endpoints changes. Repeated reconciliation MUST neither duplicate interfaces nor leak tunnel, process, route, or allocation state.

For an imported `Live` transition that leaves endpoint addresses unchanged, c9s SHALL treat unchanged non-interface planning input as proof that freshly emitted container or artifact differences are creation-time consequences of the new endpoint inventory. The running Pod SHALL retain its accepted cold-only state while the connectivity revision applies the new interfaces and interface-wait actions. A later legitimate recreation SHALL use the complete current cold plan. c9s MUST NOT recognize a kind, vendor, environment name, file, or template to make this decision.

Any endpoint-address addition, modification, or removal on a realized Node SHALL require the existing recreate lifecycle even when its kind normally declares Live link application. Such a transition MUST NOT be accepted as a retained Live connectivity revision, and c9s SHALL NOT introduce live endpoint-address commands as part of this behavior.

#### Scenario: Add a live Link

- **WHEN** an unaddressed Link is added to running Nodes whose plans support live link application
- **THEN** traffic can pass without recreating their device containers

#### Scenario: Rewire an endpoint

- **WHEN** a Link changes one endpoint without changing either endpoint's address set
- **THEN** obsolete state is removed and only the new endpoints converge under the existing lifecycle rules

#### Scenario: Kind requires lifecycle action

- **WHEN** a changed Link terminates on a kind whose plan declares restart or recreation
- **THEN** c9s performs exactly that declared action and reports it through Kubernetes events and status

#### Scenario: Address changes on a Live-capable kind

- **WHEN** an already-realized Node's endpoint gains, changes, or loses an IPv4 or IPv6 prefix
- **THEN** c9s requires recreation instead of retaining the Pod through a Live connectivity revision, and the recreated workload consumes the complete current cold plan

#### Scenario: Address and structural properties change together

- **WHEN** one transition changes endpoint addresses as well as structural Link or MTU properties
- **THEN** c9s applies recreation for the affected Node and does not additionally run a live address operation

#### Scenario: Address input is unchanged

- **WHEN** replanning produces exactly the same effective endpoint IPv4 and IPv6 values as the base input
- **THEN** those values do not independently trigger recreation or alter the existing Live transition decision
