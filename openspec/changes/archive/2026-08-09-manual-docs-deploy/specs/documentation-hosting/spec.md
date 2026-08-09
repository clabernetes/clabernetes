## MODIFIED Requirements

### Requirement: Release-gated production deployment

Production documentation SHALL deploy to Cloudflare from the release workflow after the release code lint and test jobs succeed, or from the manual production documentation workflow. Production documentation SHALL be served at `c9s.run`. The release documentation deploy job SHALL NOT declare a dependency on the release image build job.

#### Scenario: Deploy on successful release validation

- **WHEN** a GitHub release is created, the release lint and test jobs succeed, and the release documentation deploy job succeeds
- **THEN** the prerendered documentation build from the release tag is published to the production Cloudflare Worker serving `c9s.run`

#### Scenario: Skip production deploy when release validation fails

- **WHEN** a GitHub release is created but a required release lint or test job fails
- **THEN** the release production documentation deploy job does not publish to `c9s.run`

#### Scenario: Release docs deploy does not wait on image build

- **WHEN** a GitHub release is created and the release lint and test jobs succeed while the image build job is still running or has failed
- **THEN** the release documentation deploy job may still run and publish to `c9s.run` once its own validation and build steps succeed

#### Scenario: Deploy from manual workflow

- **WHEN** a maintainer triggers the manual production documentation workflow and the deploy job succeeds
- **THEN** the prerendered documentation build from the default branch is published to the production Cloudflare Worker serving `c9s.run` without creating a GitHub release
