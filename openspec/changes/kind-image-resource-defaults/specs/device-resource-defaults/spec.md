# Spec Delta

## Purpose

Defines predictable resource defaults for direct device containers, including global kind and image selection, explicit profile overrides, supported-kind recommendations, and safe adoption by existing installations.

## ADDED Requirements

### Requirement: Global resource defaults select explicit profile, image, kind, then generic policy

Config SHALL provide `spec.deployment.resourcesByKind`, `spec.deployment.resourcesByImage`, and `resourcesDefault`. For each logical primary container, policy SHALL resolve in that precedence order only after giving a non-nil effective NodeProfile resource block highest priority. Image rules SHALL outrank kind rules so image-specific variants remain configurable.

#### Scenario: Explicit NodeProfile wins

- **WHEN** a Node's effective NodeProfile declares resources and both global maps contain matching entries
- **THEN** the workload uses the NodeProfile resource block rather than either global entry

#### Scenario: Topology resource override wins

- **WHEN** a Topology declares default or node-specific resources and global maps match one of its emitted Nodes
- **THEN** the generated NodeProfile supplies the explicit resource block and the global entries do not replace it

#### Scenario: Image rule precedes kind rule

- **WHEN** a primary container matches one image rule and its logical Node has an exact kind entry
- **THEN** the workload uses the image entry

#### Scenario: Kind rule precedes generic default

- **WHEN** no explicit profile or image rule applies and the declared kind has an exact entry
- **THEN** the workload uses the kind entry rather than `resourcesDefault`

#### Scenario: Generic default is the final fallback

- **WHEN** no explicit profile, image rule, or kind rule applies and `resourcesDefault` is non-nil
- **THEN** the workload uses `resourcesDefault`

### Requirement: Resource layers use whole-block selection

The system SHALL select exactly one profile, image, kind, or generic resource block rather than deep-merging quantities across layers. The selected block MUST retain existing per-quantity overlay behavior against planned requirements. An explicitly present empty block SHALL suppress lower-priority layers.

#### Scenario: Partial profile does not inherit a lower quantity

- **WHEN** a profile declares only a CPU request and a matching kind entry declares memory
- **THEN** the selected profile block does not inherit the kind entry's memory request

#### Scenario: Empty profile suppresses global defaults

- **WHEN** an effective NodeProfile explicitly sets `resources: {}` and global defaults would otherwise match
- **THEN** no global resource block is applied to its primary containers

#### Scenario: Empty matching map entry suppresses lower defaults

- **WHEN** the highest-priority matching kind or image entry is `{}` and lower-priority global layers are non-empty
- **THEN** the empty entry is selected and lower-priority policy is not applied

#### Scenario: Selected block overlays only declared quantities

- **WHEN** a selected block declares a CPU request and the plan declares a memory limit
- **THEN** the primary container receives the CPU request and retains the planned memory limit

### Requirement: Kind defaults match declared kind names exactly

Kind selection SHALL use a case-sensitive exact match against the logical Node's declared containerlab kind. Kind keys MUST NOT be interpreted as expressions, and valid aliases SHALL remain distinct lookup keys rather than silently sharing one entry.

#### Scenario: Exact declared kind matches

- **WHEN** a Node declares kind `nokia_srlinux` and that exact key exists
- **THEN** the corresponding kind resource entry is eligible for selection

#### Scenario: Alias requires its own entry

- **WHEN** a Node declares `srl` and only `nokia_srlinux` has a kind entry
- **THEN** the `nokia_srlinux` entry is not selected for the `srl` Node

#### Scenario: Different case does not match

- **WHEN** a Node declares a kind whose case differs from every configured kind key
- **THEN** no kind entry matches and resolution proceeds to the generic fallback

### Requirement: Image defaults use full source-reference regular expressions

Image keys SHALL be RE2 regular expressions matched against the complete planned primary-container source image reference, including its repository and tag. Matching MUST NOT use a digest-resolved reference, an auxiliary image, glob semantics, or arbitrary regexp-length priority.

#### Scenario: Tag-qualified expression matches

- **WHEN** an image expression fully matches the primary container's source reference including its tag
- **THEN** that image entry is eligible for selection

#### Scenario: Similar auxiliary image does not select policy

- **WHEN** an auxiliary or internal container image matches an expression but the logical primary source image does not
- **THEN** that expression does not supply the logical primary's resource policy

#### Scenario: Partial expression does not match accidentally

- **WHEN** an expression matches only a substring but cannot match the complete source reference under full-reference anchoring
- **THEN** it is not selected

### Requirement: Invalid or ambiguous image configuration fails before workload mutation

The system SHALL reject an invalid image expression and SHALL reject resolution when more than one image expression matches. Ambiguity errors SHALL identify all matching expressions deterministically; the system MUST NOT fall back to a less suitable default or mutate the affected workload.

