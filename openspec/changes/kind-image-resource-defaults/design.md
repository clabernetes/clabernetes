# Design

## Context

`ConfigDeployment.ResourcesDefault` currently supplies one generic `ResourceRequirements` block, while `NodeProfileSpec.Resources` supplies an explicit replacement. `directPrimaryContainerResources` chooses the profile block when non-nil and the generic Config block otherwise. The direct renderer then overlays only declared resource names onto every logical primary container, leaving other planned requirements and non-primary component containers intact.

Topology resource entries already compile into shared or dedicated NodeProfiles. Containerlab kinds are registered by the pinned module, including aliases, and plans retain the declared kind and source image for each primary container. See proposal.md - Why for the user-facing problem.

## Goals / Non-Goals

**Goals:**

- Keep resource policy in the existing Config and NodeProfile APIs, with deterministic selection and empty-block behavior.
- Resolve defaults against normalized plan identity so kind-supplied default images and grouped Nodes work correctly.
- Make bad global matching configuration fail closed before workload mutation.
- Ship maintainable, documented request recommendations without confusing Kubernetes requests with VM sizing or hard limits.
- Preserve existing explicit-profile behavior and existing installations during upgrades.

**Non-Goals:**

- No kind/type-keyed hierarchy, per-component resource override API, or automatic derivation of requests from QEMU environment variables.
- No CPU or memory limits in shipped defaults and no resource enforcement beyond ordinary Kubernetes requests.
- No change to the existing precedence between the selected resource block and containerlab-derived request/limit quantities.
- No deep merge between profile, image, kind, and generic resource blocks.

## Decisions

### 1. Use separate exact-kind and image-regexp maps

Add these fields to `ConfigDeployment` and the bootstrap ConfigMap contract:

```yaml
resourcesDefault:
  requests: {cpu: 200m, memory: 512Mi}
resourcesByKind:
  nokia_srlinux:
    requests: {cpu: "2", memory: 2Gi}
resourcesByImage:
  '^ghcr\.io/nokia/srlinux:.*$':
    requests: {cpu: "2", memory: 3Gi}
```

Both new fields are `map[string]k8scorev1.ResourceRequirements`. Map values are non-pointers so an explicitly present `{}` is distinguishable from an absent key and selects an empty policy. `resourcesDefault` remains a pointer to preserve nil versus explicit empty.

Separate fields avoid guessing whether a string is a kind or expression and avoid collisions between the two namespaces. The new kind field is deliberately not named `resourcesByContainerlabKind`: the removed legacy field had a different nested kind/type contract.

Alternative considered: one ordered rule list with typed selectors and priorities. It handles overlapping expressions elegantly but adds unnecessary API and migration complexity for the requested kind/image maps.

### 2. Select one layer, then overlay declared quantities

Resolution uses this strict precedence:

1. non-nil `NodeProfile.spec.resources`;
2. the unique matching `resourcesByImage` entry;
3. an exact `resourcesByKind` entry;
4. non-nil `resourcesDefault`;
5. nil, meaning no additional resource policy.

Image rules outrank kind rules so custom image variants remain reachable after every kind receives a shipped default. A selected block is not deep-merged with lower layers. An explicit empty profile or map entry therefore suppresses lower policy.

After selection, the renderer retains its current per-resource-name overlay behavior. For example, selecting a block with only a CPU request does not erase a containerlab-derived memory limit. This is distinct from merging resource blocks across precedence layers.

Alternative considered: deep-merging each CPU/memory quantity through all layers. It would break current whole-field profile behavior and remove the clean empty-block mechanism for suppressing inherited values.

### 3. Make regexp matching deterministic and fail closed

Image keys are valid Go RE2 expressions, implicitly full-reference anchored as `^(?:expression)$`. Matching uses the primary container's source image reference from the normalized plan, including repository and tag, never its digest-resolved reference or an auxiliary image. Documentation recommends explicit anchors and escaped literals in examples even though the resolver supplies full-reference anchoring.

All matches are collected. Zero matches proceeds to kind resolution; exactly one selects that entry; multiple matches return an error containing the sorted expressions. Arbitrary regexp length, lexical order, and Go map iteration never determine precedence. Invalid expressions also return an error rather than being ignored. A Node with explicit profile resources does not consult global image rules and is not blocked by an otherwise invalid unused global map.

Errors follow the existing plan-input/preflight path and prevent create or mutation of the affected workload. Bootstrap parsing rejects malformed values where possible; direct Config edits surface through affected Node conditions/events.

Alternative considered: longest-pattern matching like `nodeSelectorsByImage`. Regexp source length is not semantic specificity, and equal-length overlaps would remain ambiguous.

### 4. Resolve global policy per logical primary container

Replace the single `PrimaryContainerResources` renderer option with a mapping keyed by planned primary container ID. After planning succeeds, the Node reconciler iterates plan Nodes, joins each declared primary container to its plan image, and resolves its policy independently. An explicit shared profile yields the same block for every member; global rules may yield different blocks for grouped Nodes of different kinds or images.

The renderer applies the selected map entry only to that primary container. Internal and non-primary component containers continue to use imported plan requirements. Resource policy does not enter the device plan or alter its digest because it is workload realization policy, not device identity.

Alternative considered: retaining one workload-wide selected block. It would make mixed-kind grouped Pods depend on primary group identity and silently give secondary Nodes the wrong kind/image default.

### 5. Match declared kind aliases exactly and test registry coverage

