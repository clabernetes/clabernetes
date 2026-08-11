## Why

The project is rebranding to **c9s** (`c9s.run` docs, c9s controller), but Kubernetes custom resources still use the `clabernetes.containerlab.dev` API group. Aligning the CRD group with the c9s identity removes a confusing split between public branding and cluster API surface.

## What Changes

- **BREAKING**: Rename the Kubernetes API group from `clabernetes.containerlab.dev` to `c9s.run` for all six CRDs (Topology, Node, Link, LauncherProfile, ImageRequest, Config).
- Keep API version `v1alpha1` unchanged; manifests use `apiVersion: c9s.run/v1alpha1`.
- Regenerate CRDs, typed clients, OpenAPI, and UI SDK from updated API source constants.
- **BREAKING**: Requires full uninstall and reinstall — no in-place `helm upgrade` from the legacy API group.
- Provide `make uninstall` to remove the Helm release, all c9s CRDs (`*.c9s.run` and legacy `*.clabernetes.containerlab.dev`), and the namespace.
- Manager installs `c9s.run` CRDs only; it does **not** auto-delete legacy CRDs.
- Update Helm RBAC, documentation, examples, e2e fixtures, and clabverter output to reference `c9s.run`.

## Capabilities

### New Capabilities

- `crd-api-group`: Defines the canonical Kubernetes API group (`c9s.run`), version (`v1alpha1`), registered CRD kinds, and breaking-cutover behavior when upgrading from the legacy group.

### Modified Capabilities

_None — existing topology, node, link, and launcher-profile specs describe resource behavior, not API group identity._

## Impact

- `apis/doc.go`, `apis/v1alpha1/doc.go` (source of truth)
- Generated artifacts: `charts/clabernetes/crds/`, `assets/crd/`, `generated/`, `ui/clabernetes-openapi.json`, `ui/src/lib/clabernetes-client/`
- `manager/initcrds.go` (CRD apply only — no legacy cleanup)
- `Makefile` (`uninstall` target)
- `charts/clabernetes/templates/clusterrole.yaml` (RBAC apiGroups)
- `clabverter/emitcrs.go` (manifest API version constant)
- Documentation (`docs/`), examples (`examples/`), e2e golden fixtures
- **Out of scope**: Helm chart name, `appName`, UI ingress hostname (`ui.clabernetes.containerlab.dev`), Go module path
