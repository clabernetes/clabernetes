# Proposal

## Why

Issue #358 rejects a valid containerlab topology whenever a Node declares `config.vars`, even though the compiler preserves those variables and the direct planner already supplies them to containerlab's startup-config templating lifecycle. This prevents affected Nodes from being planned or booted and contradicts the supported startup-configuration workflow.

## What Changes

- Accept `config.vars` as supported Node input for containerlab startup-config Go templates instead of rejecting every non-nil `config` block during direct-Pod plan mapping.
- Render variables through the existing imported containerlab lifecycle and carry the rendered configuration through the existing plan-artifact and post-deploy boundaries; do not introduce a separate configuration engine.
- Preserve arbitrary `config.vars` values when rewriting actual payload path fields, including values that happen to equal a declared payload destination.
- Add planner regression coverage for nested variables and a direct-runtime boot test that proves the running Node's effective configuration contains values rendered from the declared variables, rather than merely proving that planning or Pod readiness succeeds.
- Update the compatibility documentation to describe startup-config templating support and its scope. A `config.vars` block without a startup-config template remains accepted but does not independently configure a Node.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `node-lifecycle`: `config.vars` becomes realized Node intent whose arbitrary values survive planning and whose referenced values appear in the Node's effective startup configuration.
- `device-planning`: planning accepts configuration variables, renders them through imported startup-config behavior, and does not rewrite arbitrary variable data as payload paths.
- `direct-runtime-conformance`: startup-configuration equivalence includes boot-level evidence that declared variables affect the running device's configuration.

## Impact

- `internal/deviceplan`: remove the incorrect unsupported-field rejection and constrain generic payload path rewriting around arbitrary variable data.
- `e2e/topology` and planner tests: add artifact-level and running-device regression coverage, using the existing SR Linux direct-runtime coverage for the boot assertion.
- `docs/concepts`: replace the current `config` rejection guidance with the supported templating contract and its limits.
- The Node API and CRD schema already carry arbitrary `config.vars` values and require no shape change. The containerlab dependency, plan schema, payload model, and persistence semantics remain unchanged.
