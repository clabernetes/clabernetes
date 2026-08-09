## 1. Cloudflare deployment configuration

- [x] 1.1 Add `wrangler.toml` at the repository root with Worker name `clabernetes`, `preview_urls = true`, and `[assets].directory = "./docs-site/build/client"`
- [x] 1.2 Confirm the assets directory matches `make build-docs` output and add `not_found_handling` only if direct URL loads fail during manual verification
- [x] 1.3 Document how workflows consume `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`, plus Cloudflare custom-domain setup for `c9s.run`, in `docs-site/README.md` or repository docs

## 2. Pull-request preview workflow

- [x] 2.1 Add `.github/workflows/docs-preview.yaml` triggered on `pull_request` types that include synchronize, running independently of `.github/workflows/cicd.yaml`
- [x] 2.2 Run `make check-docs` then `make build-docs` in the preview workflow before any Cloudflare upload
- [x] 2.3 Deploy with `wrangler versions upload`, capturing the versioned commit preview URL and passing `--preview-alias pr-<PR_NUMBER>` for the rolling PR preview
- [x] 2.4 Post or update a pull-request comment containing both the commit-specific preview URL and the `pr-<number>` rolling preview URL
- [x] 2.5 Restrict preview deploys to trusted pull-request contexts (same-repository PRs) if fork PRs cannot access Cloudflare secrets

## 3. Main-branch preview workflow

- [x] 3.1 Add `.github/workflows/docs-main-preview.yaml` triggered on `push` to `main`, running independently of code CI
- [x] 3.2 Run `make check-docs` then `make build-docs`, then deploy with `wrangler versions upload --preview-alias main`
- [x] 3.3 Verify `main` preview updates on push to `main` without changing production at `c9s.run`

## 4. Release production workflow

- [x] 4.1 Add a `deploy-docs` job to `.github/workflows/release.yaml` with `needs: [lint, test, images]`
- [x] 4.2 Run `make check-docs` then `make build-docs` in the release docs job before production deploy
- [x] 4.3 Deploy production with `wrangler deploy`, passing `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` from repository secrets
- [x] 4.4 Verify production docs deploy does not run when lint, test, or image build fails on release

## 5. Domain cutover and verification

- [ ] 5.1 Attach `c9s.run` as a custom domain on the `clabernetes` Worker in Cloudflare
- [ ] 5.2 Remove the existing `c9s.run` redirect to containerlab.dev
- [ ] 5.3 Validate pull-request preview: check-docs passes, dual-link PR comment appears, commit URL is commit-specific, `pr-<number>` URL rolls forward on new commits
- [ ] 5.4 Validate main preview: push to `main` updates `main` preview alias without touching `c9s.run`
- [ ] 5.5 Validate release production: successful release publishes to `c9s.run`; failed release build skips docs deploy

## 6. Spec sync

- [x] 6.1 Archive or sync delta specs from this change into `openspec/specs/documentation-hosting/spec.md` and update `openspec/specs/documentation-site/spec.md` when implementation is complete
