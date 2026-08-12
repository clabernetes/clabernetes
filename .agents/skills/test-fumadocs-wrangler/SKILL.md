---
name: test-fumadocs-wrangler
description: Builds and validates the Fumadocs React Router static site through Wrangler with headless browser checks. Use when testing production-like docs behavior, redirects, direct loads, refreshes, Cloudflare SPA fallback, or temporary Wrangler previews without committing.
---

# Test Fumadocs with Wrangler

Use `scripts/test.sh` for production-like docs tests. It:

1. Builds `docs-site`.
2. Installs Wrangler, Playwright, npm cache, and Chromium only under `mktemp`.
3. Copies Wrangler configuration and links build assets inside that temporary directory.
4. Runs `wrangler dev` from the temporary directory.
5. Checks direct loads, refreshes, redirects, console errors, page errors, and failed requests in Chromium.
6. Stops Wrangler and deletes the temporary directory on success, failure, or interruption.

## Run

Pass paths as arguments. Use quoted `source=>destination` entries for redirects:

```bash
bash .agents/skills/test-fumadocs-wrangler/scripts/test.sh \
  /docs/quickstart \
  /docs/release-notes/0.7 \
  '/docs/release-notes/0.7.0=>/docs/release-notes/0.7' \
  '/docs/release-notes/0.7.0/=>/docs/release-notes/0.7'
```

With no arguments, the script checks `/docs`.

To upload the passing build to an unauthenticated temporary Cloudflare account:

```bash
DEPLOY_TEMPORARY=1 bash .agents/skills/test-fumadocs-wrangler/scripts/test.sh /docs
```

Temporary deployments can show Cloudflare human verification to automated browsers. Treat local Wrangler + Chromium as the automated pass/fail result; report the temporary URL for manual inspection.

## Rules

- Never install testing tools into the repository or change `package.json`/lockfiles.
- Do not use `pnpm dlx`, global installs, or repository-local Wrangler state.
- Do not create `.wrangler/` in the repository.
- If project dependencies are missing, stop and report that `docs-site/node_modules` must be installed separately.
- Run local checks before any external upload.
- Only deploy when the user requests a temporary preview.
- Do not update production or an existing preview alias unless the user explicitly requests it and credentials are already available.
- After testing, confirm no Wrangler process remains and no temporary files were added to Git.
