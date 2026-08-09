## 1. Scaffold the documentation application

- [x] 1.1 Create the isolated `docs-site/` pnpm package with React, React Router framework mode, Vite, TypeScript, Tailwind CSS 4, Fumadocs, and package-local configuration
- [x] 1.2 Configure the Fumadocs MDX content collection to load Markdown and MDX from `../docs` and restrict Vite filesystem access to the required repository paths
- [x] 1.3 Add the Fumadocs root provider, global styles, shared layout options, and reusable MDX components
- [x] 1.4 Generate and retain a package-local `pnpm-lock.yaml` without changing the npm-managed `ui/` package

## 2. Implement routes and static behavior

- [x] 2.1 Implement the landing route and wildcard documentation route with Fumadocs page tree, page metadata, table of contents, relative-link handling, and not-found behavior
- [x] 2.2 Configure React Router with runtime SSR disabled and prerender paths for `/` and every page discovered from the Fumadocs source
- [x] 2.3 Configure build-generated search data and the Fumadocs search UI to execute queries entirely in the browser without an API route
- [x] 2.4 Add package scripts for development, type checking, content checking, static building, and previewing `build/client`

## 3. Establish the documentation structure

- [x] 3.1 Add Fumadocs navigation metadata and frontmatter for the existing architecture, guide, and CRD reference documents
- [x] 3.2 Create the overview and local quickstart pages using current repository installation and `try-c9s` workflows
- [x] 3.3 Create core-concept pages for Node and Link, LauncherProfile, and Topology that establish the primary API and compatibility relationships without duplicating field reference content
- [x] 3.4 Create a curated examples index that links readers to the relevant repository example directories and manifests
- [x] 3.5 Normalize links in included documentation so cross-document links use site routes and links outside `docs/` use explicit repository or external URLs

## 4. Add validation and repository workflows

- [x] 4.1 Implement a Markdown and MDX link checker that maps content files to site routes and fails on unresolved internal documentation links
- [x] 4.2 Add a `docs-install` Makefile target and make the host-reachable root `serve-docs` target depend on it before delegating to the pnpm development script in `docs-site/`, plus targets for documentation checking, static building, and preview
- [x] 4.3 Document the local documentation workflow and clarify that `docs-site/` uses pnpm independently of the npm-managed operator UI

## 5. Verify the static site

- [x] 5.1 Run the package type check, content/link check, and production build and confirm that all expected documentation routes are emitted under `docs-site/build/client`
- [x] 5.2 Serve the generated static artifact and verify direct page loads, navigation, syntax highlighting, theme switching, and browser-side search without a runtime server
- [x] 5.3 Verify responsive navigation on a narrow viewport and confirm that editing a file under `docs/` updates the local development site
