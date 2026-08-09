## ADDED Requirements

### Requirement: Manual production documentation workflow

The repository SHALL provide a GitHub Actions workflow triggered by `workflow_dispatch` that deploys production documentation from the default branch without creating a GitHub release.

#### Scenario: Maintainer triggers manual production deploy

- **WHEN** a maintainer runs the manual production documentation workflow from the GitHub Actions UI
- **THEN** the workflow checks out the default branch, validates documentation, builds the static site, and deploys to the production Cloudflare Worker serving `c9s.run`

#### Scenario: Manual deploy does not wait on code CI

- **WHEN** the manual production documentation workflow starts
- **THEN** documentation validation and production deployment proceed without declaring a dependency on the repository Go lint, test, or image build pipeline jobs

### Requirement: Manual deploy concurrency control

The manual production documentation workflow SHALL use a concurrency group that prevents overlapping production documentation deployments from cancelling an in-flight deploy.

#### Scenario: Queue concurrent manual production deploys

- **WHEN** a maintainer triggers the manual production documentation workflow while another production documentation deployment is already running
- **THEN** the newer workflow run waits until the in-flight deployment completes rather than cancelling it

### Requirement: Reusable production documentation deploy workflow

Production documentation deployment steps SHALL be defined in a reusable GitHub Actions workflow callable by both the release workflow and the manual production documentation workflow.

#### Scenario: Release workflow calls reusable deploy

- **WHEN** the release workflow publishes production documentation after a successful release validation
- **THEN** it invokes the reusable production documentation deploy workflow with the release tag as the checkout ref

#### Scenario: Manual workflow calls reusable deploy

- **WHEN** the manual production documentation workflow runs
- **THEN** it invokes the same reusable production documentation deploy workflow with the default branch as the checkout ref