#### Scenario: Invalid expression

- **WHEN** global resolution encounters an invalid RE2 expression for a Node without explicit profile resources
- **THEN** reconciliation reports a configuration error before creating or updating that Node's workload

#### Scenario: Multiple matching expressions

- **WHEN** two different image expressions fully match one primary source image
- **THEN** reconciliation fails before workload mutation and reports both expressions in deterministic order

#### Scenario: Explicit profile bypasses unused invalid global rules

- **WHEN** a Node has non-nil explicit profile resources and the global image map contains an invalid expression
- **THEN** profile resolution uses the explicit block without consulting the global image map

### Requirement: Defaults resolve independently for logical primary containers

Each logical Node's declared primary application container SHALL receive resource policy resolved from its own profile, kind, and source image. Non-primary and internal component containers MUST retain imported plan requirements rather than inheriting this policy.

#### Scenario: Mixed-kind grouped Nodes

- **WHEN** two logical Nodes share one Pod and have different kind defaults
- **THEN** each logical primary container receives its own selected kind entry

#### Scenario: Non-primary component retains planned resources

- **WHEN** a plan contains a primary application container and a non-primary component container
- **THEN** only the primary receives the selected profile/global block and the component retains its imported requirements

#### Scenario: Distributed component plan

- **WHEN** a component-based kind contains multiple declared logical primary and internal containers
- **THEN** resource policy follows the plan's primary-container identities rather than container order

### Requirement: Shipped defaults cover supported kinds with documented requests

The default installation configuration SHALL define positive CPU and memory requests for every supported containerlab kind and alias, with explicitly documented exclusions for kinds that cannot become direct workloads. Defaults MUST NOT include CPU or memory limits, and every value SHALL have documented provenance.

#### Scenario: Inspect supported kind defaults

- **WHEN** a user inspects the shipped default configuration for a supported kind or alias
- **THEN** it contains positive CPU and memory requests and no CPU or memory limits

#### Scenario: Containerlab adds a supported kind

- **WHEN** the pinned containerlab registry gains a new kind that direct workloads can realize
- **THEN** default coverage validation fails until its requests or an explicit documented exclusion is added

#### Scenario: Review a derived VM default

- **WHEN** a user consults documentation for a VM-backed kind
- **THEN** the documented request identifies the vCPU/RAM basis and accounts for container/helper overhead rather than equating VM RAM with the complete Pod request

#### Scenario: Use a large hardware variant

- **WHEN** a hardware variant needs resources beyond its kind's documented baseline
- **THEN** documentation directs the user to an image rule or NodeProfile instead of presenting the baseline as a universal minimum

### Requirement: Bootstrap adoption does not silently resize existing installations

Fresh Config creation SHALL receive shipped kind and image defaults. Bootstrap merge into an existing Config MUST leave each nil feature map unset; once a feature map is explicitly present, merge SHALL add absent shipped entries without replacing user entries. Overwrite mode SHALL retain replacement semantics.

#### Scenario: Fresh installation

- **WHEN** bootstrap creates the initial Config from shipped values
- **THEN** the Config contains the shipped kind and image maps

#### Scenario: Existing Config without resource selector maps

- **WHEN** bootstrap merge processes an existing Config whose kind and image maps are both nil
- **THEN** both maps remain nil and the existing effective generic-default behavior is preserved

#### Scenario: Existing installation opts into kind defaults

- **WHEN** an existing Config has a non-nil kind map and bootstrap later supplies additional kind entries
- **THEN** absent shipped keys are added and existing user-valued keys are preserved

#### Scenario: Existing installation has only image rules

- **WHEN** bootstrap merge processes an existing Config with a non-nil image map and nil kind map
- **THEN** image entries merge without causing the kind map to become populated automatically

#### Scenario: Overwrite is explicit

- **WHEN** an administrator selects overwrite merge mode
- **THEN** bootstrap replacement uses the supplied resource-default maps according to existing overwrite semantics

### Requirement: Global resource changes reconcile affected workloads

Changes to the singleton global Config SHALL enqueue direct workloads so their selected resources can be recomputed without waiting for an unrelated Node event. Explicit profile resource blocks MUST continue to win during that reconciliation.

#### Scenario: Global kind request changes

- **WHEN** an administrator changes a kind request used by a running Node
- **THEN** that Node reconciles and its rendered primary-container request changes to the new selected value

#### Scenario: Profile remains explicit after Config event

- **WHEN** a global Config update enqueues a Node with explicit profile resources
- **THEN** reconciliation retains the profile resource block rather than replacing it with a global match

#### Scenario: Global Config is deleted

- **WHEN** the singleton Config is deleted
- **THEN** affected Nodes reconcile using the documented empty-Config fallback rather than retaining stale global resource defaults indefinitely
