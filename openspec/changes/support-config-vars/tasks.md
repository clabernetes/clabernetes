# Tasks

## 1. Accept variables in device planning

- [x] 1.1 Add planner regression coverage through the synthetic imported-kind boundary for nested lists, mappings, and scalars consumed by an inline startup-config template, plus a `config.vars` block with no startup-config consumer; verify the new tests fail at the current `definition.config` rejection.
- [x] 1.2 Remove only the `definition.config` unsupported-field entry while preserving the other rejected fields, reuse imported Containerlab rendering without adding a plan field or second renderer, and run `go test ./internal/deviceplan/...` to verify the regression tests and existing planner suite pass.

## 2. Preserve arbitrary variable data

- [x] 2.1 Add regression coverage in which a startup-config payload destination and a `config.vars` string are identical, exercising both explicit planning-payload and staged lifecycle-payload rewriting; verify the current implementation rewrites the variable incorrectly while the test expects the file reference to resolve and the variable to remain literal.
- [x] 2.2 Make the shared structural traversal skip only the complete `NodeDefinition.Config.Vars` subtree, continue traversing every actual file-reference field, and run `go test ./internal/deviceplan/...` to verify both rewriting boundaries and the rendered artifact behavior pass.

## 3. Prove running-device configuration

- [x] 3.1 Extend the direct SR Linux fixture with nested `config.vars` referenced by its CLI-format startup-config template and extend `TestNodeLinkDirect` to poll `sr_cli info from running` for a unique rendered value; run the test through the root Make e2e workflow against the selected cluster with `E2E_TEST_PACKAGES=./e2e/topology/direct` and `E2E_TEST_ARGS='-run=^TestNodeLinkDirect$'`, recording the exact command/context or any environment blocker, and verify the running device shows the supplied value after boot.

## 4. Document startup-template support

- [x] 4.1 Update `docs/concepts/nodes-and-links.md` and `docs/concepts/containerlab-differences.md` to remove `config` from the rejected fields, explain `config.vars` startup-config templating and the no-consumer behavior without implying a standalone config engine, and run `make check-docs` to verify the documentation.

## 5. Repository validation

- [x] 5.1 Run `make test` and `make lint` from the repository root, inspect any formatter changes and the final diff, and record results plus the focused e2e result or skipped-check blocker without claiming unexecuted validation passed.
