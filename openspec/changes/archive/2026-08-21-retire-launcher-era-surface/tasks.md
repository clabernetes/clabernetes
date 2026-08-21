# Tasks: Retire the launcher-era surface

## 1. Old-release compatibility removal

- [x] 1.1 Delete `internal/upgradepreflight` and the `upgrade-preflight` CLI command with tests
- [x] 1.2 Delete the 0.6.x legacy-object cleanup pass from the topology controller
- [x] 1.3 Delete the legacy PVC adoption fallback from the node controller
- [x] 1.4 Delete daemon-era Link finalizer stripping and the finalizer constant
- [x] 1.5 Remove `Node.status.probeStatuses` and the `NodeProbeStatus*` types
- [x] 1.6 Remove `Link.status.error`; repoint the plan-input gate at the `Accepted` condition

## 2. Connectivity selector removal

- [x] 2.1 Remove `Topology.spec.connectivity` and `Link.spec.connectivity` with the slurpeeth enum
- [x] 2.2 Remove the slurpeeth VNI clamp, fabric Service TCP port, and plan vocabulary
- [x] 2.3 Update clabverter, tests, goldens, examples, and the compatibility baseline

## 3. NodeProfile rename

- [x] 3.1 Rename the CRD, API types, reference/status fields, and condition
- [x] 3.2 Regenerate deepcopy, CRDs, OpenAPI, and clients; update chart and docs-site views
- [x] 3.3 Rename internal group-primary identifiers to primary/pod vocabulary

## 4. Leftover purge

- [x] 4.1 Remove `deviceRuntimeMode` and `launcherImage` from the chart values schema
- [x] 4.2 Purge launcher builds from `.develop/`, `hack/c9s_install.py`, and `hack/migrate-ghcr.sh`
- [x] 4.3 Delete dead constants, helpers, and launcher-era comments across the tree
- [x] 4.4 Update documentation: daemon-era wiring prose, the docs-site architecture diagram,
      management-mesh documentation, upgrade guide, release notes, and examples

## 5. Validation

- [x] 5.1 `make test`, `make lint`, `make verify-generated`, `make check-docs`
- [x] 5.2 Cluster validation on kind (direct e2e suite)
