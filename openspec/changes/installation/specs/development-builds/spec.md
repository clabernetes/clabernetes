## ADDED Requirements

### Requirement: Manually dispatched unpublished build

The repository SHALL provide an authorized GitHub Actions manual dispatch that builds an unpublished c9s artifact set from the exact commit resolved by a selected repository branch or tag ref. The workflow SHALL publish OCI artifacts without creating a GitHub Release.

#### Scenario: Build a feature branch head

- **WHEN** a developer dispatches the workflow with package push enabled on a feature branch
- **THEN** the workflow resolves and records that branch's full head SHA and builds artifacts from that commit

#### Scenario: Build a tagged commit

- **WHEN** a developer dispatches the workflow on a repository tag
- **THEN** the workflow builds artifacts from the commit resolved by that tag

#### Scenario: Detached commit has no ref

- **WHEN** a developer wants to build a commit that is not the head of a selectable repository branch or tag
- **THEN** the documented workflow requires the developer to create or select a repository ref pointing to that commit

#### Scenario: Unpublished build completes

- **WHEN** the manual publication succeeds
- **THEN** no GitHub Release object is created and the artifacts are not eligible for latest-stable resolution

### Requirement: Commit-scoped artifact identity

An unpublished build SHALL use version `0.0.0-<short-sha>` for its manager, launcher, clabverter, clicker chart, and c9s chart. The c9s chart SHALL pin manager and launcher values to that exact version and SHALL record the resolved full source SHA in chart metadata.

#### Scenario: Publish commit artifacts

- **WHEN** the resolved source SHA begins with `abc1234`
- **THEN** all images and charts are published with version `0.0.0-abc1234`

#### Scenario: Inspect unpublished chart values

- **WHEN** a user inspects chart `0.0.0-abc1234`
- **THEN** its manager and launcher image values reference matching `0.0.0-abc1234` tags

#### Scenario: Inspect unpublished chart metadata

- **WHEN** a user inspects the unpublished chart metadata
- **THEN** it identifies the full repository source SHA used by the build

### Requirement: Unpublished build validation gate

An unpublished artifact publication SHALL require successful lint, unit, e2e, multi-architecture image publication, chart publication, exact artifact probes, and an exact-version installation smoke. Publication or handoff SHALL not be reported as successful when a required gate fails.

#### Scenario: Feature branch e2e fails

- **WHEN** e2e fails for a manually dispatched feature build
- **THEN** image/chart publication or successful handoff is blocked

#### Scenario: Published image lacks a required platform

- **WHEN** either manager or launcher lacks linux/amd64 or linux/arm64 in its manifest
- **THEN** the unpublished build fails its artifact verification

#### Scenario: Exact chart is unavailable after push

- **WHEN** the workflow cannot probe chart `0.0.0-<short-sha>` after publication
- **THEN** the workflow fails and does not advertise the build as installable

#### Scenario: Exact install smoke fails

- **WHEN** the exact unpublished chart cannot be installed and verified in the smoke cluster
- **THEN** the workflow fails and retains actionable diagnostics

### Requirement: Unpublished build handoff

After every successful unpublished build, the workflow SHALL publish a job summary containing the resolved full SHA, exact development version, artifact references, and copyable `make install` and `make try-c9s` commands.

#### Scenario: Share a feature build

- **WHEN** unpublished build `0.0.0-abc1234` succeeds
- **THEN** the workflow summary includes `make install VERSION=0.0.0-abc1234` and `make try-c9s C9S_VERSION=0.0.0-abc1234`

#### Scenario: Branch moves after build

- **WHEN** new commits are pushed to the source branch
- **THEN** the earlier workflow summary and chart metadata continue identifying the immutable commit used by `0.0.0-abc1234`

### Requirement: Mutable main development channel

Every successful main publication SHALL overwrite OCI chart `0.0.0` as the explicit mutable main channel. That chart SHALL record the full main source SHA and SHALL pin manager and launcher to immutable `0.0.0-<short-sha>` image tags from the same workflow run. The existing `dev-latest` image aliases MAY continue for development tooling but SHALL NOT determine images installed through the main chart.

#### Scenario: Main merge publishes edge artifacts

- **WHEN** commit `def5678...` is successfully published from main
- **THEN** chart `0.0.0` records that full commit and pins both runtime images to `0.0.0-def5678`

#### Scenario: Main advances

- **WHEN** a later main commit publishes successfully
- **THEN** chart `0.0.0` moves to the later commit while the earlier exact `0.0.0-<short-sha>` image tags remain immutable

#### Scenario: dev-latest moves independently

- **WHEN** a `dev-latest` alias is updated
- **THEN** an already fetched main chart's explicit manager and launcher values do not resolve through that alias

### Requirement: Development artifact installation

The `make install` workflow SHALL treat `VERSION=main` as exact chart version `0.0.0` in the development channel and SHALL accept exact unpublished versions matching `0.0.0-<sha>`. The `make try-c9s` workflow SHALL accept the equivalent values through `C9S_VERSION`. Both SHALL probe the exact chart and SHALL never classify either channel as latest stable. Only the try workflow requires source-revision metadata to retrieve a matching demo.

#### Scenario: Install mutable main

- **WHEN** a user runs `make install VERSION=main`
- **THEN** the installer probes chart `0.0.0`, reports its recorded source revision, and verifies its pinned manager and launcher images

#### Scenario: Install exact unpublished build

- **WHEN** a user runs `make install VERSION=0.0.0-abc1234`
- **THEN** the installer probes and installs that exact chart and verifies matching commit-scoped images

#### Scenario: Development chart lacks source metadata

- **WHEN** a selected main or unpublished chart does not record a full source revision
- **THEN** `make install` may proceed when the exact chart is available, while `make try-c9s` refuses to apply a source-mismatched demo

#### Scenario: Development workflow run exists but artifacts do not

- **WHEN** a successful historical workflow run is discoverable but its exact chart probe fails
- **THEN** the build is reported as unavailable and installation does not begin

### Requirement: Development-source try demo

For main and exact unpublished builds, `make try-c9s` SHALL retrieve the demo from the full source revision recorded in chart metadata and SHALL apply it only after validating the exact chart and image references.

#### Scenario: Try unpublished feature build

- **WHEN** a user runs `make try-c9s C9S_VERSION=0.0.0-abc1234`
- **THEN** the demo is retrieved from the chart's full source SHA and therefore matches the built feature code

#### Scenario: Source demo is unavailable

- **WHEN** the chart's recorded source revision does not contain the expected demo manifest
- **THEN** `make try-c9s` exits before applying a mismatched checkout or release demo
