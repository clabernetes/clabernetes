## MODIFIED Requirements

### Requirement: Plans carry a vendor-neutral management-interposition profile

For every supported kind, device planning SHALL derive a management-interposition profile from the
pinned containerlab dependency and carry it in the runtime-neutral plan. The profile SHALL declare
at least: the interface name and MAC behavior the device expects for its management port, the
management gateway inputs for generated configuration, the inbound port translations for the
Pod-address path, and the management mesh membership — the mesh tunnel identifier and the
deterministic gateway link-layer identity — all supplied to planning as controller-allocated
data. The profile MUST NOT carry a peer-discovery transport name: the peer set reaches the
sidecar through the published peer directory, not through the plan.

Consumers of the profile (renderer, sidecar, controllers) MUST NOT contain kind- or
vendor-conditional behavior; all vendor variance SHALL be expressed through profile data, and
universal hardening (checksum offload, forwarding scoping, translation precedence, ARP responder
scoping, gateway containment, state re-assertion) SHALL be unconditional runtime baseline rather
than profile flags. Where the pinned containerlab version does not expose a needed fact
declaratively, the fact SHALL live only in the version-pinned compatibility layer of device
planning and SHALL be tracked as an upstream containerlab contribution.

#### Scenario: Profile is derived, not hardcoded

- **WHEN** a supported kind's plan is produced
- **THEN** its interposition profile is derived from the pinned containerlab registry or the
  version-pinned compatibility layer, and no component outside device planning branches on kind or
  vendor identity to realize interposition

#### Scenario: Kind declares no explicit management interface

- **WHEN** a kind's evaluated containerlab configuration exposes no explicit management interface
  name
- **THEN** the profile uses containerlab's primary-interface contract, matching what the kind
  would observe under containerlab

#### Scenario: Pinned dependency changes

- **WHEN** the pinned containerlab version is updated
- **THEN** registry-driven conformance verifies every supported kind still yields a complete
  interposition profile, and any drift fails the compatibility gate before workloads are affected

#### Scenario: Mesh membership is planned data

- **WHEN** a namespace-owning Node with an allocated management identity is planned
- **THEN** its interposition profile carries the controller-allocated mesh tunnel identifier and
  the deterministic gateway identity, and planning rejects incomplete or invalid mesh input
  before any workload is created
