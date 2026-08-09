## MODIFIED Requirements

### Requirement: Structured repository-owned documentation

The site SHALL render repository-owned Markdown or MDX as navigable documentation with a landing page, quickstart, architecture and core concepts, guides, examples, and a CRD reference section. Core-concept navigation in the Guide category SHALL present Node and Link as the primary API and SHALL describe Topology as a supported higher-level compatibility resource. CRD field schemas SHALL be presented in a separate CRD Reference documentation category rather than as hand-maintained field tables.

#### Scenario: Navigate the primary API documentation

- **WHEN** a reader opens the core concepts section in the Guide category
- **THEN** the reader can reach Node, Link, LauncherProfile, and Topology documentation with the primary-versus-compatibility relationship stated explicitly

#### Scenario: Reach existing user documentation

- **WHEN** a reader uses the site navigation
- **THEN** the architecture, guide, example, and CRD reference material is reachable through stable site routes

#### Scenario: Browse CRD reference by kind

- **WHEN** a reader opens the CRD Reference category
- **THEN** the reader can navigate to per-kind schema pages for Topology, Node, Link, LauncherProfile, Config, and ImageRequest

## ADDED Requirements

### Requirement: Documentation category navigation

The documentation site SHALL organize sidebar navigation with a category dropdown switcher for **Guide** (user documentation at existing `docs/` routes) and **CRD Reference** (`docs/crd/`). Guide content SHALL remain at the current `docs/` paths without relocation into a wrapper folder.

#### Scenario: Switch categories from the sidebar

- **WHEN** a reader opens the sidebar category dropdown while viewing Guide content
- **THEN** the reader can select CRD Reference and the sidebar shows only CRD Reference pages

#### Scenario: Preserve Guide URL paths

- **WHEN** a reader follows existing Guide links such as `/docs/quickstart` or `/docs/concepts/nodes-and-links`
- **THEN** those routes resolve without an additional `/guide` path segment

#### Scenario: CRD Reference routes

- **WHEN** a reader opens CRD Reference content
- **THEN** pages are served under `/docs/crd` and per-kind subroutes

### Requirement: CRD reference sourced from assets

CRD reference pages SHALL render schemas from the repository's `assets/crd/` YAML files via the interactive CRD viewer rather than hand-maintained field tables.

#### Scenario: Schema matches installed CRDs

- **WHEN** a contributor updates a CRD in `assets/crd/` and rebuilds the documentation site
- **THEN** the CRD reference pages reflect the updated schema without editing Markdown tables
