## MODIFIED Requirements

### Requirement: Structured repository-owned documentation

The site SHALL render repository-owned Markdown or MDX as navigable documentation with a landing
page, quickstart, architecture and core concepts, guides, examples, and a CRD reference section.
Core-concept navigation in the Guide category SHALL present Node and Link as the primary API and
SHALL describe Topology as a supported higher-level compatibility resource. CRD field schemas SHALL
be presented in a separate CRD Reference documentation category rather than as hand-maintained
field tables. The reference SHALL describe only the five direct-runtime CRDs and SHALL NOT publish
an ImageRequest page.

#### Scenario: Navigate the primary API documentation

- **WHEN** a reader opens the core concepts section in the Guide category
- **THEN** the reader can reach Node, Link, LauncherProfile, and Topology documentation with the primary-versus-compatibility relationship stated explicitly

#### Scenario: Reach existing user documentation

- **WHEN** a reader uses the site navigation
- **THEN** the architecture, guide, example, and CRD reference material is reachable through stable site routes

#### Scenario: Browse CRD reference by kind

- **WHEN** a reader opens the CRD Reference category
- **THEN** the reader can navigate to per-kind schema pages for Topology, Node, Link, LauncherProfile, and Config, with no ImageRequest route or generated schema entry
