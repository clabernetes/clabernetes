# Task 11.2–11.5 evidence: nested-runtime removal

Date: 2026-08-20. Cluster: kind `c9s-direct-links` (two workers), managers `fabric-35` (DNS completion) and `fabric-36` (full removal build).

## What was removed

- `launcher/` package (5,726 lines), the `launch` CLI command, and the nested Node-controller
  path: `reconcileSecondary`, `reconcileLauncher`, `reconcileDeployment`,
  `reconcileFabricService`, `reconcileExposeService`, `resolveGroupExposedPorts`,
  `DeploymentReconciler`, `controllers/node/{deployment,digest,status}.go`, and the legacy
  launcher namespace identity (`NamespaceResourcesReconciler.Reconcile`, launcher
  ServiceAccount/RoleBinding names). `Reconcile` is direct-only and fail-closed.
- The temporary mode switch end to end: `internal/deviceruntime.Mode/ParseMode` (package now
  holds only `ErrDirectRuntimeUnavailable`), `manager.GetDeviceRuntimeMode`, the
  `NewReconciler` mode parameter, `DEVICE_RUNTIME_MODE` constant/env, chart value
  `manager.deviceRuntimeMode`, and the e2e gate
  (`testhelper.SkipUnlessDeviceRuntimeMode`/`C9S_E2E_DEVICE_RUNTIME_MODE`).
- Launcher build/release surface: `build/launcher{,.Dockerfile}`, `make build-launcher`,
  `LAUNCHER_IMAGE` variables and env, launcher entries in `.mk/e2e.mk`,
  `.github/workflows/images.yaml` matrix + cross-image cache scopes, and
  `publish-dev.yaml` image publication/verification. Chart: `LAUNCHER_IMAGE` env,
  `manager.launcherImage`, the launcher ClusterRole, and the OpenShift privileged-SCC subject
  now targets the direct-runtime ServiceAccount.
- Dead config plumbing: `GetLauncherImage{,PullPolicy}`, `GetPrivilegedLauncher`,
  `GetContainerlab{Debug,Timeout,Version}`, `GetLauncherLogLevel`, `GetExtraEnv` (interface,
  manager, fake), the matching `ResolvedProfile` fields, 25 launcher-era env constants, and
  `go mod tidy` fallout (slurpeeth). `deadcode ./cmd/clabernetes` candidates were verified
  one-by-one: test-consumed exports stay; `plannerPodName` and `zeroHardwareAddress` deleted.

## E2E suites converted to direct

- `e2e/topology/basic`: golden compile assertions kept (Topology → Node/Link/LauncherProfile,
  expose + fabric Services), nested Deployment goldens deleted; `NormalizeNode` now strips
  run-specific `status.directContainers/directManagement/planDigest`. The SR Linux
  DNS-from-management test execs the device container directly
  (`ip netns exec srbase-mgmt ping <pod-dns-name>`).
- `e2e/clabverter`: direct goldens (Service targetPorts are destination ports, no launcher
  publication range). Passed in 47s.

## Always-provided DNS (found by the converted DNS test, fixed generically)

Upstream containerlab always fills every node's DNS from the host resolv.conf
(`CLab.extractDNSServers` in `core/clab.go`); c9s deliberately avoids `core.NewContainerLab`,
so no node ever received resolver identity and SR Linux rendered no `server-list`, leaving
`/etc/netns/srbase-mgmt/resolv.conf` empty (name resolution dead in the mgmt VRF). Fix, fully
kind-opaque: `directruntime.RuntimePodDNSServers` reuses the imported
`clabutils.ExtractDNSServersFromResolvConf` against the Pod filesystem; `Adapter.PodDNSServers`
carries it across every lifecycle boundary; `completeRuntimeManagement` fills entries whose
allocation declared no resolver; `applyManagementDNS` merges with upstream precedence
(topology DNS wins, `container:` network-mode members skipped, shared definition never
mutated). Proof: `e2e/topology/basic` `TestSRLinuxDNSFromManagementNamespace` — SR Linux
pings its peer by pod DNS name from the mgmt VRF. Validation note: a first pass appeared green
while `DEVICE_RUNTIME_IMAGE` still pinned the pre-fix image — the ping had raced SR Linux's
creation of its (empty) `/etc/netns/srbase-mgmt/resolv.conf`, which masks the Pod resolver once
present. With the preparation image actually carrying the completion, the rendered config
carries the resolver `server-list` and steady-state resolution holds.

## Negative verification (11.5)

- `internal/directpod/nested_removal_test.go`: rendered direct workloads reference no launcher
  image, no `DEVICE_RUNTIME_MODE`/`LAUNCHER_IMAGE` env, and no container-runtime socket mounts.
- `cmd/clabernetes/cli/nested_removal_test.go`: the shipped binary registers no `launch`
  command.

## Test-harness fix surfaced by the conversion

`testhelper.YQCommand` interpolated the object YAML into `bash -c "echo '<yaml>' | yq"`. A
transient direct-planning condition whose message contains quoted diagnostics (for example
`'device planning MissingInput at links...'`) broke the shell quoting and failed the golden
assertion immediately instead of letting `eventually` retry past the transient. The helper now
feeds yq over stdin, so document content can never break the normalization pipeline. The
unmasked run then exposed a second instability: host-terminated Links annotate the worker node
and host-endpoint daemon Pod UID, which follow scheduling — `NormalizeLink` now strips both
annotations so goldens carry only stable identity.

## Verification run

- `go build ./...`, `go vet ./...`, full unit suite (`go test $(go list ./... | grep -v e2e)`) green.
- `make verify-generated` green; chart goldens regenerated and inspected (diffs are exactly the
  removals); `helm lint` clean.
- Live on `fabric-35`: clabverter 47s, basic (incl. DNS) 94s, direct 385s (first-boot
  contention from four simultaneous suites; see below), SR-SIM 122s with zero restarts.
- Live on `fabric-36` (mode plumbing fully removed, `DEVICE_RUNTIME_MODE` env absent): direct
  70s clean, basic incl. the strengthened two-success DNS assertion green.
- Operational notes: `kubectl set image` alone leaves `DEVICE_RUNTIME_IMAGE` pointing at the
  old runtime — prepare/lifecycle keep executing the previous binary; always update the env
  with the image. Launching four suites immediately after an image rollout produced
  first-attempt Init/PostStart crashloops that recovered and do not reproduce in isolated
  runs. Remaining deferred: compatibility baseline `nested-only` rows are 12.2 scope.
