## Context

Clabernetes is a Go-first repository with one isolated npm-managed Next.js application under `ui/`. Repository-owned user documentation consists of plain Markdown under `docs/`, related READMEs and manifests under `examples/`, and links to separately published documentation at containerlab.dev. There is no documentation build, navigation model, search index, or local link validation.

The documentation site must work locally first and produce a static artifact suitable for an undecided hosting provider later. It must not couple its dependencies or runtime to the operator UI, Kubernetes, or a server-side search API. Existing documentation also needs an information architecture that reflects Node and Link as the primary API while retaining Topology as a supported higher-level resource.

## Goals / Non-Goals

**Goals:**

- Provide a pnpm-managed local documentation application with fast Vite development.
- Keep repository-owned documentation as the source of truth.
- Deliver Fumadocs navigation, responsive layouts, themes, code rendering, and static browser search.
- Prerender every route into an artifact that can be served by a generic static file server.
- Introduce overview, quickstart, and core-concept pages that establish the Node/Link-first model.
- Validate internal documentation links before considering a static build complete.

**Non-Goals:**

- Select or configure a hosting provider, public domain, analytics service, or deployment workflow.
- Replace or embed the existing Next.js operator UI.
- Package the documentation as a container or add it to Helm, DevSpace, image, or release pipelines.
- Generate the CRD reference from OpenAPI or CRD schemas in this change.
- Import every example README into the site; the initial examples section will curate and link to repository examples.
- Remove or redirect the currently published containerlab.dev documentation.

## Decisions

### Use React Router framework mode on Vite

The application will use React Router framework mode, the Fumadocs Vite MDX plugin, Fumadocs UI, Tailwind CSS 4, and TypeScript. React Router provides a documented Fumadocs integration and an explicit static prerendering model while keeping Vite as the development and build tool.

Waku was considered because it is minimal, Vite-based, and supports static rendering. It is not selected because its React Server Component model and smaller ecosystem add a second unfamiliar framework without providing a material advantage for a static documentation site. Next.js was considered because `ui/` already uses it, but it would violate the Vite preference and would still need to remain a separate application. A bare Vite SPA was rejected because it would require recreating framework routing and data-loading integration that Fumadocs already supports through React Router.

### Keep the application and content separate

Application code, configuration, scripts, dependencies, and `pnpm-lock.yaml` will live under `docs-site/`. Documentation content and Fumadocs navigation metadata will remain under the existing `docs/` directory, which the application will register as its content collection.

This avoids duplicating content and keeps Markdown useful in repository browsing. Because the content directory is outside the Vite application root, Vite filesystem access will be limited explicitly to the repository `docs/` directory. Moving all content into `docs-site/content/` was rejected because it would break existing repository links and make site implementation details own the documentation source of truth.

### Use a static-first route model

The landing page will use `/`; documentation pages will use `/docs/...`. React Router will run with runtime SSR disabled and will derive prerender paths from the Fumadocs source, including every Markdown and MDX page. The production artifact will be `docs-site/build/client`.

Although runtime SSR is disabled, route components must remain build-time-render-safe because React Router renders pages during prerendering. The first phase assumes the artifact is hosted at a domain root. Base-path handling for subdirectory hosting will be decided with the hosting provider.

### Use browser-side static search

The build will generate a search index from the Fumadocs source and the search UI will query that index in the browser. No `/api/search` route will be created. This makes local preview exercise the same search architecture that the eventual static deployment will use.

### Evolve the information architecture without duplicating reference content

The initial navigation will contain:

- Overview and local quickstart
- Architecture
- Core concepts for Node and Link, LauncherProfile, and Topology
- Existing how-to guides
- A curated examples index linking to repository examples
- CRD reference

New concept pages will explain relationships and link into the existing detailed CRD reference instead of copying field-by-field reference material. Existing guide and reference files will gain frontmatter and link updates as needed. Topology-oriented guides remain valid but will identify when the direct Node/Link workflow is preferable.

### Normalize links for both the site and repository

Links between included documentation files will resolve through Fumadocs site routes. Links to manifests, example directories, or other repository files outside `docs/` will use explicit repository URLs. External links remain unchanged.

A documentation check script will parse Markdown and MDX links, map content paths to generated routes, and fail on unresolved internal documentation targets. The root documentation build target will run type/build validation and this link check.

### Keep pnpm local to the documentation package

`docs-site/` will declare its package manager and maintain its own lockfile. The repository will not introduce a root JavaScript workspace, and `ui/` will continue using its existing npm lockfile. A `docs-install` Makefile target will install from the frozen package lock. The root `make serve-docs` target will depend on that installation step, delegate to the pnpm development script in `docs-site/`, and bind Vite to a host-reachable address while keeping live content updates. Separate package scripts and Makefile targets will cover checking, static building, and previewing.

## Risks / Trade-offs

- **Content outside the Vite root may cause watch or access failures** → Configure the content collection and Vite filesystem allowlist explicitly, then verify live reload for `docs/` changes.
- **Mixed npm and pnpm usage can confuse contributors** → Keep each package isolated and document that `ui/` uses npm while `docs-site/` uses pnpm.
- **Static React Router routes can accidentally depend on runtime loaders** → Disable runtime SSR from the start, prerender every content route, and avoid actions or server-only endpoints.
- **Search can silently reintroduce a server dependency** → Use only the static browser client and verify search against the generated artifact.
- **Existing Markdown links assume GitHub paths** → Update included content links and run deterministic link validation.
- **Manual CRD reference can continue drifting from generated schemas** → Clearly leave reference generation as follow-up work rather than expanding this change.
- **Future subpath hosting may require route and asset-base changes** → Assume root hosting for the local phase and treat provider-specific base paths as a deployment design decision.
- **Reorganizing content can obscure git history** → Prefer adding frontmatter and small concept pages over moving large existing documents in the first iteration.

## Migration Plan

1. Scaffold the isolated React Router and Fumadocs application under `docs-site/`.
2. Connect the external `docs/` content directory and render a minimal landing page plus one existing page.
3. Configure static prerendering and browser-side search before migrating the remaining content.
4. Add navigation metadata, overview, quickstart, concepts, and examples index pages.
5. Adapt existing documents with frontmatter and site-safe links.
6. Add link validation and root Makefile workflows.
7. Verify local development, static build, static preview, direct page loading, responsive navigation, themes, and search.

Rollback consists of removing `docs-site/`, the added documentation metadata/pages, and the root Makefile targets. No application runtime, API, or deployment state is migrated.

## Open Questions

- Which static hosting provider and public URL will be used after the local phase?
- Will the new site eventually replace the Clabernetes section on containerlab.dev or coexist with it?
- Should CRD/OpenAPI reference generation become a separate follow-up change?
