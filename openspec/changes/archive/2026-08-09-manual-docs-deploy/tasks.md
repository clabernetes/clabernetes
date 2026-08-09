## 1. Reusable production deploy workflow

- [x] 1.1 Create `.github/workflows/docs-deploy.yaml` with `workflow_call` inputs `ref` and `deploy-message`
- [x] 1.2 Add checkout (with LFS), pnpm 11.17.0, Node 22, `make check-docs`, `make build-docs`, and `cloudflare/wrangler-action@v4` deploy steps matching the current `release.yaml` `deploy-docs` job
- [x] 1.3 Set `permissions: contents: read` and ensure Cloudflare secrets are consumed via inherited secrets

## 2. Manual trigger workflow

- [x] 2.1 Create `.github/workflows/docs-deploy-main.yaml` with `on: workflow_dispatch`
- [x] 2.2 Add `concurrency: group: docs-production-deploy, cancel-in-progress: false`
- [x] 2.3 Call `docs-deploy.yaml` with `ref: main` and deploy message including `${{ github.sha }}`

## 3. Release workflow refactor

- [x] 3.1 Replace inline `deploy-docs` job in `release.yaml` with `uses: ./.github/workflows/docs-deploy.yaml`
- [x] 3.2 Change `deploy-docs` `needs` from `[lint, test, images]` to `[lint, test]`
- [x] 3.3 Pass `ref: ${{ github.event.release.tag_name }}` and release-tag deploy message to the reusable workflow
- [x] 3.4 Add `concurrency: group: docs-production-deploy, cancel-in-progress: false` to the release `deploy-docs` job caller

## 4. Verification

- [x] 4.1 Confirm workflow YAML is valid (no duplicate job names, correct `workflow_call` input references)
- [x] 4.2 Verify manual workflow appears in GitHub Actions UI with expected name
