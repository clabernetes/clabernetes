## Context

`NodeSpec` embeds `NodeDefinition` (`apis/v1alpha1/containerlab.go`), 40 fields copied from an older containerlab release. How each field is consumed today:

```
Node.spec (40 containerlab fields)
  ├─ read by controllers ....... kind, type (default resources lookup)
  │                              image (pull-through + node selectors)
  │                              ports (expose allocation)
  │                              network-mode (launcher pod grouping)
  │                              mgmt-ipv4/6 (LoadBalancerIP)
  ├─ read by clabverter ........ startup-config, license, binds (file delivery)
  └─ everything else ........... marshalled verbatim into topo.clab.yaml
                                 → containerlab + docker INSIDE the launcher pod
```

`materializeTopology` builds a lab named `clabernetes-<launcher node>` containing only that launcher's group members, so every launcher pod runs its own single-node (or one-chassis-group) lab. Any containerlab feature whose meaning spans nodes of one lab therefore cannot work.

Containerlab parses topologies strictly. Verified against v0.78.0:

```
$ containerlab graph --offline --dot -t topo.clab.yaml
Failed to read topology file: yaml: unmarshal errors:
  line 7: field publish not found in type types.NodeDefinitionWithDeprecatedFields
  line 9: field wait-for not found in type types.NodeDefinitionWithDeprecatedFields
```

Cross-checked against the pinned v0.74.3 and v0.78.0 `types/node_definition.go`: `publish`, `sandbox`, `kernel`, `wait-for`, and top-level `SANs` exist in neither. They are landmines, not dead weight.

## Goals / Non-Goals

**Goals:**

- Reduce the Node vocabulary to fields a launcher pod can actually realize
- Make removals and unknown keys loud instead of silent
- Guarantee the vocabulary is a subset of the launcher's containerlab vocabulary, and keep it that way
- Expose the container escape hatches users need (devices, capabilities, shm, privilege)
- Bump the launcher's containerlab to 0.78.0

**Non-Goals:**

- Exposing `stages`, `credentials`, `link-apply-mode`, `pid-mode`, `cgroupns-mode`
- Strict parsing of the containerlab `definition:` block on Topology
- Moving management-network settings off `LauncherProfile`
- Any conversion webhook or field-deprecation grace period (the API is `v1alpha1`)

## Decisions

### 1. Curated subset, not a verbatim mirror

**Decision:** Treat the Node spec as a curated subset of containerlab vocabulary. A field earns its place only if the controller reads it, or containerlab-in-a-pod can realize it for a single node.

**Rationale:** The "verbatim mirror" framing is what let five nonexistent fields survive and what makes `LauncherProfile` and Node compete over resources, pull policy, and readiness. Three tests decide every removal: does the field exist in the pinned containerlab; does it mean something for one node in one lab; is it already owned by `LauncherProfile` or the pod spec.

**Removed from the Node API surface (17 + 1 sub-field):**

| Field | Reason |
| ------- | -------- |
| `publish`, `sandbox`, `kernel`, `wait-for`, `SANs` | Not in containerlab; hard-fail the launcher's `clab deploy` |
| `extras.mysocket-proxy` | Parses, but mysocketctl is gone |
| `runtime` | The launcher has exactly one runtime (docker) |
| `cpu-set` | Host CPU ids inside a pod cpuset: rejected or silently wrong |
| `cpu`, `memory` | Pod requests/limits already cap this; `LauncherProfile.resources` owns it |
| `image-pull-policy` | Fights the image pull-through machinery; `LauncherProfile.imagePull` owns it |
| `healthcheck` | Docker healthcheck status is invisible to Kubernetes; `LauncherProfile.statusProbes` owns readiness |
| `auto-remove` | The pod owns lifecycle; a removed container leaves a ready pod with no node and kills post-mortem debugging |
| `aliases` | Docker network aliases resolvable only inside one pod |
| `group`, `position` | Feed containerlab graphs and generated inventories; clabernetes produces neither |
| `startup-delay` | Staggering boots on one host; pods start independently |
| `labels` | No `spec.labels`: Docker container labels are not useful to c9s, so Topology definition labels are mapped to Node `metadata.labels` instead (Decision 10) |

