## 1. Toolchain foundation

- [x] 1.1 Consolidate installation, try, and e2e tool version pins into one source of truth and align local and CI GitHub CLI, Helm, kubectl, KinD, yq, and UV versions.
- [x] 1.2 Add pinned GitHub CLI download support and refactor the download targets so all installation tools live at repository-local versioned paths and each source mode downloads only its required tools.
- [x] 1.3 Update e2e, development helpers, and `uninstall-c9s` to use the shared pinned paths and an explicitly selected kube context without changing their intended namespaces.
- [x] 1.4 Add a runnable check that places conflicting fake tools on `PATH` and proves install, try, e2e, and uninstall commands select the repository-local binaries.

## 2. Release discovery and selection

- [x] 2.1 Add a PEP 723 UV script under `hack/` with pinned Rich and Typer dependencies, typed release records, and explicit absolute paths to the downloaded GitHub CLI and Helm binaries.
- [x] 2.2 Invoke repository-local `gh api` for paginated Releases and Actions JSON, delegating credentials to GitHub CLI while implementing draft filtering, prerelease marking, legacy tag normalization, UTC publication formatting, and concise auth/rate-limit/network/schema errors.
- [x] 2.3 Implement non-interactive `latest` and exact-version resolution, keeping exact normalization usable when release catalog retrieval is unavailable.
- [x] 2.4 Implement the interactive selector with Rich output on stderr, one normalized value on stdout, explicit cancellation behavior, and a non-TTY error.
- [x] 2.5 Add `make ls-releases` to concurrently probe stable, main, and development OCI charts and render the newest 10 installable artifacts in a Rich table sorted by publication/availability time, with `ALL=1` displaying the complete catalog without requiring cluster access.
- [x] 2.6 Wire `VERSION=latest|main|vX.Y.Z|X.Y.Z|0.0.0-<sha>|local|select` into `make install` and the equivalent `C9S_VERSION` selections into `make try-c9s`, while keeping an unset value non-interactive.
- [x] 2.7 Add fixture-based script tests for GitHub CLI JSON/subprocess failures, multi-page and unordered responses, drafts, prereleases, unavailable OCI charts, bounded newest-first results, concurrent probes, tags with and without `v`, latest-stable semantics, cancellation, malformed values, authentication, rate limits, network errors, and malformed API data.
- [x] 2.8 Add a development catalog backed by recent successful manual `cicd` runs, with a separate mutable main entry, source branch/SHA, workflow completion metadata, run links, and exact OCI probing on selection.

## 3. Development artifact publication

- [x] 3.1 Document and expose the existing authorized `cicd` manual dispatch for a selected feature branch or tag ref, resolving and recording the exact full source SHA.
- [x] 3.2 Require lint, unit, and e2e success for manually dispatched publication; remove the current feature-branch e2e skip and prevent publication/handoff when a required gate fails.
- [x] 3.3 Publish manager, launcher, clabverter, clicker chart, and c9s chart as `0.0.0-<short-sha>`, with c9s chart values pinned to matching runtime image tags.
- [x] 3.4 Embed the full source SHA in custom chart metadata and add post-push probes for the exact chart plus linux/amd64 and linux/arm64 manager/launcher manifests.
- [x] 3.5 Add an exact unpublished-version KinD install smoke and write the full SHA, artifacts, `make install`, and `make try-c9s` handoff commands to the successful workflow summary.
- [x] 3.6 Change main chart `0.0.0` packaging to embed the full main SHA and pin manager/launcher to immutable `0.0.0-<short-sha>` images while retaining `dev-latest` only as a development alias.
- [x] 3.7 Add workflow-level checks for selected-ref identity, e2e publication blocking, exact custom artifacts, source metadata, main image pinning, artifact-probe failure, and handoff output.

## 4. Shared context and artifact preflight

- [x] 4.1 Add shared install variables for context, namespace, Helm release, OCI chart, timeout, source selection, local transport, and registry while preserving compatible existing overrides.
- [x] 4.2 Implement context capture and bounded existence, reachability, authentication, node-discovery, and required-permission checks that run before Helm and pass the captured context to every command.
- [x] 4.3 Probe every stable, main, or unpublished selection with `helm show chart --version <exact>` and report an unavailable artifact without mutating the cluster.
- [x] 4.4 Resolve manager/launcher image references from selected chart values without requiring source-revision metadata during `make install`.
- [x] 4.5 Inspect the selected chart CRDs to derive its c9s API group and inspect the cluster for installed legacy and `c9s.run` CRDs.
- [x] 4.6 Block cross-group installation before Helm with the destructive `make uninstall-c9s` warning, while allowing same-group reinstall and version changes.
- [ ] 4.7 Add focused preflight checks for no context, unknown context, stale or unreachable API endpoint, unauthenticated context, missing permissions, missing OCI version, invalid development metadata, and both API-group mismatch directions.

## 5. Local build identity and image transport

