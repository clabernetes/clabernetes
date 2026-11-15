# Design

## Context

The strict Topology compiler owns containerlab link parsing, but `compiler/linkConfig` currently carries only endpoints, labels, vars, and MTU. `CompiledLink`, `LinkEndpointSpec`, `InterfaceInput`, and `InterfacePlan` likewise stop at node/interface identity. During planning, `evaluateInterfacesWithRuntimeState` constructs a containerlab `EndpointGeneric` without its optional IP prefixes. These are independent gaps; fixing only the parser would still lose the values before imported kind initialization.

Containerlab consumes endpoint addresses through imported kind behavior, not through one universal address-assignment operation. SR Linux renders them into generated interface configuration, cEOS consumes them during post-deploy configuration, and several other kinds have their own handling. The repository requires the pinned module and its kind knowledge to remain unmodified.

A second constraint is the Live connectivity projection: for eligible structural changes it deliberately retains cold-only container and artifact state. Simply adding addresses to interface inputs would therefore make address-only edits appear successful without replacing generated running configuration. The specs make that transition explicitly recreation-only. See the specification deltas for the external requirements.

## Goals / Non-Goals

**Goals:**

- Use one canonical per-endpoint representation from both supported containerlab syntaxes through cold-start kind configuration.
- Fail closed on malformed data at direct-resource, compiler, and normalized-plan boundaries.
- Detect effective address differences before Live projection and route affected Nodes through the existing recreate lifecycle.
- Keep comparison semantic, so a different textual spelling of the same prefix does not recreate a Node.
- Preserve direct-resource/compiled-Topology equivalence and all existing persistence protections.

**Non-Goals:**

- Live CLI, gNMI, netlink, or whole-interface address reconciliation.
- New imported kind adapters, upstream containerlab changes, dependencies, or c9s kind-name switches.
- Automatic persistent-state deletion or implicit device-state reset.
- Support for unrelated structured-endpoint fields such as `mac` or `vars`, or multiple prefixes per family on one endpoint.

## Decisions

### 1. Normalize source syntax into per-endpoint API fields

Add optional `ipv4` and `ipv6` strings to the compiled endpoint and `LinkEndpointSpec`. Link-level arrays are mapped by endpoint index after the normal two-endpoint checks; empty entries mean no address. Structured veth endpoints decode the same two fields directly. Both forms then share the existing compilation, sanitization, naming, rendering, and reconciliation path.

Use `netip.ParsePrefix` plus an address-family check for validation. A bare IP is invalid, while `/31`, `/32`, `/127`, and `/128` are valid. Never call `Masked()`. Direct Links use the same rules through the shared Link validation path. Address fields on the reserved `host` endpoint are rejected because no logical Node owns their imported configuration.

Keep the original strings in the user-facing Link spec, but parse and canonicalize with `Prefix.String()` at normalized device-plan boundaries. This retains host bits while making IPv6 compression and other equivalent spellings compare by effective value. The optional JSON additions do not require a new CRD API version or a device-plan schema-version bump; existing empty values retain their previous serialization and behavior.

**Alternative considered:** store containerlab-style address arrays on `LinkSpec`. Rejected because structured endpoints are naturally per-endpoint, direct resources would expose a less uniform model, and lifecycle evaluation already operates per interface.

### 2. Carry addresses as first-class planning data

Extend `InterfaceInput` and `InterfacePlan` with optional IPv4/IPv6 fields. `compileAcceptedInterfaces` copies and canonicalizes each local endpoint's values from the accepted Link. The mapper preserves them in accepted interface plans, and codec validation repeats family/CIDR checks so hand-crafted or stale planner input fails closed.

Including the values in both normalized input and plan keeps the accepted plan self-describing, includes addresses in input/plan identity, and lets transition evaluation compare typed values instead of artifact contents or kind names. Reconstruction already consumes normalized interface input, so no separate runtime address channel is needed.

**Alternative considered:** put addresses only in `InterfaceInput`. Rejected because duplicating established interface intent such as MTU into the accepted plan makes change validation and introspection more consistent and avoids treating addresses as hidden planner-only metadata.

### 3. Populate imported generic endpoints once

