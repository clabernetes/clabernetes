## 1. Trim the Node vocabulary

- [x] 1.1 Remove from the Node API surface in `NodeDefinition` in `apis/v1alpha1/containerlab.go`: `Publish`, `Sandbox`, `Kernel`, `WaitFor`, `SANs`, `Runtime`, `CPU`, `CPUSet`, `Memory`, `Group`, `Position`, `StartupDelay`, `AutoRemove`, `ImagePullPolicy`, `Labels`, `Aliases`, `Healthcheck`. `labels` is reintroduced as a definition-only yaml field in section 8c, with `json:"-"`, so it never becomes `Node.spec.labels`
- [x] 1.2 Remove `MysocketProxy` from `Extras`
- [x] 1.3 Delete the now-unused `HealthcheckConfig` type and its alias in `util/containerlab/types.go`
- [x] 1.4 Initially dropped `labels` from the explicit merge set; superseded by section 8c, which intentionally merges definition-only `labels` alongside env and sysctls before copying them to Node metadata

## 2. Align stale shapes with containerlab

- [x] 2.1 Change `EnforceStartupConfig` to `*bool`
- [x] 2.2 Add `SuppressStartupConfig *bool` (`suppress-startup-config`)
- [x] 2.3 `CertificateConfig`: `Issue` to `*bool`, add `KeySize int` (`key-size`) and `SANs []string` (`sans`)
- [x] 2.4 `validity-duration` resolved: containerlab 0.78.0 *does* decode a duration string, and strictly (`notaduration` fails with `cannot unmarshal !!str into time.Duration`). Shipped as a `string` with a Go duration pattern; `metav1.Duration` rejected since it implements json marshalling only

## 3. Add launcher-realizable escape hatches

- [x] 3.1 Add `Devices []string` (`devices`), `CapAdd []string` (`cap-add`), `ShmSize string` (`shm-size`)
- [x] 3.2 Add `Privileged *bool` (`privileged`), `Tmpfs map[string]string` (`tmpfs`), `SecurityOpts []string` (`security-opts`)
- [x] 3.3 Confirm every added yaml tag matches containerlab's spelling exactly -- checked against v0.78.0 `types/node_definition.go`, `types/types.go` and `types/component.go`, and enforced from here on by the task 7.1 test

## 4. Narrow `ports` and fix the parser

- [x] 4.1 Restrict `ports` entries to `<port>[/protocol]` with schema validation. The first CEL range rule exceeded the apiserver cost budget; the final item pattern spells out 1-65535 and `network-mode` is length-bounded before its remaining CEL rule
- [x] 4.2 Rewrite the `Ports` doc comment so the generated CRD description explains the semantics to users
- [x] 4.3 Move the `note: no yaml omitempty ...` implementation detail out of the doc block into `materializeTopology`, the rendering code it explains
- [x] 4.4 Rewrite `ProcessPortDefinition` (`util/containerlab/ports.go`) as a destination-only parse; `GetPortPattern` and `processPortDefinitionFull` are gone, along with the regex itself -- a `strings.Cut` plus `strconv.Atoi` is inherently anchored, and host IP bindings, ranges and unknown protocols now each get a named error
- [x] 4.5 Add the parser's missing unit test: `22`, `22/udp`, plus the previously misread `1.2.3.4:80:80`, `50000-50010:50000-50010`, and `22:22/sctp`
- [x] 4.6 Remove the unreachable user-pin branches from `ResolveExposedPorts` (`controllers/node/expose.go`), keeping retention of prior allocations and group collision avoidance
- [x] 4.7 Normalize two-sided port entries to their destination port in the Topology compiler, logging what was dropped so pasted containerlab topologies keep working
- [x] 4.8 Left `AsContainerlabPortDefinition` alone -- note it has *no* caller at all, and duplicates the inline `fmt.Sprintf` in `materializeTopology`

## 5. Make the Node schema strict

- [x] 5.1 Remove `+kubebuilder:pruning:PreserveUnknownFields` from `NodeSpec` in `apis/v1alpha1/node.go`
- [x] 5.2 Verify the regenerated CRD retains exactly one `x-kubernetes-preserve-unknown-fields` -- confirmed, the remaining one belongs to `config.vars`
- [x] 5.3 Add validation restricting `network-mode` to `container:<primary>`, with a message explaining it declares launcher grouping
- [x] 5.4 Update the `NodeSpec` doc comment, which currently promises that unknown containerlab vocabulary is preserved