- [x] 5.1 Generate one local build identity from the clean commit or a unique dirty build ID and pass it as image tag and Docker `VERSION` build argument to manager and launcher.
- [x] 5.2 Reuse the cluster platform detector for local installs, pass the single target platform to BuildKit, and fail clearly for empty or heterogeneous platform results.
- [ ] 5.3 Refactor the existing e2e image build/load behavior into shared helpers that build both images, load them into every verified KinD node, and use exact `IfNotPresent` image references.
- [x] 5.4 Detect or require the existing KinD cluster name without relying only on the context name prefix, and validate it before direct image loading.
- [x] 5.5 Implement non-KinD external-registry transport using an explicit `C9S_REGISTRY`, existing registry-auth validation, immutable pushes, and documented image-pull requirements.
- [ ] 5.6 Implement the explicit in-cluster registry transport by reusing focused registry deployment, port-forward, BuildKit, and image-reference helpers without invoking DevSpace.
- [ ] 5.7 Add runnable checks for clean and dirty identity changes, build-argument embedding, KinD loading, missing non-KinD registry, external push command construction, opt-in in-cluster transport, and mixed platforms.

## 6. Shared Helm installation and verification

- [x] 6.1 Add the shared Helm upgrade/install target for stable, main, unpublished, and local charts with exact chart/image values, namespace creation, proxy values, bounded wait, and manager rollout status.
- [x] 6.2 Wait for the selected API group's Config singleton and patch only launcher image and pull policy so manager/launcher versions converge without overwriting unrelated configuration.
- [x] 6.3 Implement post-install checks for Helm chart/source, full development source revision, manager Deployment image, Config launcher image and policy, rollout health, and embedded/local build identity where available.
- [x] 6.4 Print a success summary containing context, namespace, release/channel, source revision, chart, manager image, launcher image, and observed binary version, and print expected-versus-observed details on mismatch.
- [ ] 6.5 Add an integration check that seeds unrelated Config customizations and an old launcher image, runs installation, and verifies only launcher image fields changed.
- [ ] 6.6 Add same-selection rerun and same-API-group version-switch checks that prove idempotency and manager/launcher coherence.

## 7. Public make install workflow

- [x] 7.1 Add the documented `make install` entrypoint that selects the required pinned tools and invokes resolution, preflight, local transport when selected, shared Helm installation, and verification.
- [x] 7.2 Ensure stable and development remote `make install` modes do not require Docker or KinD and local non-KinD install fails before building when no supported registry transport is configured.
- [x] 7.3 Update `uninstall-c9s` to honor the same context, namespace, release, and pinned tool contract while retaining explicit destructive CRD cleanup.
- [ ] 7.4 Add an acceptance fixture that creates a KinD cluster independently, invokes `make install` for stable, main, unpublished, and local sources, verifies no try-only resources were created, and tears it down.

## 8. try-c9s workflow

- [x] 8.1 Make try KinD creation idempotent, refresh a dedicated state-directory kubeconfig, and prove the named cluster API before reusing it.
- [ ] 8.2 Retain and converge the existing dual-stack/MetalLB and proxy behavior while calling the shared installer for all source selections.
- [x] 8.3 Use the checkout demo for local source, fetch the immutable selected-tag demo for stable releases at or above `v0.6.0`, and fetch the source-revision demo for main/unpublished builds.
- [x] 8.4 Reject unsupported historical releases, missing development source metadata, and unavailable source-revision demos before applying mismatched resources.
- [x] 8.5 Make topology readiness timeout fail after collecting topology, pod, event, manager, and launcher diagnostics.
- [x] 8.6 Keep access output and `try-c9s-clean` scoped to the selected demo and named disposable cluster, including safe repeated cleanup.
- [ ] 8.7 Add acceptance checks for default latest, exact stable, main, unpublished commit, local checkout, existing-cluster reuse, unsupported historical demo, bad artifact, forced demo failure, access output, and cleanup.

## 9. CI and artifact confidence

- [ ] 9.1 Add a Linux amd64 installation acceptance job that exercises local `try-c9s`, existing-cluster `make install`, rerun, source switching, compatibility failure, and teardown through public targets.
- [x] 9.2 Add stable-release, exact unpublished, and mutable-main smokes after their images and charts are pushed, verifying chart metadata, manager, launcher, CRD group, demo readiness, and cleanup.
- [ ] 9.3 Add Linux arm64 smoke coverage for repository-local tool downloads and at least one remote or local KinD installation path.
- [ ] 9.4 Preserve actionable diagnostic upload/output on acceptance failures without adding destructive Docker cache cleanup to local Make targets.

## 10. Documentation and final validation

- [x] 10.1 Rewrite the local quickstart with requirements, resource expectations, latest/main/exact/select/local commands, source-matched demo behavior, success output, diagnostics, access, and cleanup.
- [x] 10.2 Add an existing-cluster installation guide covering context safety, stable and development channels, accurate timestamp labels, KinD loading, external and opt-in in-cluster registries, architecture, proxy/private registry requirements, verification, and uninstall.
- [x] 10.3 Document manual feature-ref dispatch, `0.0.0-<short-sha>` handoff, validation gates, source metadata, artifact availability, and the difference between mutable `0.0.0` main and latest stable.
- [x] 10.4 Update upgrading documentation for API-group preflight and destructive cutover, including manager/launcher coupling, same-group version changes, rollback limits, and Config preservation.
- [ ] 10.5 Synchronize root README, Make help, chart README, examples, and troubleshooting links with the new public contract and repository-local tool behavior.
- [ ] 10.6 Run selector tests, workflow checks, unit tests, chart tests, generated-artifact verification, local e2e, stable/main/unpublished installation acceptance, documentation checks, and Make help review; record or fix every failure attributable to this change.
