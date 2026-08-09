## Why

The c9s documentation site exposes CRD field reference as a long hand-maintained Markdown page with tables that drift from the controller-generated schemas in `assets/crd/`. Readers need an interactive, schema-accurate reference (collapsible fields, permalinks, types, descriptions, enums, defaults) comparable to production CRD doc tools, without maintaining duplicate content.

## What Changes

- Port the mkdocs-crd-viewer rendering model (from [eda-labs/mkdocs-crd-viewer](https://github.com/eda-labs/mkdocs-crd-viewer)) into the Fumadocs/React docs site: TypeScript core parser/renderer, bundled CSS, and client interactivity script.
- Add an MDX `<CrdViewer>` component that reads CRD YAML from `assets/crd/` and renders interactive schema trees at build time.
- Restructure documentation navigation into two sidebar categories with a dropdown: **Guide** (existing pages at `docs/` root) and **CRD Reference** (`docs/crd/` only).
- Replace the monolithic `docs/crd-reference.md` field tables with per-kind pages under `docs/crd/`, keeping short conceptual prose where needed.
- CRD pages live under `/docs/crd/*`; all other guide URLs stay where they are today (`/docs`, `/docs/quickstart`, etc.) — no `(guide)` wrapper folder.

## Capabilities

### New Capabilities

- `crd-viewer`: Interactive CRD schema rendering in the docs site (parse CRD YAML, HTML tree, collapse/expand, field permalinks, metadata facts).

### Modified Capabilities

- `documentation-site`: Navigation becomes multi-root (Guide + CRD Reference); CRD reference content is generated from CRD YAML via the viewer instead of hand-maintained tables; new routes under `/docs/crd/`.

## Impact

- **docs/** — existing guide pages stay at `docs/` root; add `docs/crd/` (`root: true`); remove `docs/crd-reference.md`; update `docs/meta.json` to list `crd` instead of `crd-reference`.
- **docs-site/** — new `app/lib/crd-viewer/`, `app/components/crd-viewer.tsx`, CSS import, MDX registration; `DocsLayout` `tabs` for Guide + CRD Reference dropdown; possible Vitest tests ported from mkdocs-crd-viewer.
- **assets/crd/** — consumed as schema source of truth (no schema edits required).
- **Links** — update internal links from `/docs/crd-reference` to `/docs/crd`; Guide routes unchanged.
- **Dependencies** — YAML parsing in docs-site (e.g. `yaml`); no Python/MkDocs in the docs build path.
- **Search / prerender** — must include new CRD reference routes and content.