## 6. Bump the launcher's containerlab

- [x] 6.1 Set `ARG CONTAINERLAB_VERSION="0.78.0+"` in `build/launcher.Dockerfile`
- [ ] 6.2 Build the launcher image and confirm `containerlab version` reports 0.78.0 -- **not run** (needs a docker image build). The vocabulary itself is verified against a local containerlab 0.78.0 in task 7.2
- [x] 6.3 Document 0.78.0 as the floor for `containerlabVersion` overrides -- on `LauncherProfile`, and on the `Config`/`Topology` deployment fields too, since all three feed the same launcher env var and both legacy doc comments recommended pinning *older*

## 7. Guard the vocabulary against drift

- [x] 7.1 Add a test that collects yaml tags from `NodeDefinition` and its sub-objects and asserts each exists in a snapshot of the pinned containerlab's vocabulary, with the refresh procedure documented in the test. Snapshot is embedded in the test rather than a fixture file (no new file, and it sits next to the refresh instructions). Verified it *fails* by temporarily re-adding `publish`
- [x] 7.2 Render a fully populated `NodeDefinition` through the real marshaller and parse it with containerlab 0.78.0 (`containerlab graph --offline --dot`): parses clean. Also covered by a yaml round-trip test over the whole vocabulary in `util/containerlab/topology_test.go`

## 8. Update tests, fixtures, and examples

- [x] 8.1 Remove `healthcheck` from `util/containerlab/topology_test.go` fixtures -- the two healthcheck-only helpers became one round trip over the *whole* curated vocabulary, which is what task 7.2 needed anyway
- [x] 8.2 Remove `healthcheck` from the clabverter input fixture and refresh the affected golden files
- [x] 8.3 Convert two-sided ports to destination-only in `controllers/topology/compile_test.go` and keep one case covering compiler normalization
- [x] 8.4 Update `examples/expose/no-auto-expose.yaml`, `examples/expose/README.md`, `examples/advanced/README.md`, and `docs/guides/expose-configuration.md` to the destination-only form
- [x] 8.5 Grep for remaining references to removed fields outside `generated/` -- none; every hit was unrelated prose or pod resources

## 8b. Keep the Topology definition permissive, but not silent

- [x] 8b.1 Decode the definition with `KnownFields(true)` in `LoadContainerlabConfig` and return unknown fields as warnings instead of failing -- the definition is native containerlab, so unimplemented vocabulary must not break a pasted topology
- [x] 8b.2 Keep genuine type errors fatal: yaml.v3 puts unknown fields and type errors in one `TypeError`, so they are split on yaml's `not found in type` marker. Verified against yaml.v3 directly first -- `binds: not-a-list` would otherwise decode to no binds at all
- [x] 8b.3 Log the warnings where users see them: `compileContainerlabDefinition` (controller, and clabverter `--emit-crs`) and clabverter's `load` (both clabverter modes). Known cosmetic wart: `--emit-crs` prints each warning twice, since it parses once itself and again via the compiler
- [x] 8b.4 Fix the nil deref this uncovered: a definition parsing with no `topology:` section panicked the reconcile, since `LoadContainerlabConfig` dereferenced `config.Topology` unconditionally. Now a parse error
- [x] 8b.5 Test that unknown fields warn without failing and without costing known vocabulary, that type errors/malformed yaml/missing topology still error, and that our own rendered vocabulary round trips warning-free
- [x] 8b.6 Verify end to end with `clabverter` over a topology carrying `settings`, `runtime`, `image-pull-policy`, `publish`, `restart-policy`, `memory`, `stages`, and `healthcheck`: all eight unsupported fields warned by name and line, while `labels` was handled by section 8c and conversion completed

## 8c. Map containerlab node labels onto kubernetes labels

