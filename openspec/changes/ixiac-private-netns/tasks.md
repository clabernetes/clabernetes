# Tasks

- [ ] Create change scope and validate cluster
  - [x] Validate the local KinD cluster (`kind-c9s-e2e`) and install the current checkout
  - [ ] Reproduce #372 on the Nebius test cluster and capture `openspec/changes/ixiac-private-netns/repro.log`
  - [x] Confirm the failure mode from the issue: `ixiacd` moves `c9s0`/`c9sr0`/`c9sm0`/`c9ss*` out of the shared Pod namespace

- [x] CRD/API plumbing
  - [x] Extend `apis/v1alpha1/nodeprofile.go:NodeProfileDeployment` + `apis/v1alpha1/topologyspec.go:Deployment` with optional `*bool` `networkIsolation`; regenerate deepcopy/OpenAPI/CRDs
  - [x] Thread the field through `compiler/{compile,render}.go` as an explicit passthrough only; omitted values stay unset in emitted profiles

- [x] Resolver/renderer plumbing
  - [x] Teach `controllers/node/profile.go` (`ResolvedProfile`) to carry `NetworkIsolation` through `applyProfileDeployment`, honoring explicit values and layering the ixia-c family default when unset
  - [x] In `internal/directpod/renderer.go`, render the `device-netns` volume, init container, sidecar mount/arg, and application mount/arg only when isolation is resolved
  - [x] Extend renderer tests with isolated vs shared profiles, including init-before-sidecar ordering

- [x] Netns lifecycle primitive
  - [x] New `internal/directruntime/devicenetns_linux.go` (+ non-Linux stub) exposing `EnsureDeviceNetns`, `openDeviceNetworkNamespace`, and `CleanupDeviceNetns`
  - [x] Create the bind mount in `device-netns-init` with bidirectional propagation; open it in the sidecar with `netlink.NewHandleAt`
  - [x] Clean up the file on graceful sidecar termination; use a dedicated hostPath because emptyDir teardown cannot unlink the submount

- [x] Cross-namespace pair placement
  - [x] Move the synthetic management device leg into the private namespace, address it there, and assert its default route there
  - [x] Move fabric and host Link device legs into the private namespace and raise them there
  - [x] Reject same-Pod and loopback Links with a clear error until their cross-namespace placement lands

- [x] Launch boundary crossing
  - [x] Extend `LaunchOperations` with `EnterDeviceNetns(path)` and implement the thread-locked `setns(CLONE_NEWNET)` in the Linux launch operations
  - [x] Call it before `LimitOpenFiles`/`Exec`; non-isolated pods keep the existing path

- [x] Conditions/events/docs
  - [x] Log the active isolation mode in the sidecar
  - [x] Record `DeviceNetworkIsolation=True` in the interposition conditions file
  - [x] Document the profile behavior, privilege requirement, and same-Pod Link limitation

- [ ] Verification
  - [x] `make lint`
  - [x] `make verify-generated` inspected (only intended generated diffs remain in the working tree)
  - [x] `go test ./internal/directpod/... ./internal/directruntime/... ./controllers/node/...`
  - [x] Real `ixia-c-one:1.61.0-9` e2e on local KinD: `TestIxiac` passes in ~52-63s, including netns inode, `c9s0` absence, management address, and `ConnectivityReady`
  - [x] Re-run `TestIxiac` on the Nebius test cluster (`npc-mk8s-c9s-beta-e0tavhzqdhqv0jeyvk`): PASS in 86.35s with the real `ixia-c-one:1.61.0-9` image
  - [x] Negative check: non-isolated `TestNodeLinkDirect` on the same cluster: PASS in 59.60s
