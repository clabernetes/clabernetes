## Why

c9s installation is split across `try-c9s`, e2e, DevSpace, and manual Helm flows, with no `make install` contract for an existing cluster and no reliable way to select or verify the installed c9s version. The repository already publishes mutable main artifacts and can manually publish commit-scoped artifacts, but neither development channel is exposed or verified as an installation option. The current published quickstart is also incorrect on `main`: the latest `0.6.0` chart serves the legacy API group while the checkout demo uses `c9s.run`.

## What Changes

- Introduce one shared installation workflow used by `make try-c9s` and a new `make install`, while keeping cluster creation, MetalLB, and demo resources exclusive to `try-c9s`.
- Support stable, development, and local selections through a common version contract: `VERSION` for `make install` and `C9S_VERSION` for `make try-c9s`, covering latest stable release, exact published release, mutable main, exact unpublished commit build, local checkout, and interactive selection.
- Add a repository-local UV/PEP 723 CLI using Rich and Typer that invokes a pinned repository-local GitHub CLI for authenticated/paginated JSON, lists releases and development builds with accurate timestamps, and returns machine-readable selections to Make.
- Keep `cicd` as the pull-request and main-merge pipeline with no publication toggle, always publishing main artifacts after a successful merge. Add a separate `Create dev release` manual workflow that selects a branch or tag, always runs lint and unit tests, optionally runs e2e, and publishes multi-architecture images and a chart as `0.0.0-<short-sha>` without creating a GitHub Release.
- Treat the existing `0.0.0` chart as an explicit mutable `main` channel rather than as “latest”; publish it with full source revision metadata and manager/launcher values pinned to the same main commit instead of relying on floating `dev-latest` image resolution.
- Validate the selected Kubernetes context and exact OCI artifact before mutating a cluster, then verify the Helm chart, manager image, launcher image, rollout, and embedded version after installation.
- Reuse the e2e local image build/load path for KinD. Require an explicit pullable registry for local-checkout installation into non-KinD clusters, while leaving the development in-cluster registry as an opt-in transport rather than an installation default.
- Make published `try-c9s` installs use a demo compatible with the selected release, make local installs use the checkout demo, and fail the command when the demo does not become ready.
- Detect the legacy-to-`c9s.run` CRD boundary before installation and direct users to the documented destructive uninstall/reinstall procedure instead of attempting a partial Helm upgrade.
- Consolidate installation tool versions so local and CI paths use the same pinned binaries, and add installation-focused automated acceptance coverage and documentation.

## Capabilities

### New Capabilities

- `installation-workflows`: Defines the `try-c9s` and existing-cluster installation contracts, source modes, image transport, preflight checks, deployment, verification, and cleanup behavior.
- `release-selection`: Defines stable and development artifact discovery, timestamp and channel presentation, interactive and non-interactive selection, normalization, pagination, and artifact validation.
- `development-builds`: Defines manual unpublished commit builds, mutable main artifacts, source metadata, test and publication gates, installability checks, and handoff commands.

### Modified Capabilities

- `crd-api-group`: Requires installation preflight to detect an incompatible legacy API-group installation and refuse an in-place cutover.
- `documentation-site`: Requires navigable documentation for disposable quickstart and existing-cluster installation, including version selection, local images, compatibility, verification, and teardown.

## Impact

- Make targets and shared helpers in `Makefile` and `.mk/`, especially `try-c9s`, e2e image loading, tool downloads, and uninstall behavior.
- A new UV-managed Python release selector with pinned inline dependencies.
- Local manager and launcher image tagging/version embedding, KinD loading, and registry push paths.
- Helm invocation and Config launcher-image reconciliation without overwriting unrelated user configuration.
- GitHub Actions for install acceptance and exact-release smoke testing.
- Main `cicd` publication, the `Create dev release` manual workflow, and the GitHub-release publication workflow.
- Root README, Make help, chart README, quickstart, installation, upgrading, and troubleshooting documentation.
- External systems: GitHub Releases API, GHCR OCI charts/images, Docker/BuildKit, KinD, Helm, and Kubernetes API/RBAC.