Kind lookup is exact and case-sensitive against the kind retained in the plan. There is no kind regexp or magic `default` key. Because containerlab aliases remain distinct plan values, shipped defaults include every supported alias rather than introducing a second canonicalization system.

A test enumerates `GetRegisteredNodeKindNames()` from the pinned registry and checks the parsed Helm defaults. Every kind reaching direct workload planning has a non-empty positive CPU and memory request. Unsupported/non-realizable registry entries require an explicit test exclusion with a documented reason. The same test rejects default limits and catches newly registered kinds after containerlab upgrades.

Alternative considered: canonicalizing aliases before lookup. It reduces duplicate YAML but adds registry-dependent behavior at the realization boundary and makes user-visible matching less direct.

### 6. Treat shipped values as reviewed scheduling recommendations

`charts/clabernetes/values.yaml` is the source of shipped default data. Every entry has documented provenance in the resource guide, including a link or reference to the relevant containerlab/vendor requirement:

- native-container kinds use documented requirements or an explicitly reviewed baseline;
- VM CPU requests derive from documented/default vCPU allocation;
- VM memory requests derive from documented/default VM RAM plus a stated QEMU/helper headroom;
- generic lightweight kinds use a documented small baseline;
- variants whose requirements differ materially from the baseline use NodeProfiles.

Containerlab `MinVCPU` and `MinAvailMemoryGb` are inputs to the review, not an automatic formula: they describe host preflight checks, and VM RAM excludes container overhead. Defaults request resources only; they do not set limits or retune QEMU environment variables.

### 7. Bootstrap new defaults without silently resizing existing labs

Fresh Config creation continues through replace mode and receives all shipped maps. Existing Config merge behavior is deliberately feature-aware:

- when `resourcesByKind` is nil, merge leaves it nil rather than injecting defaults that would outrank an existing `resourcesDefault`;
- when `resourcesByKind` is already non-nil, merge adds absent shipped kind keys without replacing user keys;
- when `resourcesByImage` is nil, merge leaves it nil; when non-nil, merge adds absent shipped image keys (if any) without replacing user keys;
- overwrite mode and fresh creation still replace the complete deployment configuration from bootstrap values.

This lets an existing administrator opt into each map explicitly. Once enabled, future chart upgrades can add new supported-kind entries. Upgrade documentation explains how to copy/review the shipped maps and warns that kind/image defaults take precedence over the generic fallback.

Alternative considered: ordinary map merge for existing installations. It is simpler but silently changes scheduling and can make existing Pods unschedulable on smaller workers.

### 8. Reconcile on global Config changes

Add a Node-controller watch for the singleton Config, filtered to its configured name/namespace. Relevant add/update/delete events enqueue all primary Nodes; existing group reconciliation realizes their members. The watch may conservatively enqueue for every Config change because these events are infrequent and global labels, metadata, pull policy, and selectors already affect realization.

Global resource resolution remains outside the plan digest. The normal rendered-Deployment conformance check detects and applies changed requests. Tests prove that a Config event reaches Nodes that do and do not reference profiles, while profile Nodes retain their explicit resource block.

### 9. Documentation describes one complete mental model

Update API comments, Helm values comments/schema, the Config and resource-management guides, and component-specific documentation together. Document precedence, whole-block selection, empty blocks, exact kind aliases, full-reference RE2 matching, ambiguity errors, per-primary/component scope, VM overhead, variant overrides, and upgrade opt-in.

The SR-SIM guide requires special reconciliation with the current primary-container wording: its statements about primary cards versus every card must be checked against normalized plans and covered by tests rather than perpetuating contradictory documentation.

## Risks / Trade-offs

- **[Large defaults make existing clusters unschedulable]** → Do not inject either feature map into an existing Config during merge; document capacity review and opt-in.
- **[Many kind aliases make the default map verbose]** → Prefer explicit data and exact matching; enforce alias coverage with the registry test.
- **[Overlapping regexps rejected where glob users expect prioritization]** → Emit sorted matching patterns and document kind/default fallbacks and single-expression alternation.
- **[A default cannot fit every hardware type, release, or image variant]** → Document the baseline and provenance; use image rules or NodeProfiles for variants rather than pretending kind defaults are minima.
- **[Global map growth adds work to each reconciliation]** → Keep expression compilation/resolution in the Config package and benchmark table tests; cache an immutable compiled snapshot only if measurements show a need.
- **[Config update storms reconcile all Nodes]** → Filter to the singleton Config and rely on rendered-object no-op conformance; debounce only if observed scale requires it.
- **[Component-heavy kinds expose ambiguous scope]** → Add plan/render tests for SR-SIM and retain imported requirements for non-primary containers.

## Migration Plan

1. Add the additive Config fields and bootstrap parsing first, with nil maps producing legacy behavior.
2. Add resolver/render behavior and Config-triggered reconciliation; existing explicit profiles retain their current effective values.
3. Ship defaults only into fresh Config creation and into maps administrators have already enabled.
4. Publish the per-kind provenance table and an upgrade example for opting into the maps after capacity review.
5. Regenerate CRDs/clients and verify fresh-install, merge, and overwrite fixtures.

Rollback is configuration-first: back up the Config before opting in. Removing or emptying the new maps restores generic-default selection after reconciliation. A controller/CRD downgrade also requires restoring the backed-up Config schema; older controllers ignore the new behavior and must not be used as an implicit resource-value migration mechanism.

## Open Questions

Exact request quantities that are not stated by the pinned containerlab release will be settled during the supported-kind data audit and recorded with provenance. This does not change the API, selection, or migration design.
