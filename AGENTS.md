# Repository instructions for coding agents

This is the canonical, vendor-neutral guidance for automated coding agents working in this repository. It applies to the whole repository unless a more specific `AGENTS.md` exists below the directory being changed.

## Instruction contract

- Follow system and user instructions before this file. For repository guidance, the nearest applicable `AGENTS.md` takes precedence.
- Treat examples such as `make test` as shell commands run from the repository root, not as agent slash commands.
- Do not assume a particular agent, model, IDE, or tool is available. Use the equivalent supported capability when a named capability is unavailable.
- Resolve uncertainty from the repository first. State assumptions that materially affect behavior or scope; ask only when a safe answer cannot be established from the code, tests, or documentation.
- Preserve unrelated user changes. Do not revert, overwrite, or reformat work outside the requested scope.

## Repository map

Clabernetes (c9s) is a Go Kubernetes controller that runs containerlab topologies in Kubernetes.

- `apis/`: Kubernetes API types. Generated deepcopy files live beside their source types.
- `controllers/`, `manager/`, `internal/`: reconciliation and runtime behavior.
- `charts/`: Helm charts and generated CRDs.
- `generated/` and `assets/crd/`: generated clients, OpenAPI output, and CRD copies.
- `docs/`: repository-owned documentation content.
- `docs-site/`: React Router/Fumadocs static site that renders `docs/`.
- `e2e/`: Kubernetes end-to-end tests.
- `openspec/`: OpenSpec proposals, designs, tasks, and specifications.

## Working agreement

- Read the task, relevant implementation, tests, and callers before editing.
- Prefer an existing project pattern, then the standard library or platform, then an installed dependency. Add a dependency only when those options do not meet the requirement.
- Fix the root cause at the shared boundary when practical. Check sibling callers so a narrow fix does not leave the same defect elsewhere.
- Preserve validation, security, accessibility, API compatibility, and data-loss protections. Document any deliberate limitation with its ceiling and likely upgrade path.
- Add or update the smallest test that would fail without a non-trivial behavior change.

## Generated files

Do not hand-edit generated artifacts, including:

- `apis/**/zz_generated.deepcopy.go`
- `charts/clabernetes/crds/`
- `assets/crd/`
- `generated/`

Update the source API or generator and run `make verify-generated`. Inspect all regenerated changes before keeping them.

## Validation

Start with the narrowest relevant check, then expand in proportion to the change:

| Change | Expected validation |
| --- | --- |
| One Go package | `go test ./path/to/package/...` |
| General Go behavior | `make test` |
| Concurrency-sensitive Go behavior | `make test-race` |
| Go formatting/lint or Helm charts | `make lint` |
| Documentation content or site code | `make check-docs` |
| Static-site build or production routing | `make build-docs`; use the `test-fumadocs-wrangler` skill when Cloudflare routing, redirects, direct loads, or refreshes matter |
| API types, CRDs, or generated clients | `make verify-generated` |
| Cluster-level behavior | `make test-e2e-local` when a Docker/Kubernetes environment is available |

`make lint` runs formatters and may modify files; inspect the diff afterward. Do not run expensive e2e or cluster-mutating targets unless the affected behavior requires them. In the final report, state which checks ran and which relevant checks were skipped.

## OpenSpec workflows

Keep planning artifacts under `openspec/`. For OpenSpec work, read the matching repository skill; invoke it through the agent's native skill mechanism when supported, or follow its `SKILL.md` directly:

- `openspec-propose`
- `openspec-apply-change`
- `openspec-sync-specs`
- `openspec-archive-change`
- `openspec-explore`

Read the selected skill before following its workflow. Do not duplicate these workflows as vendor-specific command files.

## Agent configuration maintenance

- `AGENTS.md` is the single source of truth for shared repository instructions.
- `.agents/skills/` is the single source of truth for portable repository skills.
- `.claude/CLAUDE.md`, `.claude/skills/`, and `.cline/skills/` are discovery symlinks only; they contain no independent guidance and do not make this repository vendor-specific.
- Edit canonical files, not compatibility paths. Do not create copied rules, commands, or skills for individual agents unless a required capability cannot be represented by `AGENTS.md` or `.agents/skills/`.
