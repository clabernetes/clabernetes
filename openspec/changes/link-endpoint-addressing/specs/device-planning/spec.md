# Spec Delta

## ADDED Requirements

### Requirement: Plans preserve imported endpoint addressing
Device planning SHALL validate and carry each interface's optional IPv4/IPv6 CIDR prefixes as explicit normalized input and accepted-plan data. Planning and imported-hook reconstruction SHALL supply the same prefixes to containerlab's generic endpoints without c9s kind dispatch.

#### Scenario: Imported kind consumes cold-plan addresses
- **WHEN** an accepted interface carries valid addresses and the imported kind consumes containerlab endpoint addresses during planning or cold start
- **THEN** its ordinary generated configuration or lifecycle behavior observes exactly those prefixes

#### Scenario: Reconstruct imported endpoint addressing
- **WHEN** an imported post-deploy or endpoint hook is reconstructed from accepted input and plan data
- **THEN** its generic endpoints expose the same IPv4/IPv6 prefixes accepted during planning

#### Scenario: Kind does not consume endpoint addresses
- **WHEN** an imported kind ignores containerlab endpoint addresses
- **THEN** c9s preserves that upstream behavior and does not synthesize netlink, CLI, or vendor-specific address configuration

#### Scenario: Reject invalid planned addresses
- **WHEN** normalized interface input or an accepted plan contains a malformed prefix or a prefix from the wrong family
- **THEN** validation rejects it with a structured error before the address reaches imported configuration behavior

#### Scenario: Address input remains deterministic
- **WHEN** equivalent explicit inputs, including interface addresses, are planned repeatedly
- **THEN** normalized plans and generated artifacts remain byte-equivalent, while an address difference changes the relevant input and plan identities
