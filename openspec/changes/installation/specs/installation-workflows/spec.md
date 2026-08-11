## ADDED Requirements

### Requirement: Shared installation behavior

The repository SHALL provide `make install` for installation into an existing Kubernetes cluster and `make try-c9s` for installation into a repository-managed KinD cluster. Both targets SHALL delegate c9s source resolution, compatibility validation, Helm deployment, manager/launcher reconciliation, and post-install verification to the same shared installation behavior. `make install` SHALL NOT create a cluster, install MetalLB, or apply a demo topology.

#### Scenario: Install into an existing cluster

- **WHEN** a user runs `make install` with a valid current Kubernetes context
- **THEN** c9s is installed into that context without creating a KinD cluster, installing MetalLB, or applying demo resources

#### Scenario: Try c9s locally

- **WHEN** a user runs `make try-c9s`
- **THEN** the target ensures its named KinD cluster and MetalLB configuration, invokes the shared c9s installer, and applies a compatible demo topology

### Requirement: Common source selection contract

`make install` SHALL accept `VERSION=latest`, `VERSION=main`, an exact published version with or without a leading `v`, an exact unpublished `0.0.0-<sha>` version, `VERSION=local`, or `VERSION=select`. `make try-c9s` SHALL accept the equivalent selections through `C9S_VERSION`. When the selector variable for either target is unset, the target SHALL behave as latest without prompting.

#### Scenario: Default latest installation

- **WHEN** a user runs either installation target without setting its selector variable
- **THEN** the target resolves and installs the latest stable published release as an exact OCI chart version

#### Scenario: Exact published installation

- **WHEN** a user sets `VERSION=v0.6.0` or `VERSION=0.6.0` for `make install`, or the equivalent `C9S_VERSION` for `make try-c9s`
- **THEN** the target normalizes the selection and requests OCI chart version `0.6.0`

#### Scenario: Mutable main installation

- **WHEN** a user sets `VERSION=main` for `make install`, or `C9S_VERSION=main` for `make try-c9s`
- **THEN** the target requests OCI chart version `0.0.0`, reports it as a mutable development channel, and does not classify it as latest stable

#### Scenario: Exact unpublished installation

- **WHEN** a user sets `VERSION=0.0.0-abc1234` for `make install`, or the equivalent `C9S_VERSION` for `make try-c9s`
- **THEN** the target requests that exact development chart and verifies its source revision and commit-scoped images

#### Scenario: Local checkout installation

- **WHEN** a user sets `VERSION=local` for `make install`, or `C9S_VERSION=local` for `make try-c9s`
- **THEN** the target installs the checkout chart with manager and launcher images built from that checkout

#### Scenario: Interactive selection

- **WHEN** a user sets `VERSION=select` for `make install`, or `C9S_VERSION=select` for `make try-c9s`, in an interactive terminal
- **THEN** the target displays the stable/development selector and installs the selected exact artifact or main channel

### Requirement: Repository-controlled installation toolchain

Installation, try, and e2e workflows SHALL use one set of pinned tool versions and SHALL invoke repository-local versioned GitHub CLI, Helm, kubectl, KinD, yq, and UV binaries by explicit path. Release/development discovery SHALL receive the absolute local GitHub CLI path and SHALL NOT resolve `gh` from host `PATH`. An installation SHALL request only the tools required by its selected source and target type.

#### Scenario: Host PATH contains different tool versions

- **WHEN** unpinned executables with the same names are present earlier on the host `PATH`
- **THEN** the installation invokes the repository-local pinned binaries instead

#### Scenario: Selector invokes GitHub CLI

- **WHEN** the release/development selector queries GitHub
- **THEN** it invokes the downloaded versioned `gh` binary supplied by Make rather than a host executable

#### Scenario: Published existing-cluster install

- **WHEN** an existing-cluster installation selects a published release
- **THEN** it does not download or require KinD

#### Scenario: Local and CI e2e tool versions

- **WHEN** the local and CI e2e paths install their tools
- **THEN** they resolve the same pinned versions used by the installation workflows

### Requirement: Existing-cluster preflight

Before mutating a cluster, `make install` SHALL capture `C9S_CONTEXT` or the current context and SHALL verify with bounded timeouts that the context exists, its API server is reachable and authenticated, required cluster information and permissions are available, the exact selected chart is fetchable, the CRD API group is compatible, and the selected local image transport is usable. Every subsequent Helm and kubectl invocation SHALL explicitly use the captured context.

