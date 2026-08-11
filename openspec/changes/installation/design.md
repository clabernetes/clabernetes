## Context

The repository currently has three materially different deployment paths:

- `.mk/try-c9s.mk` creates KinD and installs an unversioned published OCI chart before applying the checkout's demo.
- `.mk/e2e.mk` builds checkout images, loads them into KinD, and installs the checkout chart.
- `make dev` delegates image transport and Helm deployment to DevSpace for source synchronization.

There is no c9s `make install` target for an existing cluster. The paths also disagree on tool versions and installation identity. The current live `c9s-e2e` cluster illustrates the observability problem: Helm reports chart `0.0.0`, the images are mutable `dev-latest` tags, and the old binary does not expose a usable version flag.

The `cicd` workflow handles pull requests and main pushes. A main push overwrites OCI chart `0.0.0`; because its image values were empty, chart templates could resolve manager and launcher to mutable `dev-latest`. A separate `Create dev release` manual workflow builds a selected branch or tag as multi-architecture images and a chart at `0.0.0-<short-sha>`, with chart values pinned to matching image tags and no GitHub Release object. Both paths retain exact artifact probing and install smoke because workflow success alone is not an installability guarantee.

Published and checkout resources are not necessarily compatible. At proposal time, GitHub's latest release is `v0.6.0`; its chart installs `clabernetes.containerlab.dev` CRDs, while the checkout demo and chart use `c9s.run`. Helm does not perform an in-place migration between those groups, and the existing `crd-api-group` specification requires uninstall/reinstall.

The installation interface must remain automation-friendly while also offering a discoverable interactive selector. It must use repository-local pinned tools, avoid routing static installation through DevSpace, and reuse the proven e2e image loading and development registry helpers where their behavior fits.

## Goals / Non-Goals

**Goals:**

- Give `make try-c9s` and `make install` one shared installation implementation.
- Make latest, exact published, interactive, and local-checkout selections explicit and verifiable.
- Make mutable main and exact unpublished commit builds explicit development channels, separate from latest stable.
- Let a developer publish tested, source-identifiable artifacts for a feature ref and hand other users exact install and try commands.
- Keep `make try-c9s` a non-interactive one-command latest-release experience by default.
- Use only repository-local pinned Helm, kubectl, KinD, yq, and UV binaries during installation.
- Fail before cluster mutation when the context, permissions, selected release, OCI chart, local transport, or API-group transition is invalid.
- Keep manager and launcher image versions coherent without overwriting unrelated Config CR fields.
- Make local builds uniquely identifiable and suitable for the selected cluster platform.
- Add installation acceptance coverage that exercises the public targets rather than a test-only reimplementation.

**Non-Goals:**

- Replacing Helm, Make, or DevSpace.
- Providing a generic Kubernetes image-upload mechanism where the cluster has no pullable registry.
- Automatically migrating or preserving resources across the legacy-to-`c9s.run` CRD cutover.
- Guaranteeing a runnable demo for every historical release before `v0.6.0`.
- Reworking the complete GitHub release publication lifecycle in this change; exact artifact probing protects installers from the current publication race.
- Creating GitHub Release objects for development builds.
- Publishing arbitrary commits from forks; an unpublished build source must resolve to a commit in this repository through an authorized workflow dispatch ref.
- Supporting heterogeneous node platforms with one local image build.

## Decisions

### 1. Use two public entrypoints over one shared install core

`make try-c9s` owns only disposable-environment concerns:

1. Ensure the pinned tools.
2. Create or reuse its named KinD cluster.
3. Materialize a dedicated kubeconfig in the try state directory.
4. Install and configure MetalLB.
5. Call the shared c9s installer.
6. Apply the source-compatible demo, wait for readiness, and print access details.

`make install` owns only existing-cluster concerns:

1. Ensure the pinned tools required by the selected source.
2. Capture the selected context once.
3. Call the same shared installer.

The shared installer resolves source, performs preflight and compatibility checks, transports local images when required, invokes Helm, reconciles the launcher image, and verifies the result.

This keeps the externally useful `make install` path independent of KinD and MetalLB while preventing the Helm behavior in `try-c9s`, e2e, and existing-cluster installs from drifting.

