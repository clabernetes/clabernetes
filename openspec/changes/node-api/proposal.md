## Why

`NodeSpec` mirrors 40 fields of an old containerlab `NodeDefinition`, but only 9 of them are read by clabernetes and the rest are rendered verbatim into a `topo.clab.yaml` that describes **one** node in **one** launcher pod. That mismatch produces three concrete problems:

- Five fields no longer exist in containerlab at all (`publish`, `sandbox`, `kernel`, `wait-for`, top-level `SANs`). Containerlab parses topologies strictly, so setting any of them yields a schema-valid Node whose launcher fails at `clab deploy` with `field publish not found in type NodeDefinition`.
- Roughly a dozen fields are inert (cross-node semantics in a single-node lab) or restate policy that `LauncherProfile` already owns, so the API offers two competing places to set the same thing.
- Escape hatches users genuinely need (`devices`, `cap-add`, `shm-size`, `privileged`) are missing, while `spec.certificate` is missing `sans` — making SANs unreachable, because the only field that mentions them is one of the five that break the lab.

`+kubebuilder:pruning:PreserveUnknownFields` on `NodeSpec` makes all of this silent: it defeats kubectl strict field validation, and unknown keys never reach the rendered topology anyway because the launcher marshals the typed struct.

## What Changes

- **BREAKING**: Remove 17 fields from the Node containerlab vocabulary: `publish`, `sandbox`, `kernel`, `wait-for`, `SANs`, `runtime`, `cpu`, `cpu-set`, `memory`, `group`, `position`, `startup-delay`, `auto-remove`, `image-pull-policy`, `labels`, `aliases`, `healthcheck`; plus `extras.mysocket-proxy`.
- **BREAKING**: Drop `PreserveUnknownFields` from `NodeSpec` so removed and unknown keys are rejected at admission instead of silently ignored. The `config.vars` arbitrary-JSON preservation stays.
- Add the launcher-realizable container escape hatches: `devices`, `cap-add`, `shm-size`, `privileged`, `tmpfs`, `security-opts`, and `suppress-startup-config`.
- Align stale shapes with containerlab: `certificate` gains `key-size`, `validity-duration`, `sans` and `issue` becomes tri-state; `enforce-startup-config` becomes tri-state.
- **BREAKING**: Narrow `ports` to `<port>[/protocol]`. The left side of a docker-style mapping is a pod-internal allocation that clabernetes assigns itself, so pinning it cannot help and can collide. Two-sided entries are rejected on Nodes and normalized to their destination port by the Topology compiler.
- Fix the port parser, which today reads `1.2.3.4:80:80` as pod port 4, silently collapses ranges, and downgrades unknown protocols to TCP — an unanchored regex matched leftmost, with no unit test.
- Restrict `network-mode` to `container:<primary>`, the only value clabernetes can realize (it is the grouping declaration).
- Bump the launcher's containerlab from `0.74.3` to `0.78.0`, which is what makes `privileged`, `tmpfs`, and `security-opts` available.
- Add a conformance test asserting the Node vocabulary is a subset of the pinned containerlab's node definition — the check that would have caught all five stale fields.
- Keep the Topology `definition:` block permissive, but stop it being silent: it is native containerlab text, so vocabulary clabernetes does not implement is dropped with a warning naming the field and its line, rather than rejected. A malformed definition, or a recognized field holding an unusable value, still fails — as does one with no `topology:` section, which currently panics the reconcile.
- Map `labels` onto Kubernetes rather than dropping it: there is still no `spec.labels` on a Node, but node labels in a Topology `definition:` are inherited like `env`, copied to the emitted Node's `metadata.labels`, and propagated to the launcher Deployment and its pods — so they are selectable with kubectl, which docker labels on a node container never were. Labels Kubernetes would reject, labels in the `clabernetes/` namespace, and controller-owned identity/selector keys are omitted with a warning.

Net effect: 40 fields become 30, every one of which the launcher's containerlab can parse. A native containerlab topology still compiles, and now says what it dropped.

## Capabilities

### Modified Capabilities

- `node-lifecycle`: Node spec is a curated subset of containerlab node vocabulary rather than a verbatim mirror; unsupported and unknown fields are rejected; grouping is declared only by `network-mode: container:<primary>`; the vocabulary is constrained to what the launcher's containerlab version can parse.
- `topology-resource`: a source definition still accepts native containerlab vocabulary, with unimplemented fields omitted and warned rather than rejected, while malformed definitions and recognized fields holding unusable values still fail compilation; containerlab node labels become Kubernetes labels on the emitted Node and on its launcher deployment and pods.

### New Capabilities

_None._

## Impact

- `apis/v1alpha1/containerlab.go` (vocabulary), `apis/v1alpha1/node.go` (schema strictness, `network-mode` validation)
- Regenerated artifacts: `charts/clabernetes/crds/`, `assets/crd/`, `generated/`, `ui/` client
- `controllers/topology/compilecontainerlab.go` (definition-only `labels` joins the overlay merge set; invalid and controller-owned labels are dropped; two-sided ports normalized)
- `util/containerlab/ports.go` (anchored destination-only parser; the regex and two-sided branch go away) and `controllers/node/expose.go` (user-pin handling becomes unreachable)
- `build/launcher.Dockerfile` (`CONTAINERLAB_VERSION`)
- Tests and fixtures using removed fields or two-sided ports: `util/containerlab/topology_test.go`, `clabverter/test-fixtures/golden/**/topo01.yaml`, `controllers/topology/compile_test.go`
- Documentation and examples: unsupported-field guidance, the containerlab version floor for `launcherProfileRef` overrides, and the port form in `docs/guides/expose-configuration.md`, `examples/expose/`, `examples/advanced/README.md`
- `util/containerlab/topology.go` (unknown fields become warnings instead of silence), with the warnings logged by `controllers/topology/compilecontainerlab.go` and `clabverter/clabverter.go`
- **Out of scope**: `credentials`, `link-apply-mode`, `restart-policy`, `pid-mode`, `cgroupns-mode`; strict _rejection_ in the Topology `definition:` block (deliberately not adopted); surfacing compiler warnings on Topology status; any change to `LauncherProfile`
- **Excluded by design**: `stages` — stage ordering gates the nodes of one lab against each other, which assumes the whole lab on one host