In `evaluateInterfacesWithRuntimeState`, create the `EndpointGeneric`, assign its parsed optional prefixes, and then wrap it as the veth endpoint passed to `AddEndpoint`. The same function serves cold planning and pre-realized imported-hook reconstruction, so both paths expose identical address intent.

No direct-runtime netlink operation is added. Imported kinds decide whether and how endpoint addresses contribute to generated artifacts or lifecycle actions; kinds that ignore them retain upstream behavior. Generated configuration therefore remains plan-borne and replay-free during preparation.

**Alternative considered:** infer configuration commands in c9s or replay a kind's complete post-deploy hook. Rejected because this duplicates kind knowledge, has broad side effects, is not available uniformly across kinds, and violates the generic imported-behavior boundary.

### 4. Classify address changes before selecting a connectivity transition

Compare base and desired normalized `InterfaceInput` values by their stable interface ID (`Link UID` plus endpoint side). An added or removed interface with a nonempty old or new address set is also an address change. Compare canonical prefixes as values, not their source strings.

For each affected endpoint Node, upgrade the transition to `LinkApplyRecreate` whenever the effective address pair differs, regardless of the kind's structural `LinkApplyMode`. This check occurs before `NewConnectivityRevision` or retained-Live projection. Combined address and structural/MTU changes therefore take one recreate path and need no special address action.

The controller's planning input contains only endpoints terminating on that Node or shared workload group, which scopes recreation to the changed side rather than automatically recreating the peer. Existing grouping rules still apply when multiple logical Nodes share a device Pod. With no base workload, addresses are ordinary cold input and cause no extra transition. If both canonical address pairs are equal, they add no lifecycle effect.

**Alternative considered:** document address edits as unsupported while allowing ordinary Live evaluation. Rejected because it would acknowledge the new plan but leave generated running configuration stale. Silently retaining the Pod is worse than an explicit recreate limitation.

### 5. Leave persistence and compatibility behavior intact

Address-driven recreation changes workload lifecycle only. It does not set `enforce-startup-config`, request a device-state reset, or alter staging-ledger rules. Unmodified planned artifacts follow the new plan, while device-modified persistent artifacts retain their current protections and documented re-seeding controls.

Existing address-free Links and plans gain empty fields and no transition difference. Generate API artifacts from source; do not edit generated files directly. Installation must update CRDs before new addressed Links are submitted. Rollback of an addressed lab requires first removing the new fields or otherwise returning to a definition the older binaries can represent.

## Risks / Trade-offs

- **Different validation at compiler, API, and planner boundaries could drift** → use one family/CIDR validation rule and test the same valid and invalid corpus at each boundary.
- **Kinds that ignore endpoint IPs may accept them without observable configuration** → document that this is containerlab kind behavior and test a kind that actually consumes them rather than inventing generic assignment.
- **Persistent saved configuration can outrank newly planned cold configuration** → preserve data-loss protections and document the existing enforced-startup/device-state-reset escape hatches instead of erasing state on an address edit.
- **A shared or overly broad comparison could recreate an unchanged peer** → compare canonical values by stable interface ID and add one-sided and grouped-workload lifecycle tests.
- **Adding fields to both input and plan can produce unexpected Live rejection** → unit-test transition evaluation before Live projection for addition, change, removal, equivalent spelling, and non-address MTU/structural changes.
- **Structured decoding can silently discard fields it does not model** → model `ipv4`/`ipv6` explicitly and retain strict rejection for every other unrepresentable field.

## Migration Plan

1. Install the regenerated CRDs and controller binaries together through the normal release process; stored address-free resources need no migration.
2. Allow directly authored Links to use the new fields immediately after upgrade.
3. Enable compiler acceptance on the same build, so no previously valid source definition changes meaning. Definitions containing `links[].ipv4`/`ipv6` change from rejected to supported.
4. Verify an address-free rolling upgrade produces no normalized-input or lifecycle difference. Address-bearing plans are valid only at the new schema level with the new binary.
5. For rollback, first remove addressed Link intent or recreate the previous address-free configuration, then install the older release; do not rely on old planners to consume new plan documents containing unknown fields.

## Open Questions

None.