Alternative considered: make the Python CLI perform installation. Rejected because it would duplicate mature Make, Helm, e2e, and shell helpers and turn a small selector into a second orchestration system.

### 2. Define one version selector contract

The public variable is:

```text
VERSION=latest         # make install default
VERSION=main           # mutable chart 0.0.0 from the latest successful main publication
VERSION=vX.Y.Z         # exact published release; X.Y.Z is also accepted
VERSION=0.0.0-<sha>    # exact unpublished commit build
VERSION=local          # checkout chart and images
VERSION=select         # interactive stable/development picker

C9S_VERSION=...        # equivalent selector for make try-c9s
```

Existing chart/namespace/context override variables remain available where practical, but all source modes normalize into an internal structure containing:

- source kind (`release`, `main`, `unpublished`, or `local`);
- GitHub tag when published;
- normalized unprefixed OCI chart version;
- full source revision when supplied by development chart metadata;
- chart reference;
- expected CRD API group;
- desired manager and launcher image references;
- demo reference when `try-c9s` is used.

`latest` means GitHub's latest stable published release, not Helm's unversioned OCI resolution, main chart `0.0.0`, or a mutable image tag. `main` explicitly selects chart `0.0.0`. Exact unpublished builds use the valid SemVer prerelease form `0.0.0-<short-sha>`. Every remote mode invokes Helm with an exact `--version`.

Alternative considered: separate source and version variables. Rejected because one version selector per Make entrypoint is smaller for users and still normalizes unambiguously.

### 3. Keep release discovery in one UV script

Add one PEP 723 script under `hack/` with exact inline dependencies on Rich and Typer. The pinned repository-local UV binary runs it and installs dependencies on demand. A pinned repository-local GitHub CLI supplies GitHub API JSON and owns authentication.

The script provides commands for listing, selecting, and resolving releases. It:

- receives the absolute repository-local `gh` path from Make and rejects an unresolved host executable;
- invokes `gh api --paginate` with JSON output for GitHub Releases and Actions endpoints;
- delegates keychain, `GH_TOKEN`, and `GITHUB_TOKEN` handling entirely to GitHub CLI and never reads or stores credentials itself;
- filters drafts;
- marks prereleases rather than silently presenting them as stable;
- preserves historical tags with or without a leading `v`;
- displays `published_at` as **Published (UTC)**;
- sorts published releases by `published_at` from newest to oldest regardless of API page order;
- writes Rich UI to stderr and only the selected normalized value to stdout when called by Make;
- reports API rate-limit and network failures without a traceback.

The selector also exposes a distinct development view. It presents `main` as a moving channel and obtains recent manually dispatched build candidates from successful `Create dev release` workflow runs through `gh api` against the GitHub Actions API. Action completion is labeled as workflow completion, not release publication or package push time. A development candidate is still installable only after the exact OCI chart probe succeeds.

The public `make ls-releases` target produces a Rich table of all installable c9s artifacts: GitHub
Releases, the mutable `main` chart at `0.0.0`, and successful manual development builds at
`0.0.0-<short-sha>`. The script receives the absolute repository-local Helm path, probes candidates
concurrently, and sorts them by publication or workflow-availability time descending. By default it
stops after the newest 10 installable artifacts; `make ls-releases ALL=1` probes and displays the
complete catalog. It omits candidates whose exact chart is unavailable and reports the omitted count
for candidates it checked. The table includes one normalized **Version** column—the value users can
pass as `VERSION` to `make install`—along with channel, source URL, and **Published/available
(UTC)**. The `main` row displays Version `0.0.0`; `VERSION=0.0.0` and `VERSION=main` select that
channel.
This target performs no cluster access or mutation.

The script does not claim that a release is installable. After resolution, the shared installer runs `helm show chart` for the exact OCI version. This probe is authoritative because the public GitHub Packages API requires `read:packages`, and a GitHub release can become visible before the release workflow pushes its chart.

Explicit exact versions can proceed to the OCI probe when release listing is unavailable. This preserves deterministic automation during a GitHub API listing failure.

The current repository does not download GitHub CLI for local workflows; only GitHub-hosted release jobs assume runner-provided `gh`. This change therefore adds GitHub CLI to the pinned local tool set rather than trusting host `PATH`.

Alternative considered: direct Python HTTP calls. Rejected because they would duplicate GitHub CLI authentication, pagination, host selection, and error behavior.

