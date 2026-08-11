## MODIFIED Requirements

### Requirement: Structured repository-owned documentation

The site SHALL render repository-owned Markdown or MDX as navigable documentation with a landing page, disposable local quickstart, existing-cluster installation guide, architecture and core concepts, guides, examples, and a CRD reference section. The installation documentation SHALL cover latest and exact stable releases, mutable main, exact unpublished commit builds, interactive selection, and local-checkout source selection; Kubernetes context behavior; KinD and registry image transport; manager/launcher verification; API-group compatibility; troubleshooting; and scoped teardown. The contributor documentation SHALL explain how to dispatch and share an unpublished build from a feature ref and how main chart `0.0.0` differs from latest stable. Core-concept navigation in the Guide category SHALL present Node and Link as the primary API and SHALL describe Topology as a supported higher-level compatibility resource. CRD field schemas SHALL be presented in a separate CRD Reference documentation category rather than as hand-maintained field tables.

#### Scenario: Navigate the primary API documentation

- **WHEN** a reader opens the core concepts section in the Guide category
- **THEN** the reader can reach Node, Link, LauncherProfile, and Topology documentation with the primary-versus-compatibility relationship stated explicitly

#### Scenario: Reach existing user documentation

- **WHEN** a reader uses the site navigation
- **THEN** the architecture, installation, guide, example, and CRD reference material is reachable through stable site routes

#### Scenario: Follow the disposable quickstart

- **WHEN** a reader wants to try c9s without an existing cluster
- **THEN** the quickstart documents requirements, `make try-c9s` latest behavior, exact and local selection, expected verification output, access, failure diagnostics, and cleanup

#### Scenario: Install into an existing cluster

- **WHEN** a reader already has a Kubernetes cluster
- **THEN** the installation guide documents context selection and preflight, latest and exact releases, main and unpublished development builds, local images for KinD, registry requirements for non-KinD clusters, verification, and uninstall

#### Scenario: List installable releases

- **WHEN** a reader wants to inspect published versions before installation
- **THEN** the documentation identifies `make ls-releases` as a cluster-independent newest-to-oldest table of releases whose exact OCI charts are available

#### Scenario: Publish and share a feature build

- **WHEN** a contributor wants others to test a feature branch commit
- **THEN** the contributor documentation explains the authorized manual dispatch, `0.0.0-<short-sha>` artifact identity, validation gates, workflow handoff commands, and absence of a GitHub Release

#### Scenario: Choose main rather than latest

- **WHEN** a reader wants the latest successfully published main commit
- **THEN** the documentation identifies chart `0.0.0` as mutable main, explains its pinned commit images and source revision, and distinguishes it from latest stable

#### Scenario: Understand API-group incompatibility

- **WHEN** a reader is moving between a legacy release and a `c9s.run` release or local checkout
- **THEN** the installation and upgrade documentation explains why in-place installation is blocked and that uninstalling CRDs deletes all c9s custom resources

#### Scenario: Choose local image transport

- **WHEN** a reader wants to install checkout images
- **THEN** the documentation distinguishes KinD image loading, explicit external registry push, and opt-in in-cluster registry behavior and lifecycle

#### Scenario: Browse CRD reference by kind

- **WHEN** a reader opens the CRD Reference category
- **THEN** the reader can navigate to per-kind schema pages for Topology, Node, Link, LauncherProfile, Config, and ImageRequest
