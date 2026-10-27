# Proposal: private device netns for greedy interfaces (ixia-c)

## Why

`keysight_ixia-c-one`'s entrypoint `ixiacd` enumerates every interface in its
network namespace and moves ("pushes") each one into a sibling traffic-engine
container it creates. Its filter skips only `lo`, `eth0`, `docker0` and
Docker's own `veth*` peers; everything else is claimed within seconds of
appearing — see #372.

The direct runtime shares one Pod network namespace between the sidecar
(`clabwire`) and all device containers. Interposition relies on that same
namespace to hold the sidecar-owned infra:

- `c9s0` — renamed CNI transport,
- `c9sr0`/`c9sm0` — interposition router leg + mesh VTEP,
- `c9ss*` — sidecar leg of every cross-Pod fabric Link.

None of these can be hidden from `ixiacd`: renaming, enslaving to a bridge,
and other naming tricks were tested in #372 and ignored. Once `c9s0` is moved
the Pod loses its address, `linkByAddress` fails, and `clabwire` crash-loops
with `cannot assign requested address` / `no interface carries the Pod
address`. Data-plane ports would hit the same fate as `c9ss*`.

Existing compensations (restoring a flushed Pod address on `c9s0` at
`interposition_linux.go:355-378`, tolerating a moved device leg at
`ensureFabricPair`/`deviceLegLink`) recover only *address flushes* and
device-leg renames — not a sidecar-owned link leaving the namespace entirely.
A move also flushes addresses with no sidecar-side signal to repair it.

We need a device whose *view* of the namespace contains only its own ports.

## What Changes

- **Explicit profile setting for isolation.** Add a generic knob
  `NodeProfile.spec.deployment.networkIsolation` (optional `*bool`) as reusable
  realization policy. Any Node referencing the profile inherits the behavior;
  omitted leaves the field unset so resolution can apply the kind default.
  Topology maps `spec.deployment.networkIsolation` through the compiler into
  emitted profiles unchanged.

- **Automatic assignment for ixia-c.** At profile-resolution time the controller
  inspects the same `kind` string containerlab already owns
  (`keysight_ixia-c-one` + alias `keysight_ixia-c`) and resolves
  `networkIsolation: true` when the field is unset. Explicit profile values
  always win. This keeps the public API generic — justification for the
  per-kind default living outside `device-planning` is in `design.md` — while
  making ixia-c work without opt-in. Compilation copies only explicit values
  into emitted profiles; it does not synthesize the default.

- **Lifecycle plumbing for the private netns.** A dedicated hostPath-backed
  bind-mount (`/var/run/netns/<pod-uid>.ns`) is created by the
  `device-netns-init` init container with bidirectional propagation and opened
  by the sidecar through a netlink handle bound to that namespace. The
  application launch boundary (`node-runtime launch --deviceNetns`) enters the
  namespace via `setns(CLONE_NEWNET)` immediately before `unix.Exec(argv)`.
  The sidecar detaches and removes its file on graceful termination. A hostPath
  is used instead of an emptyDir because kubelet cannot unlink the bind-mounted
  file during emptyDir teardown.

- **Cross-namespace veth pairs.** Synthetic management and fabric veth pairs
  continue to be created in the Pod namespace; the device-side end
  (`spec.DeviceInterface`, `spec.InterfaceName`/fabric) is then
  `netlink.LinkSetNsFd(deviceLeg, devNsFd)`-moved into the device netns and
  addressed/raised there. Host Links move the worker end to the worker
  namespace and the device end into the private namespace.

- **Reconciler awareness.** `EnsureInterposition`, `EnsureFabricEndpoint`,
  `EnsureHostInterface`, and `SweepTransportState` tolerate the device leg
  living in another namespace. Same-Pod and loopback Links are rejected with a
  clear error until their cross-namespace placement is implemented.

- **Smallest-first scope.** Mgmt + data-plane fidelity for ixia-c on direct
  runtime is the target. Same-Pod/loopback Link placement and same-pod
  secondaries joining the isolated netns are explicitly deferred as non-blocking
  until proven needed — see Out of Scope.

## Capabilities

### New Capabilities

None — ixia-c gains direct-runtime parity via an explicit profile capability,
but no new public capability beyond that is introduced until more than one
consumer proves the shape.

### Modified Capabilities

- `node-profiles`: `NodeProfile` gains `spec.deployment.networkIsolation` as a
  generic realization policy controlling whether the Node's device containers
  run in a sidecar-created private netns. Topology maps the same field and
  resolves isolation to `true` for ixia-c kinds when unset.
- `topology-resource`: topology compilation copies an explicit deployment
  policy value into emitted NodeProfiles and leaves omitted values unset.
- `direct-connectivity`: sidecar transport/VTEP state stays Pod-scoped; data +
  mgmt legs live behind `c9s0` in a second netns when the resolved profile
  enables isolation, discovered from the resolved profile.
- `direct-device-runtime`: application container startup crosses a namespace
  boundary when enabled; probes/exec still target the device container, not the
  infra netns.

## Impact

- `apis/v1alpha1/{nodeprofile,topologyspec}.go` + generated deepcopy/OpenAPI/CRDs
  (`make generate` / `make manifests`) — new deployment profile field; docs-site reference regen (`make docs`).
- `compiler/{compile,render}.go` — map an explicit topology deployment policy value into compiler input and emitted profiles; no default synthesis or profile grouping key change.
- `controllers/node/profile.go` + `internal/directpod/renderer.go` — resolve `networkIsolation` (including the ixia-c family default), render the dedicated `device-netns` hostPath/init container/mounts only when enabled, and thread the namespace path into the sidecar and application launch commands.
- `internal/directruntime/{launch,connectivity,interposition,fabric,links}_linux.go`
  + new `devicenetns_linux.go` — netns lifecycle, namespace-bound netlink handle, cross-namespace management/fabric/host leg placement, and launch `setns`.
- `cmd/clabernetes/cli/entrypoint.go` — `device-netns` init command and `--deviceNetns` wiring on the launch and connectivity boundaries.
- Docs: node-profile behavior, privilege requirement, and the same-Pod Link limitation.

## Open Questions

None blocking — deferred items tracked under Risks.