**Kept in the Node API (23):** `kind`, `type`, `image`, `license`, `startup-config`, `enforce-startup-config`, `config`, `binds`, `env`, `env-files`, `exec`, `entrypoint`, `cmd`, `user`, `ports`, `network-mode`, `mgmt-ipv4`, `mgmt-ipv6`, `sysctls`, `dns`, `certificate`, `components`, `extras`. Definition-only `labels` is a separate compatibility field and is not part of this count.

`binds` stays because it is how ConfigMap-delivered files reach the node container. `mgmt-ipv4/6` earns its place twice: in-pod docker address plus the `useNodeMgmtIpv4Address` LoadBalancerIP path.

### 2. `config.vars` stays — it is live at deploy time

**Decision:** Keep `config`.

**Rationale:** The `containerlab config` engine command is gone from the pinned release (no `cmd/config.go` in v0.74.3), which suggested the field was inert. It is not: `DefaultNode.GenerateConfig` renders every startup config through `SubstituteEnvsAndTemplate(…, d.Cfg)`, and `types.NodeConfig` carries `Config *ConfigDispatcher`, so `{{ .Config.Vars.x }}` resolves during `clab deploy`.

### 3. Added fields (7)

**Decision:** Add `devices`, `cap-add`, `shm-size`, `suppress-startup-config` (all present in v0.74.3) plus `privileged`, `tmpfs`, `security-opts` (v0.78.0).

**Rationale:** Node kinds auto-add common devices, but nothing lets a user add a device, grow shm, or drop a seccomp profile. `suppress-startup-config` pairs with persistence.

**Excluded:** `pid-mode`/`cgroupns-mode` (only meaningful across containers of one pod, nothing asks for it), `credentials` (feeds a generated `ssh_config` inside the lab dir), `link-apply-mode` (clabernetes owns link lifecycle and the launcher runs `deploy`, never `apply`), `restart-policy` (deferred — see Open Questions).

`stages` is excluded by design rather than deferred. Stage machinery exists to order and gate the nodes of one lab against each other, which presupposes the whole lab running on one host; a launcher pod holds one node (or one chassis group), and ordering across pods is Kubernetes' concern. The per-stage `exec` hooks are the only single-node-meaningful part and they overlap with the `exec` field that is kept.

### 4. Shape alignment with containerlab

**Decision:** `certificate` becomes `{issue *bool, key-size, validity-duration, sans}` and `enforce-startup-config` becomes `*bool`.

**Rationale:** Both are non-pointer bools today, so explicit `false` is indistinguishable from unset and is dropped by `omitempty`. SANs are currently unreachable: the only field naming them is one of the five that break the lab.

**`validity-duration` (resolved):** containerlab types it as `time.Duration` behind a plain yaml tag, which raised the question of whether a duration string decodes at all. Verified against containerlab 0.78.0: it does, and strictly — `validity-duration: 8760h` parses, while `notaduration` fails with `cannot unmarshal !!str notadur... into time.Duration`. clabernetes therefore carries the field as a `string` validated by a Go duration pattern, which renders verbatim and round-trips. `metav1.Duration` was rejected because it implements only json marshalling, so the yaml render would emit the wrapper struct.

### 5. Strict schema over silent pruning

**Decision:** Remove `+kubebuilder:pruning:PreserveUnknownFields` from `NodeSpec`.

**Rationale:** It buys nothing today — the launcher marshals the typed struct, so preserved unknown fields never reach the rendered topology — while defeating kubectl strict field validation. Without it, `kubectl apply` answers `unknown field "spec.runtime"` instead of accepting a Node that will never behave as written.

**Note:** The second `x-kubernetes-preserve-unknown-fields` in the CRD belongs to `config.vars` (arbitrary JSON values) and must stay.

**Strictness stops at the Node.** A Topology `definition:` is *native containerlab text*, and a user must be able to paste a working topology and have it run. So the two entry points differ deliberately: the Node CR rejects an unknown field, while the compiler drops it and warns. Making the definition strict too would mean any containerlab vocabulary clabernetes has not implemented — `stages`, or anything a newer containerlab adds — turns a previously working topology into a hard failure, and clabernetes would have to chase containerlab's vocabulary release for release just to keep pasting working.