#### Scenario: No Kubernetes context

- **WHEN** the user runs `make install` without `C9S_CONTEXT` and no current context is configured
- **THEN** the target exits before Helm with an actionable context-selection error

#### Scenario: Context exists but API is unreachable

- **WHEN** the selected context exists but an authenticated API probe times out or fails
- **THEN** the target exits before cluster mutation and identifies the selected context and failed probe

#### Scenario: Context lacks required permissions

- **WHEN** the selected identity cannot create or update resources required by the chart
- **THEN** the target exits before Helm and reports the missing permission

#### Scenario: Context remains stable

- **WHEN** the host's current context changes after installation preflight starts
- **THEN** all installation commands continue using the context captured for that invocation

### Requirement: Exact remote artifact validation

For every stable or development remote source, the installer SHALL probe the exact normalized OCI chart version before invoking Helm and SHALL install with an explicit `--version`. It SHALL NOT rely on Helm's unversioned chart resolution or a mutable image tag to implement latest-release selection. The installer SHALL resolve manager and launcher image references from the selected chart values when they are explicitly provided, without requiring source-revision metadata.

#### Scenario: Selected release artifacts are ready

- **WHEN** the exact OCI chart probe succeeds
- **THEN** Helm installs that exact chart version and the chart-coupled published manager and launcher images

#### Scenario: Release exists before chart publication

- **WHEN** GitHub reports a release but the exact OCI chart probe fails
- **THEN** installation exits before cluster mutation and reports that the release artifact is unavailable or not ready

#### Scenario: Unknown exact version

- **WHEN** the user selects a version for which no OCI chart exists
- **THEN** installation fails before mutating the cluster

#### Scenario: Main chart is selected

- **WHEN** the user selects `main`
- **THEN** installation probes exact chart version `0.0.0` and validates its source revision and immutable commit image pins

#### Scenario: Unpublished chart is selected

- **WHEN** the user selects `0.0.0-abc1234`
- **THEN** installation probes that exact chart and fails before mutation only when the chart itself is unavailable

### Requirement: Identifiable local builds

For local source, the installer SHALL build manager and launcher images for the target cluster's single node platform, SHALL give both images one unique build identity, SHALL pass that identity through the Docker `VERSION` build argument, and SHALL use the same identity in Helm values and the success summary. A dirty checkout identity SHALL differ when image-input files change, while files excluded by `.dockerignore` SHALL NOT change the identity.

#### Scenario: Clean checkout build

- **WHEN** local source is installed from a clean checkout
- **THEN** both images use an identity derived from the checkout commit and embed that identity in their binaries

#### Scenario: Dirty checkout rebuild

- **WHEN** local source is installed twice from dirty checkout states
- **THEN** the second build receives a distinct identity that causes the workload Pod template to reference the new images

#### Scenario: Mixed-platform cluster

- **WHEN** local source targets a cluster whose nodes report more than one OS/architecture combination
- **THEN** installation exits before building and explains that a single local build cannot satisfy the cluster

### Requirement: Local image transport

The installer SHALL load local images directly into a verified KinD cluster. For a non-KinD cluster, it SHALL require a registry prefix pullable by cluster nodes unless the user explicitly selects the project-managed in-cluster registry transport. Static installation SHALL NOT invoke the DevSpace deployment or source-sync pipeline.

#### Scenario: Local source on try KinD

- **WHEN** `make try-c9s C9S_VERSION=local` builds the checkout images
- **THEN** both images are loaded into every node of the try KinD cluster and Helm uses those exact image references with `IfNotPresent`

#### Scenario: Local source on existing KinD

- **WHEN** `make install VERSION=local` targets a cluster verified as KinD
- **THEN** the images are loaded into that KinD cluster without requiring an external registry

#### Scenario: Local source on a non-KinD cluster without registry

- **WHEN** local source targets a non-KinD cluster and neither a pullable registry nor explicit in-cluster transport is configured
- **THEN** installation exits before building and explains the supported registry choices

#### Scenario: Explicit external registry

- **WHEN** local source targets a non-KinD cluster with `C9S_REGISTRY` configured
- **THEN** the installer builds and pushes immutable manager and launcher tags to that registry and Helm uses those references

#### Scenario: Explicit in-cluster registry

- **WHEN** the user explicitly selects the in-cluster registry transport
- **THEN** focused registry, port-forward, BuildKit, and image-reference helpers are reused without starting DevSpace