Alternative considered: use shell and `gh api` without Python. Rejected because the Rich/Typer interactive selector and normalized machine-output contract still require structured application logic.

### 4. Model unpublished commit and main artifacts as development channels

Keep `cicd` focused on pull-request validation and main-merge publication, and use a separate `Create dev release` entrypoint for ad-hoc publication. Both call one reusable development publisher rather than duplicating the image, chart, metadata, verification, and smoke implementation.

For manual unpublished builds:

1. An authorized developer dispatches `Create dev release` on the feature branch or tag that points at the desired repository commit and chooses whether to run e2e.
2. Lint and unit always validate that exact workflow ref; e2e runs when selected. Publication does not proceed when any required check fails.
3. Manager, launcher, and clabverter multi-architecture images are published as `0.0.0-<short-sha>`.
4. Clicker and c9s charts are published at the same version. The c9s values pin manager and launcher to those exact tags.
5. Chart `appVersion` or an equivalent chart annotation records the full source SHA.
6. The workflow probes both image platforms and the exact chart, performs an exact-version install smoke, and writes `make install` and `make try-c9s` handoff commands to the job summary.

No GitHub Release is created, and the build is not eligible for `latest`. Users install it with
`VERSION=0.0.0-<short-sha>`; `try-c9s` uses the equivalent `C9S_VERSION` selector. The try path
obtains the compatible demo from the full source revision recorded in the chart.

GitHub workflow dispatch selects a branch or tag ref; it does not directly select an arbitrary detached SHA. For feature development, the desired commit must be the head of the selected branch/tag. The workflow summary records the resolved full SHA so the artifact remains auditable after the branch moves.

For main builds, preserve chart version `0.0.0` as a deliberately mutable edge alias, but package it with:

- full source SHA as chart application/source metadata;
- manager and launcher values pinned to immutable `0.0.0-<short-sha>` image tags.

The image workflow may continue publishing `dev-latest` for development tooling, but installing `main` does not depend on it. This closes the race where chart `0.0.0` and `dev-latest` could advance independently and makes manager/launcher identity observable.

Alternative considered: treat `0.0.0` as latest. Rejected because it is rebuilt on every main merge and is intentionally less stable than a GitHub Release.

Alternative considered: create prerelease GitHub Releases for commit builds. Rejected because the requested artifacts are explicitly unpublished and the existing OCI workflow already supplies the necessary distribution mechanism.

### 5. Use one pinned installation toolchain

Move installation-related version pins to one source consumed by local and CI recipes. Download versioned binaries, including GitHub CLI, into a shared repository-local directory and invoke their absolute paths. Source-specific targets request only what they use:

- remote existing-cluster install and interactive/catalog selection: kubectl, Helm, UV, GitHub CLI;
- local existing-cluster install: kubectl, Helm, UV, Docker/BuildKit, plus registry helpers;
- `try-c9s`: the above plus KinD and yq;
- e2e: KinD, kubectl, Helm, and yq from the same pins.

The design removes the current local/CI Helm and UV version split. Host `gh`, `helm`, `kubectl`, `kind`, `yq`, or `uv` must not influence installation behavior.

Checksum verification remains desirable hardening, but it is not made a blocker unless every pinned upstream artifact exposes a stable checksum source for all supported OS/architecture pairs.

### 6. Capture and prove the target cluster before mutation

`make install` accepts optional `C9S_CONTEXT` and otherwise reads the current context once. Every subsequent Helm and kubectl call receives that exact context explicitly.

Preflight checks, with bounded timeouts, SHALL establish:

1. the context exists;
2. its API server is reachable and authenticated;
3. nodes can be listed and expose a single target OS/architecture for local builds;
4. the caller can create or update the required namespace and cluster-scoped Helm resources;
5. the selected chart can be fetched/rendered;
6. any existing c9s CRDs and Helm release are compatible with the selected chart's CRD API group;
7. the chosen local image transport is usable.

`try-c9s` does not depend on the user's current context. It writes/refreshes a kubeconfig from the named KinD cluster under `build/try-c9s/<cluster>/` and passes that kubeconfig explicitly. Existing KinD presence is not considered success until the API probe passes.