The leniency is bounded to *unknown* fields. `yaml.v3` reports unknown fields and genuine type errors in one `TypeError`, so `LoadContainerlabConfig` splits them: entries carrying yaml's `not found in type` marker become warnings, and anything else — a malformed document, or a known field holding the wrong type — stays fatal. Downgrading the latter would be data loss, since `binds: not-a-list` would otherwise silently decode to no binds at all. Splitting on a message substring is admittedly brittle, but it is the only signal yaml.v3 exposes; `TestLoadContainerlabConfigWarnsOnUnknownFields` fails if a yaml bump changes the wording, which turns a silent behavior flip into a build failure.

Warnings are logged rather than surfaced on the Topology's status, matching how the compiler already reports port normalization. Nothing in the repo emits Kubernetes Events, so introducing an EventRecorder for this alone was rejected. Two known warts: `clabverter --emit-crs` prints each warning twice, because it parses once itself and again through the shared compiler, and the controller repeats them every reconcile. Both are cosmetic and neither justified threading suppression state through the compiler.

### 6. `network-mode` remains the grouping declaration, now validated

**Decision:** Keep the containerlab-native field; reject values other than `container:<primary>`.

**Rationale:** `ResolveLauncherNode` derives pod grouping from it, and the SR-SIM chassis workflow is documented in those terms. The gap is that `host` — meaningless and harmful in a launcher pod — is currently accepted silently.

### 7. Containerlab version floor is documented, not validated

**Decision:** Bump `CONTAINERLAB_VERSION` in `build/launcher.Dockerfile` and document 0.78.0 as the floor for `LauncherProfile.deployment.containerlabVersion` overrides.

**Rationale:** That override downloads an arbitrary containerlab from GitHub releases at launcher startup, so pinning something older reintroduces exactly the strict-parse failure this change removes (`field privileged not found …`). Semver comparison of a free-form string in CEL is more machinery than the risk warrants; the vocabulary conformance test pins the build-time version, which is the case that matters.

`0.78.0` is published in the netdevops apt repo for both architectures. The trailing `+` in the existing value is apt's install marker (the counterpart of `pkg-`), not an "or newer" wildcard, so the pin stays exact.

### 8. `ports` declares destination ports only

**Decision:** Accept `<port>[/protocol]`. Reject the docker-style `<host>:<container>` form on Nodes, normalize it to its destination port in the Topology compiler, and rewrite the parser to be anchored and to error on forms it cannot represent.

**Rationale:** `spec.ports` is intent, not payload — it never reaches the rendered topology:

```
spec.ports ──► ResolveExposedPorts (+ auto-expose defaults, retained prior
               allocations, group-mate collision avoidance) ──► status.exposedPorts
                     ├──► expose Service: port = destination → targetPort = exposePort
                     └──► topo.clab.yaml ports, all rendered on the group primary
```

`materializeTopology` zeroes `Ports` on every member before rendering, so the pod-side port is an allocation clabernetes owns from the 60000-64999 range. Pinning it cannot help — clients always reach the natural port through the Service — and it can hurt: user pins "win unconditionally" with no conflict check, so two chassis members pinning the same value render duplicate publishes on one node and docker rejects the lab.

Removing the field outright was considered and rejected. Node containers sit on a docker bridge network inside the launcher pod, not in the pod's namespace, so a port is reachable only when docker publishes it. Without `ports`, the hardcoded auto-expose list (21, 22, 23, 80, 443, 830, 5000, 5900, 6030, 9339, 9340, 9559, 57400 TCP and 161 UDP) becomes a permanent ceiling, and `LauncherProfile.expose.disableAutoExpose` degenerates into "expose nothing, ever".

The parser fix rides along because it is the same vocabulary-validation work and is broken in the same silent way: `GetPortPattern` is unanchored and used with `FindStringSubmatch`, so `1.2.3.4:80:80` yields pod port 4, `50000-50010:50000-50010` collapses to `50010:50000`, and `22:22/sctp` quietly becomes TCP. There is no test file for it today.

### 9. The new validations are shaped by the apiserver's CEL cost budget

