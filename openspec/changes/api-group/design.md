## Context

All six c9s CRDs currently register under `clabernetes.containerlab.dev/v1alpha1`. The API group is defined in two source-of-truth locations:

- `apis/doc.go` — `Group` constant consumed by Go types and `SchemeGroupVersion`
- `apis/v1alpha1/doc.go` — `+groupName` kubebuilder marker for controller-gen

Running `make run-generate` cascades from these constants into CRD YAML (`charts/clabernetes/crds/`, `assets/crd/`), typed clients (`generated/clientset/`), OpenAPI (`generated/openapi/`, `ui/clabernetes-openapi.json`), and UI SDK types.

The project is rebranding to c9s (`c9s.run` documentation, c9s controller in Helm values). The Kubernetes API group still references the old clabernetes/containerlab identity.

The manager already implements legacy CRD cleanup in `manager/initcrds.go` — it deleted `connectivities` and `nodeprofiles` CRDs when their replacements landed. This change extends that pattern to remove the entire legacy API group.

## Goals / Non-Goals

**Goals:**

- Rename API group to `c9s.run`, keeping version `v1alpha1`
- Regenerate all API artifacts from updated source constants
- Hard cutover: delete legacy `clabernetes.containerlab.dev` CRDs on upgrade
- Update RBAC, docs, examples, e2e fixtures, and clabverter output

**Non-Goals:**

- Dual-serve or conversion webhook between old and new groups
- Automated migration of existing CR instances (users re-apply manifests)
- Helm chart rename, `appName` change, or UI ingress hostname change
- Go module path rename (`github.com/clabernetes/clabernetes`)

## Decisions

### 1. Hard cutover over dual-serve

**Decision:** Delete the entire legacy API group on manager startup; no coexistence period.

**Rationale:** The API is `v1alpha1` and the project has previously removed legacy CRDs without conversion. A dual-serve period would require registering both groups in the scheme, doubling informers and RBAC, with no conversion webhook infrastructure today.

**Alternative considered:** Serve both groups temporarily with a migration tool — rejected as unnecessary complexity for alpha.

### 2. Source-of-truth change + regen cascade

**Decision:** Change `apis/doc.go` and `apis/v1alpha1/doc.go`, then run `make run-generate`.

**Rationale:** Controllers already use `SchemeGroupVersion` from Go types — no controller logic changes needed. CRD filenames will change from `clabernetes.containerlab.dev_*.yaml` to `c9s.run_*.yaml` via controller-gen.

### 3. Extend existing `removeLegacyCrds` pattern

**Decision:** Add all six legacy CRD names plus prior stragglers (`connectivities`, `nodeprofiles`) to the legacy cleanup list. Gate deletion on the corresponding `c9s.run` CRD being present.

**Rationale:** Reuses proven safety gate from the `nodeprofiles` → `launcherprofiles` migration. Manager-owned cleanup handles runtime deletion regardless of Helm CRD install behavior.

### 4. API group only — no broader rebrand

**Decision:** Scope limited to Kubernetes API identity. Chart name, deployment labels, and UI ingress host remain unchanged.

**Rationale:** Minimizes blast radius. UI ingress (`ui.clabernetes.containerlab.dev`) is a separate DNS/ingress concern.

## Risks / Trade-offs

| Risk | Mitigation |
| ------ | ------------ |
| All existing CR instances deleted on upgrade | Document breaking change in release notes; users export and re-apply manifests |
| UI/API mismatch if components ship out of sync | Ship manager, CRDs, and UI in a single release |
| Stale user GitOps repos referencing old group | Release notes + updated docs/examples |
| E2e golden file renames break diff review | Mechanical rename; run e2e to validate |

## Migration Plan

1. User exports existing manifests (`kubectl get ... -o yaml`)
2. User updates `apiVersion` from `clabernetes.containerlab.dev/v1alpha1` to `c9s.run/v1alpha1`
3. User upgrades c9s (Helm or manager image)
4. Manager applies new `c9s.run` CRDs, then deletes legacy CRDs (and their instances)
5. User re-applies updated manifests

**Rollback:** Downgrading to a pre-change release restores the old API group but does not recover deleted CR instances. Users must retain source manifests before upgrading.

## Open Questions

_None — breaking cutover approach confirmed._