Alternative considered: rely on `helm upgrade --install` errors. Rejected because it mutates too early and produces poor diagnostics for wrong contexts, stale KinD kubeconfigs, missing artifacts, and incompatible CRDs.

### 7. Derive compatibility from chart CRDs, not a version table

Before installation, render or inspect the selected chart CRDs and derive the target API group. Inspect the cluster for existing c9s CRDs in both `clabernetes.containerlab.dev` and `c9s.run`.

If an existing installation's group differs from the selected chart's group, fail without invoking Helm and direct the user to `make uninstall-c9s`, explicitly warning that CRD deletion removes all custom resources. This handles local builds and future releases without hardcoding the release where the group changed.

Same-group reinstall and version changes are permitted, subject to the exact artifact and post-install checks.

### 8. Match the try demo to the selected source

For local source, apply the checkout's `examples/basic/srl-multitool.yaml`.

For supported published source, retrieve the demo from the immutable selected Git tag rather than from the checkout. The guaranteed published-demo support floor is `v0.6.0`, where the stable path exists. The release selector may list older releases, and `make install` may install an older exact chart after a successful OCI probe, but `make try-c9s` rejects releases below the demo support floor with an actionable message.

For `main` and exact unpublished commit builds, the try workflow retrieves the demo from the full
source revision embedded in the chart metadata. It fails before applying resources if the chart
lacks source metadata or that revision's demo is unavailable. The existing-cluster install does not
perform this metadata check.

The demo manifest is stored in the try state directory before application. Readiness timeout dumps topology state, pods, events, and manager/launcher logs, then returns failure. The current soft-success behavior is removed.

Alternative considered: maintain duplicate legacy demos on `main`. Rejected because a release-tagged demo is already version-coupled to its controller and avoids an expanding compatibility map.

### 9. Give local builds immutable identities

Local manager and launcher builds share one generated identity:

- clean checkout: `local-<short-commit>`;
- dirty checkout with image-input changes: `local-<short-commit>-dirty-<worktree-hash>`.

The same identity is:

- used as the image tag;
- passed as the Docker `VERSION` build argument;
- supplied explicitly to Helm for manager and launcher;
- printed in the post-install summary.

The installer detects the cluster's single node platform and passes it to BuildKit. Mixed-platform clusters fail unless future work adds a multi-platform push implementation.

The worktree hash follows the image Docker context, so documentation and other excluded files do not
invalidate the image identity. Unique identities for changed image inputs prevent `IfNotPresent` and
an unchanged Pod template from reusing stale content.

### 10. Select image transport by cluster type and explicit user choice

For `try-c9s`, the KinD cluster name is known and local images are loaded with `kind load docker-image`.

For `make install` local source:

- verified KinD target: load both images directly into every node;
- non-KinD target: require `C9S_REGISTRY` and push immutable tags that the cluster can pull;
- optional `C9S_IMAGE_TRANSPORT=in-cluster`: reuse the focused DevSpace registry deployment, port-forward, BuildKit, and image-reference helpers without invoking DevSpace itself.

The in-cluster registry is not the default because it is an additional mutable service and remains a runtime dependency whenever new launcher pods need the local image. Documentation must describe its lifecycle and teardown.

The existing platform detection and external-registry authentication helpers are reused or factored into shared scripts. The DevSpace deployment and source-sync pipeline are not reused for static installation.

### 11. Reconcile manager and launcher versions surgically

Published chart defaults couple manager and launcher to the chart version, while local source passes both images explicitly. However, the chart's bootstrap `merge` mode preserves a non-empty launcher image in an existing Config CR.

After Helm succeeds, the installer waits for the chart's Config singleton and patches only:

- `spec.deployment.launcherImage`;
- `spec.deployment.launcherImagePullPolicy`.

It uses the API group derived from the selected chart. It does not set chart bootstrap mode to `overwrite`, because overwrite would replace unrelated user configuration.

The installer then verifies:

- Helm release, chart version, and selected channel;
- manager Deployment image;
- Config launcher image and pull policy;
- manager rollout;
- embedded manager version where the selected binary exposes it;
- local build identity for both local images.

Any mismatch fails with the expected and observed values. Successful output displays context,
namespace, Helm release, channel, chart, manager image, launcher image, and observed binary version.

### 12. Test the public contract in layers

