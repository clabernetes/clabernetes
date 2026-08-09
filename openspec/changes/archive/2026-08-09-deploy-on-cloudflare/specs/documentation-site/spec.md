## ADDED Requirements

### Requirement: Hosted documentation deployment integration

The documentation application SHALL integrate with the repository's hosted deployment workflows by producing build output at a stable path consumed by Cloudflare Workers Static Assets deployment without additional transformation.

#### Scenario: Build output matches deployment configuration

- **WHEN** the production documentation build completes successfully
- **THEN** the emitted static files are written to the directory referenced by the Cloudflare deployment configuration

### Requirement: Deployment documentation

The repository SHALL document how maintainers configure Cloudflare and GitHub secrets, attach the `c9s.run` custom domain, and run preview and production documentation deployments.

#### Scenario: Maintainer setup instructions

- **WHEN** a maintainer follows the repository documentation for hosted documentation deployment
- **THEN** they can identify the `clabernetes` Worker, the `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` secrets used by workflows, domain steps, and the workflows responsible for preview and production deploys
