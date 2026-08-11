## ADDED Requirements

### Requirement: Installable artifact catalog

The repository SHALL provide `make ls-releases`, backed by the UV-run release CLI, that invokes the pinned repository-local GitHub CLI with paginated JSON output to retrieve every page of the repository's GitHub Releases and successful development workflow candidates. It SHALL include the mutable `main` chart when available, exclude drafts and failed/incomplete development runs, probe OCI chart candidates concurrently with the pinned repository-local Helm binary, omit candidates whose exact OCI chart is unavailable, sort installable stable and development entries by publication or workflow-availability time from newest to oldest, and display the newest 10 installable entries by default. `make ls-releases ALL=1` SHALL probe and display the complete installable catalog. The target SHALL NOT require Kubernetes access or mutate a cluster.

#### Scenario: List all published releases

- **WHEN** a user runs `make ls-releases` and the API returns multiple pages
- **THEN** the CLI follows pagination, discovers stable/main/development candidates, probes them concurrently, and displays the newest 10 candidates whose exact OCI charts are available

#### Scenario: API pages are not date ordered

- **WHEN** GitHub returns releases in an order that does not match publication time
- **THEN** the table displays installable releases from newest `published_at` to oldest

#### Scenario: Release chart is unavailable

- **WHEN** a non-draft GitHub Release has no fetchable exact OCI chart
- **THEN** it is omitted from the installable table and the command reports the number of omitted candidates separately

#### Scenario: Complete release catalog is requested

- **WHEN** a user runs `make ls-releases ALL=1`
- **THEN** the CLI probes and displays every stable, main, or successful development candidate whose exact OCI chart is available

#### Scenario: Release probes are concurrent

- **WHEN** multiple OCI chart candidates are checked
- **THEN** independent Helm probes run concurrently with a bounded worker count rather than as one sequential request stream

#### Scenario: Main chart is available

- **WHEN** the latest successful main workflow has published chart `0.0.0`
- **THEN** the catalog includes a distinct `main` row with channel `main` and Version `0.0.0`, which can be supplied as `VERSION=0.0.0` or `VERSION=main`

#### Scenario: Unpublished development chart is available

- **WHEN** a successful manual `Create dev release` workflow has published chart `0.0.0-abc1234`
- **THEN** the catalog includes a distinct development row with Version `0.0.0-abc1234`, which can be supplied as `VERSION=0.0.0-abc1234`, plus source branch, workflow URL, and workflow-availability timestamp

#### Scenario: Displayed version is install input

- **WHEN** a user selects a stable or development row from the table
- **THEN** the displayed Version value is the exact value accepted by `VERSION` for both `make install` and `make try-c9s`

#### Scenario: Draft release is returned

- **WHEN** the GitHub API response contains a draft release
- **THEN** the draft is omitted from the selectable catalog

#### Scenario: Prerelease is returned

- **WHEN** the GitHub API response contains a published prerelease
- **THEN** the entry remains available and is visibly marked as a prerelease

#### Scenario: Release tag lacks a leading v

- **WHEN** a historical release tag is `0.0.7`
- **THEN** the CLI preserves the displayed tag, probes OCI version `0.0.7`, and displays it only when that chart exists

#### Scenario: List releases without a cluster

- **WHEN** no Kubernetes context is configured and the user runs `make ls-releases`
- **THEN** the table is produced without contacting or mutating a Kubernetes cluster

### Requirement: Accurate timestamp semantics

The release catalog SHALL describe GitHub's `published_at` value as the publication time and SHALL label development workflow completion time as availability time. It SHALL NOT present commit time, tagger time, release creation time, or workflow completion time as a Git tag push time.

#### Scenario: Display release time

- **WHEN** a release contains a `published_at` timestamp
- **THEN** the CLI displays it in UTC under a column labeled **Published/available (UTC)**

#### Scenario: Package timestamps are unavailable

- **WHEN** the caller lacks `read:packages` permission
- **THEN** release listing still works through the public Releases API and does not claim to show a GHCR push time

### Requirement: Latest stable resolution

The `latest` selector SHALL resolve through GitHub's latest stable release semantics, SHALL exclude drafts and prereleases, and SHALL return the normalized unprefixed OCI version and original GitHub tag.

#### Scenario: Latest stable release exists

- **WHEN** GitHub identifies `v0.6.0` as the latest stable release
- **THEN** resolution returns GitHub tag `v0.6.0` and OCI version `0.6.0`

#### Scenario: A newer prerelease exists

- **WHEN** a prerelease has a newer publication time than the latest stable release
- **THEN** the default `latest` selector still resolves the latest stable release

### Requirement: Interactive release selection

The CLI SHALL provide a Rich/Typer interactive selector that presents stable releases and development channels distinctly and returns exactly one normalized selection. Rich tables and prompts SHALL be written separately from the machine-readable selected value so Make can capture the result safely.

#### Scenario: User chooses a stable release

- **WHEN** the user selects `v0.5.0`
- **THEN** the selector writes `0.5.0` as its machine-readable result and exits successfully

#### Scenario: User chooses a prerelease

- **WHEN** the user explicitly selects an entry marked as a prerelease
- **THEN** the selector returns its normalized version without presenting it as stable

#### Scenario: User chooses main

- **WHEN** the user explicitly selects the mutable main channel
- **THEN** the selector returns `main` and visibly marks it as moving development content

#### Scenario: User chooses an unpublished commit build

