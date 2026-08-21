## ADDED Requirements

### Requirement: Plans carry a vendor-neutral management-interposition profile

For every supported kind, device planning SHALL derive a management-interposition profile from the pinned containerlab dependency and carry it in the runtime-neutral plan. The profile SHALL declare at least: the interface name and MAC behavior the device expects for its management port and the management gateway inputs for generated configuration.

Consumers of the profile (renderer, sidecar, controllers) MUST NOT contain kind- or vendor-conditional behavior; all vendor variance SHALL be expressed through profile data, and universal hardening (checksum offload, forwarding scoping, translation precedence, state re-assertion) SHALL be unconditional runtime baseline rather than profile flags. Where the pinned containerlab version does not expose a needed fact declaratively, the fact SHALL live only in the version-pinned compatibility layer of device planning and SHALL be tracked as an upstream containerlab contribution.

#### Scenario: Profile is derived, not hardcoded

- **WHEN** a supported kind's plan is produced
- **THEN** its interposition profile is derived from the pinned containerlab registry or the version-pinned compatibility layer, and no component outside device planning branches on kind or vendor identity to realize interposition

#### Scenario: Kind declares no explicit management interface

- **WHEN** a kind's evaluated containerlab configuration exposes no explicit management interface name
- **THEN** the profile uses containerlab's primary-interface contract, matching what the kind would observe under containerlab

#### Scenario: Pinned dependency changes

- **WHEN** the pinned containerlab version is updated
- **THEN** registry-driven conformance verifies every supported kind still yields a complete interposition profile, and any drift fails the compatibility gate before workloads are affected

### Requirement: Management artifacts render from allocated identities at plan time

Management-parameterized configuration SHALL render from controller-allocated management inputs during planning. Runtime completion MUST NOT synthesize a management identity from the Pod address; its only runtime contribution to management rendering is Pod-resolver DNS discovery. A plan whose management inputs are incomplete for any node SHALL fail planning with a diagnostic naming the node rather than degrading to a Pod-derived identity.

#### Scenario: Startup configuration is rendered

- **WHEN** a kind renders management-parameterized startup configuration
- **THEN** the render uses the allocated management address, prefix, and gateway available at plan time and is byte-identical between planning and preparation

#### Scenario: Allocation is missing

- **WHEN** planning encounters a node without a complete allocated management identity
- **THEN** planning fails closed with a diagnostic naming the node and the missing allocation
