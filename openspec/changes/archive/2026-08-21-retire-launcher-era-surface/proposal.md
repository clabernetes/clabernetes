# Proposal: Retire the launcher-era surface

## Why

The direct-runtime cut (`direct-node-pods`, `sidecar-mgmt-interposition`, `management-l2-mesh`)
removed the launcher runtime, the host-endpoint daemon, and the slurpeeth transport as
implementations, but left their surface behind: a CRD named after the deleted component, a
connectivity selector with exactly one realization, compatibility code for releases that must be
cleanly reinstalled anyway, and launcher-era vocabulary across identifiers, comments, charts,
dev tooling, and documentation. The release is already a breaking cut, so this is the only cheap
moment to finish the cut.

## What Changes

- **Rename `LauncherProfile` to `NodeProfile`** (kind, resource `nodeprofiles`,
  `Node.spec.profileRef`, `Node.status.appliedProfile`, condition `NodeProfileResolved`); the
  spec shape is unchanged. The `launcher-profiles` capability is renamed to `node-profiles`.
- **Remove the `connectivity` field** from Topology and Link specs along with the slurpeeth
  enum value, tunnel-range clamp, fabric Service TCP port, and plan vocabulary. Exactly one
  cross-Pod realization exists (in-Pod VXLAN).
- **Remove old-release compatibility code**: the `upgrade-preflight` CLI and package, the
  0.6.x legacy-object cleanup pass, the legacy PVC adoption fallback, the daemon-era Link
  finalizer stripping, `Node.status.probeStatuses`, and `Link.status.error` (superseded by the
  `Accepted` condition). The documented upgrade path is a full uninstall and reinstall.
- **Purge launcher-era leftovers** from the chart schema (`deviceRuntimeMode`,
  `launcherImage`), dev tooling (`.develop/`, `hack/c9s_install.py`), dead constants and
  helpers, identifier vocabulary (`ResolveLauncherNode` family becomes primary/pod terms),
  comments, and documentation (including the docs-site architecture diagram and the
  management-mesh documentation gap).

## Impact

- Affected specs: `node-profiles` (renamed from `launcher-profiles`), `link-lifecycle`,
  `topology-resource`, `node-lifecycle`, `direct-connectivity`; terminology across other specs.
- Affected code: `apis/`, generated CRDs/clients, controllers, clabverter, chart schema,
  `.develop/`, `hack/`, docs, docs-site.
- Breaking: yes — folded into the same 0.9 clean cutover already required by the API-group
  rename; documented in `docs/upgrading.md`.