**Decision:** Enforce the `ports` range with a regex alternation rather than a CEL rule, and bound `network-mode` with `MaxLength=73`.

**Rationale:** Found by dry-running the generated CRD against a real apiserver, which is the only place this surfaces — `controller-gen` emits the rule happily and every offline check passes. The first attempt validated the range with `self.all(p, int(p.split('/')[0]) >= 1 && int(p.split('/')[0]) <= 65535)` and was rejected at install time:

```
spec.…properties[ports].x-kubernetes-validations[0].rule: Forbidden: estimated rule
cost exceeds budget by factor of more than 100x
```

The estimator multiplies worst cases, so an unbounded list of unbounded strings is priced as if every entry were maximal, and a CRD that exceeds the budget cannot be installed at all. Hence: no CEL over `ports` (the alternation `[1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5]` spells out 1-65535 at zero cost), and a length bound on `network-mode` so its rule is priced against 73 characters instead of an unbounded string.

The trade-off is legibility: the alternation is opaque next to the CEL it replaces, and an off-by-one in it silently widens what the apiserver accepts. `TestNodePortsPattern` reads the pattern back out of the generated CRD and asserts the boundaries (`0`, `65536`, `022`, `22:22`, `50000-50010`, `22/sctp`), so the claim is checked rather than asserted in a comment.

### 10. `labels` is removed from the Node spec but kept in the definition vocabulary

**Decision:** No `spec.labels` on a Node. A Topology definition's node labels are parsed, inherited like `env`, and copied onto the emitted Node's `metadata.labels`, from where the Node controller propagates them to the launcher Deployment and its pods. The field is carried on `NodeDefinition` as `json:"-" yaml:"labels,omitempty"` — parseable from a definition and renderable in the overlay merge, absent from the Node API.

**Rationale:** Labels are the one removed field with a native Kubernetes home, so "removed" was the wrong shape for it: in containerlab they become docker labels on the node container, which in this architecture is a container inside a pod nobody selects on. `metadata.labels` is both the idiomatic place and the useful one — `kubectl get pods -l owner=roman` reaches the launcher pods, which docker labels never could. Dropping the vocabulary outright would have meant a pasted topology's labels silently disappearing (with a warning, after Decision 5), when the intent maps cleanly onto Kubernetes.

`json:"-"` is what makes both halves true at once, and it is the mirror of the `yaml:"-"` already used in `NodeSpec` for the fields that are Node-only. The field never persists: the compiler reads it in memory and writes metadata, and the launcher — which reads the Node CR as JSON — never sees it, so no docker labels are rendered. `controller-gen` and `openapi-gen` both skip it, so the CRD and the published schema have no `spec.labels` (verified: 33 spec properties, `labels` absent).

Three classes of label are dropped at compile time, each warned by name:

- **Invalid as a Kubernetes label.** Docker label keys and values are far more permissive. An unusable one would otherwise produce an emitted Node the apiserver refuses to create, which is a stuck topology — so this is a trust boundary, validated with `k8svalidation.IsQualifiedName`/`IsValidLabelValue`.
- **In the `c9s.run/` namespace.** Those labels mean things to the controllers (`c9s.run/ignoreReconcile`, `c9s.run/disableDeployments`), and a lab definition should not be able to set them on its own Nodes.
- **Controller-owned non-`c9s.run/` keys.** `app.kubernetes.io/name` is part of the launcher selector and `c9s.run` also owns the standard `name`, `app`, topology-owner, topology-kind, and topology-node keys. These exact keys are reserved too; otherwise a valid user label could overwrite a Node metadata invariant while being replaced on the Deployment.

The Deployment propagation deliberately skips the same `c9s.run/` namespace rather than copying every Node label. It keeps the controller-owned labels controller-owned, and it means a lab *without* custom labels renders a byte-identical deployment — no gratuitous pod roll on upgrade. The pod selector is built from a separate map, so none of this can touch that immutable field.

### 11. Use `c9s.run/` for c9s-owned labels

**Decision:** Rename c9s-owned label keys from `clabernetes/...` to `c9s.run/...`, including the
direct-resource test marker as `c9s.run/mode: direct`. Keep the existing `clabernetes/...`
prefix on annotations; this change is label cleanup, not an annotation migration.

