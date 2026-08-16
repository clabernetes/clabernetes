## 1. API and profile resolution

- [x] 1.1 Add optional `Affinity *corev1.Affinity` to the shared `apis/v1alpha1.Scheduling` type.
- [x] 1.2 Add affinity to `node.ResolvedProfile` and deep-copy a referenced
  LauncherProfile's affinity during profile resolution.
- [x] 1.3 Render resolved affinity into launcher Pod templates and include it in Deployment
  conformance comparisons.

## 2. Topology projection

- [x] 2.1 Include affinity when Topology scheduling is copied into generated shared
  LauncherProfiles, including affinity-only scheduling blocks.
- [x] 2.2 Verify generated dedicated resource-policy profiles retain the shared Topology affinity and
  direct CR manifest generation uses the same projection.

## 3. Unit tests

- [x] 3.1 Add profile-resolution tests for configured, omitted, explicitly empty, and deep-copied
  affinity values.
- [x] 3.2 Add launcher Deployment rendering tests that compare node affinity, pod affinity, and pod
  anti-affinity sections against expected YAML/JSON fixture data.
- [x] 3.3 Add Deployment conformance tests covering affinity addition, change, and removal.
- [x] 3.4 Add Topology rendering tests for affinity-only scheduling and affinity retention on shared
  and dedicated generated LauncherProfiles.
- [x] 3.5 Add or update direct CR-manifest tests/fixtures if needed to prove launcher affinity is
  preserved without end-to-end execution.

## 4. Documentation and generated artifacts

- [x] 4.1 Add an "Affinity Rules" section to `docs/guides/resource-management.md` explaining both
  `Topology.spec.deployment.scheduling.affinity` and `LauncherProfile.spec.scheduling.affinity`;
  update `docs/concepts/launcher-profiles.md` and `examples/deployment/with-scheduling.yaml` with
  matching launcher-scoped examples.
- [x] 4.2 Regenerate deepcopy, CRD, and OpenAPI artifacts from the API source; do not hand-edit
  generated files.
- [x] 4.3 Run focused Go unit tests, `make verify-generated`, and relevant documentation checks;
  record that e2e tests are intentionally not required for this change.
