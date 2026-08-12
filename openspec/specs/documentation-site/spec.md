# documentation-site Specification

## Purpose

Provide a local-first, statically prerenderable documentation site for c9s that renders repository-owned Markdown, supports browser-side search, and integrates with repository Makefile workflows.

## Requirements

### Requirement: Isolated local documentation workflow

The repository SHALL provide a pnpm-managed documentation application that can be installed, developed, built, and previewed independently of the c9s operator. The repository SHALL expose a `docs-install` Makefile target for locked dependency installation and a `make serve-docs` target that depends on it and starts a host-reachable documentation development server from the repository root.

#### Scenario: Start the local documentation site

- **WHEN** a contributor runs the documented development command without previously installing documentation dependencies
- **THEN** the locked dependencies are installed and a host-reachable local development server starts with live content updates without requiring Kubernetes or any Clabernetes service

#### Scenario: Run through the root Makefile

- **WHEN** a contributor invokes `make serve-docs` from the repository root
- **THEN** the Makefile completes `docs-install`, delegates to the pnpm-managed documentation package, and starts its development server with live content updates

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

### Requirement: Documentation reading experience

The site SHALL provide responsive navigation, per-page table of contents where headings exist, syntax-highlighted code blocks, light and dark themes, and page metadata derived from content frontmatter.

#### Scenario: Read a guide locally

- **WHEN** a reader opens a guide containing headings and YAML examples
- **THEN** the page displays navigable headings and syntax-highlighted examples in the active theme

#### Scenario: Use a narrow viewport

- **WHEN** a reader opens the site on a narrow viewport
- **THEN** the documentation navigation and page content remain usable without horizontal page overflow

### Requirement: Static documentation build

The production build SHALL prerender the landing page and every documentation route and SHALL emit browser assets that can be served by a generic static file server without a Node.js runtime.

#### Scenario: Build all documentation routes

- **WHEN** the production documentation build runs
- **THEN** it succeeds only after generating a static output containing the landing page and all discovered documentation pages

#### Scenario: Serve the generated output

- **WHEN** the generated output directory is served by a generic static file server
- **THEN** a reader can load a prerendered documentation URL directly and navigate the site without a runtime application server

### Requirement: Browser-side documentation search

The static site SHALL provide full-text search over the included documentation by loading a build-generated search index and executing queries in the browser.

#### Scenario: Search static documentation

- **WHEN** a reader enters a term present in a documentation page
- **THEN** matching documentation results are returned without calling a server-side search endpoint

### Requirement: Site-safe links

Documentation links SHALL resolve to site routes or explicit repository and external URLs rather than depending on GitHub Markdown-relative navigation. The documentation validation workflow SHALL detect unresolved internal documentation routes before the static artifact is accepted.

#### Scenario: Follow a cross-document link

- **WHEN** a reader follows a link from a guide to another included documentation page
- **THEN** the browser opens the corresponding site route

#### Scenario: Validate an unresolved route

- **WHEN** documentation content references an internal route that does not exist
- **THEN** the documentation validation workflow reports the unresolved link and exits unsuccessfully

### Requirement: Documentation application isolation

The documentation application SHALL maintain its own dependency manifest and pnpm lockfile.

#### Scenario: Install documentation dependencies

- **WHEN** a contributor installs dependencies from the documentation package
- **THEN** pnpm resolves the documentation dependency graph

### Requirement: Hosted documentation deployment integration

The documentation application SHALL integrate with the repository's hosted deployment workflows by producing build output at a stable path consumed by Cloudflare Workers Static Assets deployment without additional transformation.

#### Scenario: Build output matches deployment configuration

- **WHEN** the production documentation build completes successfully
- **THEN** the emitted static files are written to the directory referenced by the Cloudflare deployment configuration

### Requirement: Deployment documentation

The repository SHALL document how maintainers configure Cloudflare and GitHub secrets, attach the `c9s.run` custom domain, and run preview and production documentation deployments.

#### Scenario: Maintainer setup instructions

- **WHEN** a maintainer follows the repository documentation for hosted documentation deployment
- **THEN** they can identify the `clabernetes` Worker, the `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` secrets used by workflows, domain steps, and the workflows responsible for preview and production deploys

### Requirement: Runtime compatibility and readiness behavior is documented

The user documentation SHALL explain that Topology compilation warns for lossy fields, fails for
structurally unrealizable resources, and does not expose strict compilation as a clabverter CLI
flag. It SHALL also explain that enabled grouped-node readiness is atomic across nested members,
Docker image healthchecks are honored, process-level readiness is not application readiness, and
TCP/SSH checks apply to the primary Node.

#### Scenario: Reader diagnoses a rejected Topology

- **WHEN** a reader consults the Topology or architecture documentation after a compilation failure
- **THEN** the documentation identifies unsupported pseudo-nodes, special endpoints, unresolved endpoints, unsupported link types, and invalid grouping as fatal compatibility cases

#### Scenario: Reader configures grouped readiness

- **WHEN** a reader consults the Node or LauncherProfile documentation
- **THEN** the documentation explains all-member readiness, Docker healthcheck requirements, process-level limitations, and primary-only TCP/SSH probes
