# Repository instructions for coding agents

## Git workflow

- Never force-push. Commits always go on top of the remote branch history with `git push`.
- Amending, rebasing, or rewriting already-pushed commits is a force push and requires the user's explicit request. Without it, add a follow-up commit instead.
- `--force-with-lease` (and any other flag that rewrites remote history) counts as a force push and is subject to the same rule.

This rule applies to every agent working in this repository, in every session, unless the user explicitly asks for a force push for a specific push.

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
- Use the matching router and switch icons from the [clab design catalog](https://github.com/clab-labs/branding/tree/main/icons) in network topology diagrams. Use its container or VM icon for endpoints, matching the actual workload.
- Give diagram connector labels and overlay text enough breathing room: reserve visible clearance from cards, other labels, arrowheads, and unrelated connector lines at the intended viewing size. Use the fewest bends needed for a clear connector route; avoid tiny zigzags, redundant corners, and unnecessary detours. Adjust row spacing and connector routing together; increasing spacing alone may leave labels on crossing lines. Visually verify the rendered diagram in its supported inline and fullscreen views before finishing, and fix cramped or overlapping labels.
- Use headless Playwright for browser interaction and verification by default. Use a visible browser only when the required behavior cannot be checked headlessly.

## Generated files

Do not hand-edit generated artifacts, including:

- `apis/**/zz_generated.deepcopy.go`
- `charts/clabernetes/crds/`
- `assets/crd/`
- `generated/`

Update the source API or generator and run `make verify-generated`. Inspect all regenerated changes before keeping them.

## Validation

Before finishing any task that changes repository files, run `make lint` locally from the repository root. Install missing lint tools with `make install-lint-tools`, fix any findings, and inspect formatter changes before finishing. If the local environment prevents a complete run, report the blocker and do not claim lint passed.

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
| Cluster-level behavior | `make test-e2e CLUSTER=existing` for the selected Kubernetes context, or `make test-e2e-local` for a disposable KinD cluster; follow the e2e contract below |

`make lint` runs formatters and may modify files; inspect the diff afterward. Do not run expensive e2e or cluster-mutating targets unless the affected behavior requires them. In the final report, state which checks ran and which relevant checks were skipped.

### E2E execution contract

- Use the root Makefile targets, implemented in `.mk/e2e.mk`, for e2e validation. Do not replace this workflow with direct `go test`/`gotestsum` invocations, manual Helm upgrades, or custom image-loading scripts. Resolve missing prerequisites through the supported workflow and report any remaining blocker.
- For an existing cluster, run `make test-e2e CLUSTER=existing`. It defaults to the current kube context; `C9S_CONTEXT` selects an explicit context, which must also be current when tests run. This target installs the current checkout through `make install VERSION=local` before testing. Inspect the existing c9s release and set `E2E_INSTALL_NAMESPACE` and `E2E_INSTALL_RELEASE` accordingly when updating it; avoid starting competing cluster-wide controllers. The target defaults to namespace/release `c9s-e2e`.
- `make test-e2e-local` forces KinD. `make e2e-test` is also KinD-specific and exports its kubeconfig; do not use either to test an existing remote context.
- When the current checkout is already installed and only tests changed, prepare tools with `make e2e-tools install-test-tools CLUSTER=existing`, then run `PATH="$PWD/build/e2e/bin:$PATH" make e2e-run` against the selected current context. Set `E2E_TEST_PACKAGES` to run one package without building the whole e2e tree. `e2e-run` is the shared test recipe; it preserves `GOWORK=off`, the race detector, and coverage collection. Ensure CGO and a working C compiler are available instead of silently dropping `-race`.
- For local e2e runs against an existing cluster, the user must provide a readable, nonempty license file at `/opt/nokia/sros/license.txt` on the machine running Make. Before invoking an e2e target for that cluster, export the file contents in the shell with `export SRSIM_LICENSE="$(cat /opt/nokia/sros/license.txt)"`. If the file is missing, unreadable, or empty, report the missing prerequisite and resolve it before running the suite. Do not print or commit the license.
- Match the suites enabled in `.github/workflows/e2e.yaml` when investigating CI failures. The Make targets inherit `SRSIM_LICENSE`; they do **not** read the local license file automatically. CI injects the license contents from its `SRSIM_LICENSE` secret. The SR-SIM test mounts those contents at `/opt/nokia/sros/license.txt` inside the workload as well.
- For SR-SIM, also set `SRSIM_IMAGE` to the image used by the relevant CI run and prepare GHCR Docker authentication. The test reads the `ghcr.io` entry from `${DOCKER_CONFIG}/config.json`, or `~/.docker/config.json` when `DOCKER_CONFIG` is unset, to create its test namespace's image-pull Secret. CI handles image pulling/loading separately from the Make test target; verify image availability for the selected cluster.
- A missing license skips SR-SIM, even if Make exits successfully. If CI enables that suite, a local run without it is incomplete validation. Report the exact command, context, tested revision, passed/failed/skipped counts, skipped suites and reasons, and any deviations from the standard recipe. Distinguish local results from GitHub Actions status; do not call CI resolved based only on a local exit code or a rerun that has not completed.

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
