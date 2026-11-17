# Design

## Context

See `proposal.md` for motivation and the capability deltas for the required behavior.

The Topology compiler already resolves Containerlab configuration-variable inheritance and stores nested values as arbitrary JSON in the flattened Node definition. Device planning decodes that definition and assigns `definition.Config` to Containerlab's `NodeConfig`. Imported preparation therefore receives `.Config.Vars` before `validateMappedDefinition` currently rejects the field.

Generated startup files already use the plan-artifact boundary. For CLI-format startup configuration, the in-Pod lifecycle boundary reconstructs imported Node state from the normalized definition before invoking the package post-deploy hook. Both planning and reconstruction rewrite declared payload paths with the same structural string walker; that walker currently also visits arbitrary variable strings.

The implementation must preserve the unmodified Containerlab dependency, avoid kind-specific c9s branches, and leave preparation free of imported code evaluation.

## Goals / Non-Goals

**Goals:**

- Realize `config.vars` through the same imported startup-config behavior used by standalone Containerlab.
- Preserve the exact variable subtree through planning and lifecycle reconstruction while continuing to resolve genuine file-reference fields to verified payloads.
- Prove both planner output and the effective configuration of a booted device.

**Non-Goals:**

- Implement a standalone or post-start configuration engine beyond the startup behavior Containerlab itself invokes.
- Add a runtime plan field for raw variables, pre-render templates in the Topology compiler, or change the Node/plan schemas.
- Enable other currently rejected fields such as `env-files`, `runtime`, `stages`, or `credentials`.
- Change persistence, enforcement, or device-saved configuration semantics.

## Decisions

### 1. Remove the mapper-only rejection and retain imported rendering

Delete `definition.config` from `validateMappedDefinition`'s unsupported-field list. Do not add a replacement mapper or renderer: `nodeConfigFromDefinition` already supplies the variables, imported kinds already consume them, and generated full-file configuration already has a direct-Pod representation.

Compiler-side pre-rendering was rejected because it would duplicate Containerlab template semantics, bypass kind-owned startup handling, and create a second rendering path for CLI and full-file formats. Silently dropping `Config` would deploy incorrect or incomplete device configuration.

An unused `config.vars` block is accepted without an additional action, matching the pinned Containerlab lifecycle rather than adding c9s-only validation.

### 2. Treat the exact `config.vars` subtree as opaque during path rewriting

Extend the structural definition traversal with enough field-path context to skip only `NodeDefinition.Config.Vars`. Continue visiting all other fields, including `StartupConfig`, `License`, and `Binds`. Both explicit planning-payload rewriting and staged-payload reconstruction must use this same behavior so they cannot diverge.

The exclusion must identify the complete field path rather than every field named `vars`; arbitrary data elsewhere and future typed path fields retain the existing structural behavior. This preserves string values even when they normalize to the same path as a declared payload destination.

Rewriting everything and restoring `Config` afterward was considered, but it requires an independent deep-copy/restoration path and makes aliasing mistakes easier. A path-aware opaque-data exclusion states the semantic boundary directly.

### 3. Reuse existing artifact and lifecycle mechanisms

No raw variable values are added to the runtime-neutral plan. For generated files, the accepted plan continues to carry rendered artifact bytes. Preparation stages those bytes without evaluating imported code. For CLI-format startup configuration, lifecycle reconstruction receives the unchanged normalized variables and rebuilds the package-owned state in scratch before the accepted post-deploy hook runs.

Adding a new runtime action was rejected because it would bypass the existing plan completeness, persistence, and reconstruction contracts and would not correspond to a separate current Containerlab operation.

### 4. Test rendered content at two levels

Planner tests will use the existing synthetic imported-kind boundary to invoke Containerlab's actual template generation, with nested list/mapping/scalar values and an inline startup-config template. Assertions will inspect planned artifact bytes, proving the previous mapper rejection is gone and rendering consumed the supplied values. A separate collision test will prove that a startup-config payload path is rewritten while an identical `config.vars` string remains literal. Existing compiler assertions already cover inherited variable flattening.

The existing direct SR Linux test will add nested variables to `srl1` and reference them from its CLI-format startup-config template. After Node readiness, the test will poll `sr_cli info from running` and require a unique variable-derived value at the templated configuration path. This covers post-deploy reconstruction and verifies device state, not merely rendered files or Pod readiness. The lookup must poll because readiness alone does not establish that the configuration observation is complete.

### 5. Document the supported contract narrowly

Update the Node compatibility and Containerlab-differences documentation to move `config` out of the rejected list and describe `config.vars` as startup-template input. Explicitly state that variables do not independently configure a Node without a startup-config consumer.

## Risks / Trade-offs

- **Payload path handling could accidentally exempt real path fields** → Exclude only the full `Config.Vars` path and test that `StartupConfig` still resolves while the colliding variable remains unchanged.
- **Planner success could hide a post-deploy regression for CLI-format configuration** → Keep the real SR Linux running-configuration assertion in addition to artifact tests.
- **Users could interpret this as a general configuration engine** → Document the startup-template-only boundary and the no-consumer behavior explicitly.
- **Readiness could race the device observation** → Use the existing polled device-command helper rather than a single immediate query.

## Migration Plan

No object, schema, or stored-plan migration is required. Existing Nodes containing `config.vars` become plannable after upgrade without being rewritten.

Rollback only requires reverting the code change. An older release will again reject these Nodes during new planning attempts; rollback procedures must not delete otherwise healthy existing workloads as a way to force reconciliation.