- [x] 8c.1 Carry `labels` on `NodeDefinition` as `json:"-" yaml:"labels,omitempty"` -- parseable from a definition, absent from the Node API. Verified the regenerated Node CRD gains no `spec.labels` and the published schema still lists 33 spec properties
- [x] 8c.2 Merge labels across defaults/kinds/node in the overlay, alongside env and sysctls
- [x] 8c.3 Drop labels kubernetes would reject, labels in the `c9s.run/` namespace, and controller-owned label keys such as `app.kubernetes.io/name`, warning per label -- an unusable label would otherwise make the emitted Node rejected on create, and controller labels must not be overridden from a lab definition
- [x] 8c.4 Copy the compiled labels onto the emitted Node's `metadata.labels` in `RenderNodes`
- [x] 8c.5 Propagate a Node's labels to the launcher deployment and its pod template, skipping the `c9s.run/` namespace so an unlabeled lab renders an unchanged deployment. The pod selector is a separate map, so it is untouched (it is immutable once created)
- [x] 8c.6 Test all three hops: inheritance and dropping in the compiler, metadata on the rendered Node, and propagation to the deployment/pod template without leaking into the selector
- [x] 8c.7 Verify end to end with `clabverter --emitCRs`: `labels` no longer warns as unsupported, `owner: roman` appears in the emitted Node's `metadata.labels`, and no `spec.labels` appears anywhere in the manifest

## 8d. Normalize c9s-owned label keys

- [x] 8d.1 Rename c9s-owned label constants and all hard-coded selectors/templates/goldens from `clabernetes/...` to `c9s.run/...`; keep `clabernetes/...` annotation keys unchanged
- [x] 8d.2 Rename the direct e2e marker to `c9s.run/mode: direct`, retaining it on direct Nodes while excluding the c9s-owned namespace from launcher Deployment and Pod labels
- [x] 8d.3 Update user-facing selectors and examples to the renamed keys, including topology-owner and image-puller troubleshooting commands
- [x] 8d.4 Verify no legacy c9s-owned label key remains outside historical archive material

## 9. Regenerate and document

- [x] 9.1 Run the generators (deepcopy, CRDs in `charts/` and `assets/`, openapi, clientset, fmt). Dropped `crd:allowDangerousTypes=true` from the Makefile: it existed solely for containerlab's float `cpu`, which this change removes. A later openapi rerun was blocked by `uv`'s `invalid peer certificate: UnknownIssuer`; the checked-in output was structurally compared with the current CRD afterward
- [x] 9.2 Document the removed fields and where each one moved, in `docs/upgrading.md` -- including the two behaviors that are not simple removals: a Topology `definition:` warns rather than rejects, and `labels` are carried to Node/deployment/pod metadata
- [x] 9.3 Document how exposure actually works in `docs/guides/expose-configuration.md`: nodes sit behind docker inside the launcher pod, the auto-expose list is what makes management ports reachable, and `ports` is how anything else gets exposed
- [x] 9.4 Verify the generated CRD reference renders the trimmed Node and that the `ports` description carries the new semantics with no implementation notes leaking -- confirmed in the CRD yaml and in `generated/openapi/openapi.json` (33 Node spec properties agree, `labels` is absent, all removed fields are absent, all added fields are present); the docs pages render from that openapi via `<CrdViewer />`

## 10. Validate

- [x] 10.1 Unit tests pass (all non-e2e packages) and `golangci-lint run` reports 0 issues. Note `make test` itself fails in this environment on `go: no such tool "covdata"` -- a pre-existing toolchain issue, reproducible on packages this change never touches (the local go is 1.24.4 while go.mod wants 1.25.0, so an auto-downloaded toolchain module without `covdata` is used)
- [x] 10.2 Confirm the generated CRDs install: all six pass `kubectl apply --dry-run=server`, which compiles the CEL and prices it, and persists nothing. This is what caught the `ports` CEL rule exceeding the apiserver cost budget by >100x (see design Decision 9) -- a failure no offline check surfaces, and one that would have made the Node CRD, and so the chart, uninstallable
- [ ] 10.3 Run the e2e suite -- **not completed**. A direct test invocation against `rd-c9s` was invalid and failed during cleanup because the cluster had no c9s release installed. The proper remote setup then reached the registry stage, but image build was blocked before compilation because the tracked `.develop/target-platform.sh` is not executable in this checkout. That setup created only the `c9s` namespace and project registry Deployment/Service; no c9s manager or launcher was installed. The standard Make target remains KinD-specific
- [ ] 10.4 Manually confirm a Node with `runtime:` or `ports: ["22:22"]` is rejected by name, and that a Node declaring `devices`, `privileged`, and a non-default port deploys with that port reachable through its Service -- **not run**: the schema is verified (properties absent/present offline, CEL compiled by a real apiserver per 10.2), but no object-level accept/reject was attempted, since that needs the new CRD actually persisted to a cluster
