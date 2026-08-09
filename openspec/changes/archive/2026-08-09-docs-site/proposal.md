## Why

Clabernetes documentation is currently split between plain Markdown in this repository and an external containerlab.dev site, with no local site for navigating, searching, or validating the repository-owned content. A local-first documentation site will make the existing material easier to use and provide a buildable static artifact that can be hosted later without requiring a runtime server.

## What Changes

- Add an isolated `docs-site` web application built with Fumadocs, React Router framework mode, Vite, and pnpm.
- Render repository-owned Markdown as a structured documentation site with navigation, theming, syntax highlighting, and browser-side search.
- Add overview and quickstart content and organize documentation around the primary Node/Link API while retaining Topology guidance.
- Configure every documentation route for static prerendering and produce a host-independent static output.
- Add local development, production build, and preview commands, including a root `make serve-docs` target for starting the local development site.
- Keep the documentation application separate from the existing Next.js operator UI and out of image, Helm, release, and deployment workflows for this change.

## Capabilities

### New Capabilities

- `documentation-site`: Local development, content organization, search, link behavior, and static production output for the Clabernetes documentation site.

### Modified Capabilities

None.

## Impact

- Adds a new pnpm-managed TypeScript package and lockfile for the documentation application.
- Adds Fumadocs, React Router, Vite, Tailwind CSS, and static-search dependencies.
- Reorganizes or adapts content under `docs/` for site navigation and metadata while keeping it repository-owned.
- Adds documentation development/build targets to the root `Makefile`.
- Does not change Clabernetes APIs, controllers, the existing `ui/` application, container images, Helm resources, or current hosting.
