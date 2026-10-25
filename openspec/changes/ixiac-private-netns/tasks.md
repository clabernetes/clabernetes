# Tasks

- [ ] Create change scope and validate cluster
  - [ ] `kubectl get --raw /readyz` on current context + confirm no existing `ixia-probe` namespace; document baseline
  - [ ] Reproduce #372 locally (single Node `keysight_ixia-c-one`) and capture failing logs as `openspec/changes/ixiac-private-netns/repro.log`

- [ ] CRD/API plumbing
  - [ ] Extend `apis/v1alpha1/nodeprofile.go:NodeProfileDeployment` + `apis/v1alpha1/topologyspec.go:Deployment` with optional enum `networkIsolation` (`Disabled`/`Enabled`, default `Disabled`); emit deepcopy/OpenAPI/CRDs via `make generate && make manifests`; regen docs-site (`make docs`)
  - [ ] Thread field through `compiler/{compile,render}.go` — bucket topologies by deployment policy including isolation so ixia-c nodes land in their own dedicated NodeProfile; add ixia-c → `Enabled` default when topology leaves it unset (Topology path)

- [ ] Resolver/renderer plumbing
  - [ ] Teach `controllers/node/profile.go` (`ResolvedProfile`) to carry `NetworkIsolation` through `applyProfileDeployment`, honoring explicit values and layering the ixia-c implicit default when the profile field is absent/empty (standalone Node path)
  - [ ] In `internal/directpod/renderer.go`, gate `device-netns` emptyDir volume + mounts to when `resolvedProfile.networkIsolation == Enabled`; thread pod UID/name into sidecar args and launch-ops env
  - [ ] Extend renderer + compiler render tests (normalize fixtures with isolated vs shared profiles; assert volume/mounts absent for non-isolated kinds)

- [ ] Netns lifecycle primitive
  - [ ] New file `internal/directruntime/devicenetns_linux.go` (+ *_linux_test.go) exposing `EnsureDeviceNetns(stateDir, podUID) (path string, cleanup func(), error)` using `vishvananda/netns` + bind-mount; include non-Linux stub
  - [ ] Wire lifecycle into `connectivity.go` startup before `EnsureInterposition` — ensure idempotency across sidecar restarts

- [ ] Cross-namespace pair placement
  - [ ] Refactor `EnsureFabricEndpoint` + `EnsureInterposition` to `LinkSetNsFd` the device-side leg into dev ns; reuse host-link pattern `fabric_linux.go:575`
  - [ ] Adjust `links_linux.go` `EnsureVethPair`/lookup to tolerate absent-in-Pod device leg when owner is isolated; add reconciler tests exercising the absence branch

- [ ] Launch boundary crossing
  - [ ] Extend `LaunchOperations` with `EnterDeviceNetns(path string) error` and implement via thread-locked `setns(CLONE_NEWNET)` mirroring `endpoint_namespace_linux.go:115-143`
  - [ ] Call before `Exec` in `RunLaunchWithOperations`; verify non-isolated pods still pass existing `launch_test.go`

- [ ] Conditions/events/docs
  - [ ] Surface isolation in conditions/events; extend interposition condition scenario wording under `direct-connectivity`
  - [ ] Minimal architecture note in `docs/` or repo comment where network sharing is documented

- [ ] Verification
  - [ ] `make lint` + targeted `go test ./internal/directpod/... ./internal/directruntime/...`
  - [ ] Re-apply ixia-c Node, assert `NodeReady + ConnectivityReady True`, no `CrashLoopBackOff` across one resync tick window (capture logs)
  - [ ] Negative check: a `nokia_srlinux` single-node lab on same cluster unchanged (ensure no accidental global enablement)
