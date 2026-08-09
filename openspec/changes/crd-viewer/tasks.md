## 1. CRD viewer library

- [x] 1.1 Add `yaml` dependency to `docs-site/package.json`
- [x] 1.2 Port `mkdocs_crd_viewer/core.py` to `docs-site/app/lib/crd-viewer/core.ts` (`loadCrdView`, `renderCrdViewer`, `FieldNode` model, controller-gen YAML loader quirk)
- [x] 1.3 Copy `crd-viewer.js` to `docs-site/app/lib/crd-viewer/crd-viewer.js` and export `initCrdViewers` wrapper
- [x] 1.4 Copy and adapt `crd-viewer.css` to Fumadocs tokens (`--color-fd-*`); import from `app.css`
- [x] 1.5 Add Vitest (or existing test runner) and port `test_core.py` scenarios from mkdocs-crd-viewer

## 2. MDX component

- [x] 2.1 Implement `docs-site/app/components/crd-viewer.tsx` with `src`, `version`, `title`, `collapsed`, `showStatus` props
- [x] 2.2 Resolve CRD paths from repo root (`assets/crd/`) at build time
- [x] 2.3 Register `CrdViewer` in `docs-site/app/components/mdx.tsx`
- [x] 2.4 Smoke-test one CRD (Node) in dev: collapse, hash permalink, enum/default facts, dark theme

## 3. Documentation navigation

- [x] 3.1 Create `docs/crd/meta.json` with `root: true`, title, description, and icon
- [x] 3.2 Update `docs/meta.json`: replace `crd-reference` with `crd` in `pages` (no other guide file moves)
- [x] 3.3 Add `DocsLayout` `tabs` in `docs.tsx` for Guide (`/docs`) and CRD Reference (`/docs/crd`)
- [x] 3.4 Verify Guide URLs unchanged and sidebar dropdown switches categories correctly

## 4. CRD reference content

- [x] 4.1 Add `docs/crd/index.md` with overview and links to per-kind pages
- [x] 4.2 Add per-kind MDX pages (topology, node, link, launcher-profile, config, image-request) with short prose and `<CrdViewer src="..."/>`
- [x] 4.3 Remove `docs/crd-reference.md` and hand-maintained field tables
- [x] 4.4 Update internal links across `docs/` from `/docs/crd-reference` to `/docs/crd` and fix old heading anchors

## 5. Build and validation

- [x] 5.1 Confirm `react-router.config.ts` prerender discovers new `docs/crd/**` routes
- [x] 5.2 Run `pnpm check` (typecheck, link check, production build)
- [x] 5.3 Verify search index includes CRD reference pages
- [x] 5.4 Manual check: category switcher, field permalinks, static preview (`pnpm preview`)