- **WHEN** the user selects available development build `0.0.0-abc1234`
- **THEN** the selector returns that exact version without presenting it as a GitHub Release

#### Scenario: Interactive selector lacks a terminal

- **WHEN** selection is requested without an interactive terminal
- **THEN** the CLI exits with an actionable instruction to use `latest` or an exact version

#### Scenario: User cancels selection

- **WHEN** the user cancels or provides no valid selection
- **THEN** the CLI exits without returning a version and no installation begins

### Requirement: Automation-safe exact selection

An explicit exact stable or `0.0.0-<sha>` development version SHALL be normalizable without requiring successful catalog retrieval. The CLI SHALL validate the supported tag/version syntax and SHALL return non-zero for malformed values.

#### Scenario: Exact version while release API is unavailable

- **WHEN** the user supplies `v0.6.0` and release catalog retrieval fails
- **THEN** the selector returns normalized version `0.6.0` so the installer can perform the authoritative OCI probe

#### Scenario: Malformed exact version

- **WHEN** the user supplies a value that is neither a supported selector nor a semantic release version
- **THEN** the CLI exits unsuccessfully and identifies the invalid value

#### Scenario: Exact unpublished version while Actions API is unavailable

- **WHEN** the user supplies `0.0.0-abc1234` and development-build listing fails
- **THEN** the selector returns that exact version so the installer can perform the authoritative OCI probe

### Requirement: GitHub authentication and failure reporting

The release CLI SHALL invoke the GitHub CLI binary provided by the repository toolchain through an explicit absolute path and SHALL parse its JSON output. The Python process SHALL delegate keychain, `GH_TOKEN`, `GITHUB_TOKEN`, host, pagination, and credential behavior to GitHub CLI rather than reading or storing GitHub credentials. It SHALL report GitHub CLI authentication, network, HTTP, schema, and rate-limit failures as concise actionable errors without a Python traceback.

#### Scenario: GitHub CLI has stored authentication

- **WHEN** the downloaded GitHub CLI can use the user's existing authenticated GitHub CLI configuration
- **THEN** release and development discovery succeeds without the Python script reading credentials

#### Scenario: Optional token is available

- **WHEN** `GH_TOKEN` or `GITHUB_TOKEN` is set
- **THEN** the Python script passes its environment through and GitHub CLI handles that token

#### Scenario: GitHub CLI is unauthenticated

- **WHEN** GitHub CLI cannot authenticate the API request
- **THEN** the selector exits non-zero with actionable `gh auth login` or token guidance and no traceback

#### Scenario: Host GitHub CLI differs

- **WHEN** another `gh` executable is earlier on host `PATH`
- **THEN** the selector still invokes the repository-local versioned GitHub CLI path supplied by Make

#### Scenario: API rate limit is exhausted

- **WHEN** GitHub responds with a rate-limit error
- **THEN** the CLI exits non-zero and reports authentication guidance and reset information when present

#### Scenario: API response is malformed

- **WHEN** a required release field is absent or invalid
- **THEN** the CLI exits non-zero and identifies the invalid GitHub response without emitting a traceback

### Requirement: Exact OCI artifact gate

A GitHub release catalog entry SHALL be treated as a candidate until the installer successfully probes the exact normalized OCI chart. The release CLI and installation output SHALL distinguish release publication from artifact readiness.

#### Scenario: Published release chart exists

- **WHEN** a selected release's exact OCI chart probe succeeds
- **THEN** installation may identify the release as installable and proceed

#### Scenario: Published release chart is not ready

- **WHEN** a selected GitHub release exists but its exact OCI chart probe fails
- **THEN** installation stops before cluster mutation and explains that publication does not prove artifact readiness

### Requirement: Development build discovery

The CLI SHALL present `main` as a distinct mutable channel and SHALL discover recent unpublished-build candidates from manually dispatched `Create dev release` workflow runs through the GitHub Actions API. It SHALL display the source branch, short SHA version, workflow completion time, and workflow URL without describing those values as release publication or package push metadata.

#### Scenario: Successful manual build candidate

- **WHEN** a completed successful `Create dev release` manual run reports source SHA `abc1234...`
- **THEN** the development catalog presents candidate version `0.0.0-abc1234`, its source branch, workflow completion time, and run link

#### Scenario: Failed or incomplete manual run

- **WHEN** a manual workflow run is failed, cancelled, or still running
- **THEN** it is not presented as an available unpublished installation candidate

#### Scenario: Historical candidate artifacts were removed

- **WHEN** a successful manual run is listed but exact chart `0.0.0-<short-sha>` no longer exists
- **THEN** the candidate is marked unavailable when selected and installation does not begin

#### Scenario: Main channel is displayed

- **WHEN** the development catalog is shown
- **THEN** it presents `main` separately as mutable chart `0.0.0` and does not mix it into stable release ordering

### Requirement: Historical demo support visibility

The release catalog SHALL identify that published `try-c9s` demo support begins at `v0.6.0` while allowing older published releases to remain visible for exact existing-cluster installation attempts.

#### Scenario: Supported try release is listed

- **WHEN** a catalog entry is `v0.6.0` or newer
- **THEN** the entry is eligible for published `try-c9s` after its chart and tagged demo probes succeed

#### Scenario: Historical release is listed

- **WHEN** a catalog entry predates `v0.6.0`
- **THEN** it remains visible as historical but is not presented as supporting the automated try demo
