## ADDED Requirements

### Requirement: CRD YAML parsing

The documentation site SHALL parse Kubernetes `CustomResourceDefinition` YAML from repository paths and extract the selected version's `openAPIV3Schema` for rendering.

#### Scenario: Load a single CRD file

- **WHEN** a CRD viewer is configured with a path to a YAML file containing one `CustomResourceDefinition`
- **THEN** the viewer resolves kind, group, version, and schema sections from that file

#### Scenario: Reject multiple CRDs in one file

- **WHEN** a YAML file contains more than one `CustomResourceDefinition` document
- **THEN** the build fails with a clear error identifying the file

#### Scenario: Select storage version by default

- **WHEN** no version is specified and the CRD defines multiple versions
- **THEN** the viewer uses the storage version, falling back to served then first listed version

#### Scenario: Parse controller-gen default quirks

- **WHEN** a CRD schema contains unquoted default values emitted by controller-gen (e.g. bare `=`)
- **THEN** the parser loads the file without error

### Requirement: Interactive schema tree rendering

The documentation site SHALL render CRD schemas as collapsible HTML trees with spec and status sections when present in the schema.

#### Scenario: Display spec and status sections

- **WHEN** the root schema defines `spec` and `status` properties
- **THEN** the viewer renders separate labeled sections for each with their field trees

#### Scenario: Collapse and expand fields

- **WHEN** a reader toggles a field node or uses expand/collapse-all controls
- **THEN** nested fields show or hide without leaving the page

#### Scenario: Show field metadata facts

- **WHEN** a schema field defines type, description, default, enum, format, or numeric range constraints
- **THEN** the viewer displays those metadata facts for the field

#### Scenario: Scalar arrays without fake child nodes

- **WHEN** a field is an array of scalar types (e.g. `array[string]`)
- **THEN** the viewer shows the array type inline without a spurious `items` child row

### Requirement: Field permalink navigation

The documentation site SHALL assign stable fragment identifiers to CRD viewer fields and support hash-based navigation.

#### Scenario: Link to a field

- **WHEN** a reader opens a URL with a CRD viewer field hash
- **THEN** the viewer expands ancestor nodes, scrolls the field into view, and highlights the field row

#### Scenario: Copy field permalink

- **WHEN** a reader activates the permalink control on a field row
- **THEN** the browser navigates to the field hash and copies the full page URL including the hash to the clipboard when supported

### Requirement: MDX integration

The documentation site SHALL expose a `CrdViewer` MDX component that authors can embed in CRD reference pages.

#### Scenario: Embed viewer by CRD path

- **WHEN** an MDX page includes `CrdViewer` with a path to a CRD YAML file under `assets/crd/`
- **THEN** the built page contains the rendered schema tree for that CRD at build time

#### Scenario: Optional viewer props

- **WHEN** an author sets `version`, `title`, `collapsed`, or `showStatus` on `CrdViewer`
- **THEN** the viewer honors those options consistent with mkdocs-crd-viewer macro behavior

### Requirement: Theming

The CRD viewer SHALL render legibly in the site's light and dark themes using documentation design tokens.

#### Scenario: Dark theme readability

- **WHEN** a reader uses the dark theme
- **THEN** CRD viewer surfaces, text, badges, and highlights remain readable and consistent with Fumadocs styling
