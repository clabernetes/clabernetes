# c9s documentation site

This package contains the Fumadocs application. Documentation content remains in the repository
`docs/` directory and is loaded by the Vite development and static build workflows.

The package uses pnpm for dependency management.

From the repository root:

```bash
make serve-docs
```

The Make target installs dependencies from `pnpm-lock.yaml` before starting the development server.

Other workflows:

```bash
make docs-install
make check-docs
make build-docs
make preview-docs
```

The production artifact is written to `docs-site/build/client` and requires only a static file
server.

## Cloudflare deployment

Documentation is deployed to the Cloudflare Worker `clabernetes` using Wrangler configuration in
the repository root [`wrangler.toml`](../wrangler.toml). The Worker serves static assets from
`docs-site/build/client`.

### GitHub secrets

Workflows authenticate with Wrangler using these repository secrets:

| Secret | Purpose |
|--------|---------|
| `CLOUDFLARE_API_TOKEN` | API token with Workers Scripts Edit permission |
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account identifier |

Both secrets are configured in the `clabernetes` GitHub repository.

Workflows pass them to Wrangler through the environment expected by `cloudflare/wrangler-action`:

```yaml
apiToken: ${{ secrets.CLOUDFLARE_API_TOKEN }}
accountId: ${{ secrets.CLOUDFLARE_ACCOUNT_ID }}
```

### Deployment workflows

| Workflow | Trigger | Cloudflare target |
| ---------- | --------- | ------------------- |
| [`docs-preview.yaml`](../.github/workflows/docs-preview.yaml) | Pull request updates to docs paths | Commit preview URL + `pr-<number>` rolling alias |
| [`docs-main-preview.yaml`](../.github/workflows/docs-main-preview.yaml) | Push to `main` on docs paths | `main` rolling preview alias |
| [`release.yaml`](../.github/workflows/release.yaml) `deploy-docs` job | GitHub release, after lint/test/images | Production at `c9s.run` |

Preview URLs use the `*.workers.dev` pattern, for example:

- `pr-42-clabernetes.<account>.workers.dev`
- `main-clabernetes.<account>.workers.dev`

Pull-request previews run independently of the Go CI pipeline. Production documentation deploys
only after release lint, test, and image build jobs succeed.

### Production domain setup

Manual steps in the Cloudflare dashboard:

1. On the `clabernetes` Worker **Domains** tab, ensure **Preview URL** is enabled and set to Public.
2. Attach the custom domain `c9s.run`.
3. Remove the existing redirect from `c9s.run` to containerlab.dev.

Preview URLs follow the pattern `pr-42-clabernetes.<subdomain>.workers.dev` and are printed by
`wrangler versions upload` once preview URLs are enabled in the dashboard.

### Verification checklist

After merging the deployment workflows:

1. Open a pull request that changes `docs/` or `docs-site/` and confirm:
   - `make check-docs` runs before upload
   - the PR comment contains both commit and `pr-<number>` preview links
   - new commits update the same PR comment and refresh the `pr-<number>` alias
2. Merge to `main` and confirm the `main` preview alias updates without changing `c9s.run`.
3. Create a release and confirm production deploy runs only after image build succeeds and serves
   `c9s.run`.