Fast tests use fixture GitHub responses and command stubs to cover pagination, stable/prerelease filtering, old tag normalization, non-interactive output, rate limits, missing context, unreachable APIs, artifact probe failure, compatibility decisions, and command construction.

KinD acceptance uses the same public Make targets and covers:

- latest published install when a compatible release exists;
- exact supported published release;
- local build/load/install;
- `make install` against a separately created existing KinD cluster;
- idempotent rerun;
- version/source switch within one API group;
- manager/launcher Config coherence;
- hard demo readiness failure;
- cleanup and uninstall;
- poisoned host PATH to prove absolute pinned tools.

Workflow tests and smoke checks cover the `Create dev release` feature-ref dispatch, mandatory lint/unit and optional e2e publication gates, exact commit images/chart, generated handoff commands, mutable main chart source metadata, main chart image pinning, and demo retrieval by source revision.

CI provides linux/amd64 gating coverage and linux/arm64 smoke coverage without multiplying every selector across every platform. The release workflow adds an exact-version install smoke after images and charts are pushed; a failed smoke fails the release workflow even though the current GitHub release object may already be visible.

## Risks / Trade-offs

- **GitHub release visible before OCI artifacts** → Always probe the exact chart before mutation and clearly report “release published, artifact not ready”; consider reordering release publication separately.
- **GitHub unauthenticated rate limiting** → Honor optional tokens, expose reset information, and allow explicit versions to proceed directly to the OCI probe.
- **Historical chart or demo unavailable after repository moves** → Guarantee `try-c9s` only from `v0.6.0`, list older releases as historical, and validate the selected demo/artifact before cluster mutation.
- **Successful workflow run but development artifact was removed or never pushed** → Treat Actions results as candidates and retain the exact OCI probe as the installation gate.
- **Mutable main chart races with mutable images** → Pin each `0.0.0` chart publication to immutable `0.0.0-<short-sha>` manager/launcher tags and embed the full source SHA.
- **Feature branch moves after dispatch** → Build the resolved workflow SHA, embed it in chart metadata, and print it in the workflow summary and installer output.
- **External registry is authenticated for push but not pullable by nodes** → Require explicit registry configuration and fail on rollout with image-pull diagnostics; document imagePullSecret requirements.
- **In-cluster registry becomes a runtime dependency** → Keep it opt-in and include lifecycle/teardown documentation.
- **Dirty local tag uses a generated build identity** → Reproducibility is lower than a clean commit, but stale-image correctness is preserved and the identity is printed.
- **Config launcher patch overrides a deliberate custom launcher image** → Treat `C9S_VERSION` as ownership of manager/launcher compatibility and document that installation reconciles both images; preserve every unrelated Config field.
- **Helm/CRD operations are not fully transactional** → Perform all possible checks before Helm, block cross-group upgrades, and provide diagnostic cleanup instructions rather than claiming automatic rollback.
- **Tool downloads are version-pinned but not checksum-verified** → Keep downloads HTTPS-only and versioned; add checksums when upstream coverage can be maintained consistently.

## Migration Plan

1. Add stable/development selection and fixture tests without changing existing targets.
2. Separate routine CI from `Create dev release`, then harden manual and main development publication with source metadata, exact image pins, optional e2e gating, artifact probes, install smoke, and handoff output.
3. Consolidate tool pins and local binary paths; prove e2e and existing developer workflows still use the intended versions.
4. Introduce the shared install core and new `make install`.
5. Migrate `try-c9s` to the shared core, release/source-revision demo selection, idempotent KinD handling, and hard readiness failure.
6. Migrate e2e local image build/load helpers to the shared local build identity and transport primitives.
7. Add post-install manager/launcher reconciliation and verification.
8. Add KinD acceptance and stable/development artifact smoke jobs.
9. Update all installation and upgrade documentation, then enable the new paths as the documented defaults.

Rollback is a source revert for tooling and Make changes. Cluster rollback is not automatic: same-group release changes can be reinstalled with an exact earlier version, while a CRD API-group cutover requires the existing destructive uninstall/reinstall procedure.

## Open Questions

- Whether checksum verification should become a release-blocking requirement once checksum sources for every pinned tool and supported platform are inventoried.
- Whether the GitHub release workflow should be redesigned in a follow-up so the release object is published only after exact install smoke succeeds.