### Requirement: API-group compatibility preflight

The installer SHALL derive the selected chart's c9s CRD API group from the chart CRDs and SHALL compare it with c9s CRDs already installed in the target cluster. It SHALL refuse an in-place installation when the groups differ and SHALL direct the user to the destructive uninstall/reinstall procedure.

#### Scenario: Compatible same-group reinstall

- **WHEN** the installed c9s CRDs and selected chart use the same API group
- **THEN** the installer permits Helm upgrade or reinstall to proceed

#### Scenario: Incompatible API-group cutover

- **WHEN** legacy `clabernetes.containerlab.dev` CRDs exist and the selected chart uses `c9s.run`, or the reverse
- **THEN** the installer exits before Helm, identifies both groups, and warns that `make uninstall-c9s` deletes all c9s custom resources

### Requirement: Coherent manager and launcher installation

After Helm deployment, the installer SHALL reconcile only the Config singleton's launcher image and launcher image pull policy to the selected source, preserving all unrelated Config fields. It SHALL then verify the Helm chart/source, manager Deployment image, Config launcher image and policy, and manager rollout.

#### Scenario: Existing Config contains an older launcher

- **WHEN** Helm upgrades the manager but Config merge behavior preserves an older launcher image
- **THEN** the installer patches the launcher image and pull policy to the selected source without replacing unrelated Config fields

#### Scenario: Unrelated Config customization exists

- **WHEN** a user has customized resources, labels, selectors, image-pull, or other Config fields
- **THEN** installation leaves those fields unchanged while reconciling manager/launcher compatibility

#### Scenario: Post-install identity mismatch

- **WHEN** the observed manager image, launcher image, chart version, or required local build identity differs from the resolved selection
- **THEN** installation fails and displays expected and observed identities

#### Scenario: Successful installation summary

- **WHEN** all post-install checks pass
- **THEN** the command displays context, namespace, Helm release, source, chart, manager image, launcher image, and observed binary version when available

### Requirement: Source-compatible try demo

`make try-c9s` SHALL apply the checkout demo for local source, SHALL retrieve the demo from the immutable selected Git tag for supported published source, and SHALL retrieve the demo from chart source-revision metadata for main and exact unpublished builds. Published demo support SHALL begin at `v0.6.0`. Demo readiness timeout SHALL produce diagnostics and fail the target.

#### Scenario: Latest or exact supported published demo

- **WHEN** `make try-c9s` installs a supported published release
- **THEN** it applies the demo from that release tag so the manifest API and schema match the installed chart

#### Scenario: Local checkout demo

- **WHEN** `make try-c9s C9S_VERSION=local` installs checkout images and chart
- **THEN** it applies the demo from the checkout

#### Scenario: Development artifact demo

- **WHEN** `make try-c9s` installs main or an exact unpublished commit build
- **THEN** it applies the demo from the full source revision recorded in the selected chart

#### Scenario: Historical release below demo support floor

- **WHEN** `make try-c9s` selects a published release older than `v0.6.0`
- **THEN** it exits before cluster mutation with an actionable unsupported-demo-version error

#### Scenario: Demo does not become ready

- **WHEN** the demo topology fails to reach `TopologyReady` before the timeout
- **THEN** the target prints topology, pod, event, and relevant component diagnostics and exits unsuccessfully

### Requirement: Idempotent rerun and scoped teardown

Re-running either public installation target with the same selection SHALL converge through Helm upgrade/install without requiring manual cleanup. Try cleanup SHALL remove the demo and disposable KinD cluster. Existing-cluster uninstall SHALL retain its explicit destructive CRD and namespace behavior and SHALL use the selected context and repository-controlled tools.

#### Scenario: Repeat an installation

- **WHEN** a successful installation target is run again with the same context and selection
- **THEN** it succeeds without duplicate resources and verifies the same manager and launcher identities

#### Scenario: Repeat try on an existing try cluster

- **WHEN** the named try KinD cluster already exists and its API is healthy
- **THEN** `make try-c9s` reuses it and converges MetalLB, c9s, and the demo

#### Scenario: Clean disposable try environment

- **WHEN** a user runs `make try-c9s-clean`
- **THEN** the demo and named disposable KinD cluster are removed without affecting other clusters

#### Scenario: Uninstall from an existing cluster

- **WHEN** a user runs `make uninstall-c9s` for an explicitly selected context
- **THEN** only that context is targeted and the documented destructive Helm, CRD, and namespace cleanup is performed
