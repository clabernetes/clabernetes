# device-state-persistence Specification

## Purpose

Defines the contract for device-written state on persistent artifact volumes across Pod replacement: when startup configuration seeds a device, when saved configuration wins, and how users re-seed or reset a Node.

## Requirements

### Requirement: Device-written files survive Pod replacement

On a persistent artifact volume, a planned generated file that the device has modified since it was last staged SHALL be left in place by preparation on subsequent Pod starts. A planned file whose current content still matches what preparation last staged SHALL be re-staged so declared spec updates propagate. This contract MUST NOT depend on the format of the startup configuration: a full-file startup configuration (for example SR Linux JSON) and a CLI-format startup configuration yield the same survival behavior for saved device configuration.

#### Scenario: Saved configuration survives a Pod restart

- **WHEN** a persistent Node seeded from a full-file startup configuration commits a configuration change, runs the Save lifecycle, and its Pod is deleted or evicted
- **THEN** the replacement Pod boots from the saved configuration and the committed change is present

#### Scenario: Saved configuration survives a declared spec change

- **WHEN** a persistent Node with saved configuration is replaced because an unrelated field of its declared spec changed
- **THEN** the replacement Pod boots from the saved configuration, not from the startup configuration in the changed spec

#### Scenario: Unmodified artifacts follow the spec

- **WHEN** a planned generated file on the persistent volume still has the content preparation last staged and the plan produces different content for it
- **THEN** preparation re-stages the file with the planned content

#### Scenario: Device writes outside planned paths

- **WHEN** the device writes files at paths inside plan-owned mounted directories that no planned artifact occupies
- **THEN** preparation leaves those files in place, as before

### Requirement: Enforced startup configuration re-seeds on every start

When a node definition declares `enforce-startup-config`, preparation SHALL re-stage startup-configuration-derived artifacts on every Pod start, overwriting device-written content at those paths. Declaring `enforce-startup-config` without a startup configuration MUST be rejected before workload creation with a structured diagnostic, matching containerlab semantics.

#### Scenario: Enforced startup config overrides saved state

- **WHEN** a persistent Node declares `enforce-startup-config` with a startup configuration, saves configuration changes, and its Pod is replaced
- **THEN** the replacement Pod boots from the declared startup configuration

#### Scenario: Enforce without startup configuration

- **WHEN** a node definition declares `enforce-startup-config` but no startup configuration
- **THEN** the Node is rejected before workload creation with a structured diagnostic

### Requirement: Device-state reset re-seeds one Node on request

The system SHALL provide a per-Node reset operation that returns the Node's persistent artifact state to a freshly seeded state: device-written files under the plan-owned artifact tree are removed and planned artifacts are staged as on first boot. Reset MUST NOT require deleting the Node resource or its claim, MUST affect only the targeted Node, and MUST be observable through the Node's status or events.

#### Scenario: Reset a persistent Node

- **WHEN** a user requests a device-state reset for one persistent Node that has saved configuration
- **THEN** the Node's next boot uses the declared startup configuration and the reset is reported on the Node

#### Scenario: Reset does not leak to siblings

- **WHEN** a device-state reset is requested for one Node of a Topology
- **THEN** other Nodes keep their persistent state

### Requirement: Save without persistence warns visibly

Running the Save lifecycle for a Node whose artifact volume is not persistent SHALL succeed but MUST surface a visible warning in the Save output that the saved configuration will not survive Pod replacement. Device Pods hold no Kubernetes credentials, so the warning channel is the Save output itself.

#### Scenario: Save on an ephemeral Node

- **WHEN** the Save lifecycle runs against a Node without persistence enabled
- **THEN** the save completes and its output carries a warning naming the Node and stating that the saved configuration is lost when the Pod is replaced

### Requirement: Ephemeral Nodes reproduce declared state on every start

Without persistence, every Pod start SHALL reproduce exactly the declared spec: planned artifacts are staged fresh and no device-written state carries over. This existing behavior is the documented default and MUST NOT change.

#### Scenario: Ephemeral Pod replacement

- **WHEN** a Node without persistence saves configuration and its Pod is replaced
- **THEN** the replacement Pod boots from the declared startup configuration

### Requirement: Claim retention can outlive the Node

Persistence policy SHALL support a retention setting. With the default setting the claim is garbage-collected with its Node, as today. With retention enabled the claim survives Node deletion, and a recreated Node with the same identity and compatible persistence policy SHALL reattach the retained claim and boot from its preserved state.

#### Scenario: Default retention deletes the claim

- **WHEN** a Node with default persistence policy is deleted
- **THEN** its claim and data are garbage-collected

#### Scenario: Retained claim reattaches

- **WHEN** a Topology whose effective persistence policy enables retention is deleted and an equivalent Topology is recreated
- **THEN** each recreated Node reattaches its retained claim and boots from the saved configuration
