## 1. API Source Constants

- [x] 1.1 Change `apis/doc.go` `Group` constant from `clabernetes.containerlab.dev` to `c9s.run`
- [x] 1.2 Change `apis/v1alpha1/doc.go` `+groupName` marker to `c9s.run`

## 2. Generated Artifacts

- [x] 2.1 Run `make run-generate` to regenerate CRDs, deepcopy, clientset, OpenAPI, and UI SDK types
- [x] 2.2 Remove stale `clabernetes.containerlab.dev_*.yaml` CRD files from `charts/clabernetes/crds/` and `assets/crd/`
- [x] 2.3 Run `make verify-generated` and confirm no diff

## 3. Uninstall Target

- [x] 3.1 Remove `removeLegacyCrds` from `manager/initcrds.go` (manager applies CRDs only)
- [x] 3.2 Add `make uninstall` target to uninstall Helm release, delete CRDs, and remove namespace
- [x] 3.3 Document uninstall-and-reinstall flow in `docs/upgrading.md`

## 4. Helm and RBAC

- [x] 4.1 Update `charts/clabernetes/templates/clusterrole.yaml` apiGroups from `clabernetes.containerlab.dev` to `c9s.run`
- [x] 4.2 Regenerate Helm chart golden test fixtures

## 5. Clabverter and Hardcoded References

- [x] 5.1 Update `manifestAPIVersion` constant in `clabverter/emitcrs.go` to `c9s.run/v1alpha1`
- [x] 5.2 Update clabverter golden fixtures and tests

## 6. Documentation and Examples

- [x] 6.1 Update all `apiVersion` references in `docs/` to `c9s.run/v1alpha1`
- [x] 6.2 Update all `apiVersion` references in `examples/` to `c9s.run/v1alpha1`
- [x] 6.3 Update `kubectl get` examples (e.g. `nodes.c9s.run`) in docs

## 7. E2E and Test Fixtures

- [x] 7.1 Update e2e apply manifests and golden files to use `c9s.run/v1alpha1`
- [x] 7.2 Rename golden files containing `clabernetes.containerlab.dev` in filenames to `c9s.run`
- [x] 7.3 Update remaining Go test hardcoded API version strings (e.g. `launcher/connectivity/watch_test.go`)

## 8. Validation

- [x] 8.1 Run unit tests (`make test` or equivalent)
- [x] 8.2 Run e2e tests to confirm controllers reconcile under the new group
- [x] 8.3 Add breaking-change note to release documentation