**Rationale:** `c9s.run` is the current API identity and gives c9s-owned metadata a clear,
qualified namespace. The old unqualified-looking `clabernetes/...` keys are ambiguous next to
the API group's current `c9s.run` identity. Existing resources need the new labels on upgrade
because the manager's cache and selectors use the renamed keys; the normal uninstall/reinstall
upgrade path already replaces generated resources.

## Risks / Trade-offs

| Risk | Mitigation |
| ------ | ------------ |
| Existing Nodes using removed fields fail to re-apply | Every removed field is inert or already breaks the launcher; release notes list all 17 with their replacement |
| Users hit `unknown field` where they previously got silence | That is the point; the error names the field |
| Topology `definition:` drops removed keys | Deliberate, so pasting a native containerlab topology keeps working; each dropped field is warned with its line, see Decision 5 |
| Warning-only reporting is missable in controller logs | Accepted: no Event machinery exists in the repo, and `clabverter` surfaces the same warnings before anything is applied |
| A `NodeDefinition` field invisible to the API (`labels`, via `json:"-"`) surprises a future reader | Documented at the field and in Decision 10; the mirror of the `yaml:"-"` fields already in `NodeSpec`, and covered by tests at all three hops (compile, render, deployment) |
| Labels on a Node now reach its deployment and pods, rolling pods once | Only for labs that actually carry custom labels: reserved namespaces and controller-owned keys are excluded, so an unlabeled lab renders an unchanged deployment |
| 0.74.3 → 0.78.0 spans four minor releases of launcher behavior | Vocabulary conformance test plus the existing e2e suite exercise `deploy` end to end |
| A future clab bump silently invalidates the vocabulary again | Conformance test fails until the checked-in field snapshot is refreshed |
| A future validation marker blows the CEL cost budget and makes the CRD uninstallable | Only reproducible against a real apiserver; `kubectl apply --dry-run=server` on the generated CRDs is the check, see Decision 9 |

## Migration Plan

1. Users remove the 17 fields from Node manifests; `cpu`/`memory` move to `LauncherProfile.resources`, `image-pull-policy` to `LauncherProfile.imagePull`, `healthcheck` to `LauncherProfile.statusProbes`, `SANs` to `certificate.sans`. Topology `definition:` blocks need no edit — the compiler warns per dropped field, so the same migration can be done from the warnings at leisure.
2. Upgrade clabernetes; the manager applies the regenerated Node CRD.
3. Re-apply manifests. Anything still carrying a removed field is rejected by name.

**Rollback:** A prior release restores the wider schema. Nodes authored against the new vocabulary remain valid there except for the seven added fields, which the older launcher's containerlab cannot parse.

## Open Questions

- **`restart-policy`.** Nothing in Kubernetes restarts a crashed *node* container — the pod stays up as long as the launcher process lives — so in-pod docker restart policy is the only mechanism that recovers a crashed node without rolling the pod. That argument does not apply to `auto-remove`. Deferred rather than decided.
- **Compiler asymmetry (resolved).** The asymmetry stays — a strict Node CR, a lenient definition — because pasting a working containerlab topology is precisely how the Topology resource gets used. What is fixed is the silence: the compiler now warns per dropped field with its line, so `group: foo` in a `definition:` is reported rather than vanishing, while the same key on a Node is still an apply-time error. Two-sided `ports` remain resolved by normalization, because pasted topologies routinely carry `57400:57400`. Whether these warnings eventually belong on Topology status instead of the log is left open.
- **Ports on grouped Nodes.** Chassis members share one network namespace, so a destination port lands wherever it was bound regardless of which member's Service carried it — a secondary's expose Service is effectively cosmetic. Today every member gets its own allocation and Service. Options are to document that, or to reject `ports` on secondary Nodes. Undecided.
- **Pod-namespace nodes.** Running node containers in the launcher pod's own namespace would make every node port reachable on the pod IP and delete the allocation layer entirely. It also costs clab's management network (`mgmt-ipv4/6`, in-lab DNS), risks collisions with the launcher's own listeners (the vxlan tunnel on 14789, probe endpoints), and changes dataplane stitching plus the grouping signal. Worth its own exploration, not a rider here.
