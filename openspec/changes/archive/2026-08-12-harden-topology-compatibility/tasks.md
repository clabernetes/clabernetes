## 1. Compiler compatibility contract

- [x] 1.1 Add warning and strict unsupported-field policies with typed diagnostics and stable
  ordering.
- [x] 1.2 Include source paths in compatibility warnings and preserve permissive omission for lossy
  fields.
- [x] 1.3 Make structurally impossible pseudo-nodes, endpoints, explicit link types, and network
  modes fatal, including explicit native `veth` and `host` link forms.
- [x] 1.4 Add compiler coverage for flattening, strict diagnostics, warning locations, impossible
  structures, and deterministic output.

## 2. Grouped launcher readiness

- [x] 2.1 Resolve every launcher-group member by its Docker node label, including stopped members,
  and reject ambiguous matches.
- [x] 2.2 Require every member's Docker lifecycle state and image healthcheck to pass before
  writing the shared healthy marker.
- [x] 2.3 Keep application TCP/SSH probes scoped to the primary node and add helper coverage for
  group membership and all-member readiness.

## 3. Reconciliation reliability

- [x] 3.1 Retry Node status writes after resource-version conflicts.
- [x] 3.2 Retry Topology aggregate status writes after resource-version conflicts.
- [x] 3.3 Verify retries read the latest server object before intentionally replacing the full
  controller-owned status snapshot.

## 4. Documentation

- [x] 4.1 Explain Topology compatibility warnings, fatal structural validation, and the fact that
  strict compilation is not currently exposed as a clabverter CLI flag.
- [x] 4.2 Explain grouped readiness, Docker healthchecks, process-level readiness limitations, and
  primary-only TCP/SSH checks in the architecture and LauncherProfile documentation.
- [x] 4.3 Add the behavior change to the current release notes and verify direct page loads and
  the production-like docs build.

## 5. Final verification

- [x] 5.1 Run the PR's focused unit tests, repository lint, and CI e2e/try-smoke checks.
- [x] 5.2 Confirm the branch diff is whitespace-clean and has no existing review comments.
- [x] 5.3 Explicitly accept controller-owned full-status replacement after testing a conflict that
  changes the stored object between retry attempts.
