## Context

See `proposal.md` for motivation. The current Node controller creates one `Deployment` per launcher group. That Deployment runs `/clabernetes/manager launch` from the launcher image, mounts `/var/lib/docker`, may mount the worker CRI socket and registry hosts, renders a containerlab topology, starts dockerd and containerlab, imports images, discovers nested containers, and runs link/status repair from the launcher process. `LauncherProfile` and global `Config` consequently mix reusable Kubernetes policy with Docker, containerlab, and launcher-process settings.

The supported launcher image currently installs containerlab 0.78.0 (`ARG CONTAINERLAB_VERSION="0.78.0+"` is the Debian package selector), and the local integration binary reports upstream 0.78.0. The upstream registry contains aliases, native container kinds, vrnetlab/VM kinds, component-based kinds, and pseudo kinds whose behavior is not described by the c9s Node schema alone. Containerlab kind implementations currently combine deterministic defaults with filesystem, image-runtime, namespace, and lifecycle side effects, so importing its existing `nodes.Node` interface into the controller would not create a safe planning boundary.

Kubernetes 1.31 or newer is already required for selectable Link endpoint fields. That baseline includes restartable init containers (native sidecars), which can establish Pod-network state before regular application containers start and continue reconciling it afterward.

## Goals / Non-Goals

**Goals:**

- Make every device and component a regular kubelet-managed application container while retaining the existing bounded Node, Link, LauncherProfile, and optional Topology ownership model.
- Consume kind intent exclusively from an unmodified, pinned containerlab Go module through generic record/replay boundaries; a normal module bump must admit new registered kinds without c9s kind code.
- Preserve cold-start ordering, live connectivity, host Links, static management intent, private images, persistence, observability, and lifecycle operations without a second container runtime.
- Provide a staged migration in which nested and direct modes can coexist temporarily, but in which the final release contains only direct mode.

**Non-Goals:**

- Reimplement a general OCI runtime, Docker API, or containerlab command interpreter inside c9s helpers.
- Treat manifest rendering, an available image subset, or a successful Linux/SR Linux lab as proof of complete compatibility.
- Automatically fall back to the launcher for an unsupported direct plan.
- Preserve Docker-specific fields merely because they are present in the current alpha API.
- Port a launcher-only vendor repair into direct Pods before direct runtime evidence proves that the same generic gap still exists.

## Decisions

### 1. Pin an exact behavior baseline and generate one compatibility inventory

Add `compatibility/containerlab/baseline.json` for the exact upstream module identity, plan schema, and generic capability/scenario/behavior contract. It deliberately contains no kind names, aliases, registry digest, availability rows, or evidence rows. The live imported registry is the only kind inventory, so a dependency update cannot be blocked by a stale c9s catalog.

A repository tool obtains the registry from the pinned containerlab Go module, normalizes aliases, and generates ephemeral reports and parameterized conformance inputs. Verification checks the imported module identity, live registry construction, generic operation coverage, documentation, and any remaining build-time version reference; it never compares names to a committed matrix. A new kind or alias is exercised automatically and does not require a c9s allowlist, mapping, or hand-authored fixture. Verification fails only when imported execution records a generic operation the direct runtime cannot represent. Debian package revision syntax is not treated as the behavior version.

Alternatives considered:

- A handwritten kind list cannot prove exhaustiveness and makes aliases easy to miss.
- Scraping documentation is not authoritative; the registered runtime registry is.
- Keeping `0.78.0+` as the only pin allows packaging details to masquerade as a compatibility contract and disappears when the launcher image is removed.

### 2. Import unmodified containerlab and own the planning adapter in c9s

c9s adds `github.com/srl-labs/containerlab` as an intentionally pinned Go module dependency without a local replacement, fork, or required upstream patch. It constructs the authoritative kind registry inside a c9s planning worker and owns a small versioned planning package that accepts only explicit normalized inputs. Updating the dependency and generated dependency data is the complete kind-import workflow: every registered kind flows immediately through the same generic adapter.

The c9s adapter invokes exported containerlab registry and Node hooks against a generic recording runtime and a controlled, disposable filesystem workspace. The runtime supplies explicit OCI and topology observations and records container creation, lifecycle, exec, namespace, and file-transfer operations instead of performing them. The workspace supplies only explicit payload/certificate inputs, confines generated artifacts, snapshots their metadata and digest, and is removed after planning. Uncontrolled host paths, real image pulls, real container launches, and real host/network mutation are forbidden.

Imported Go code can call operating-system APIs without passing through the containerlab runtime interface, so planning MUST NOT execute in the long-running manager process. The Node controller runs the adapter in a short-lived, content-addressed c9s planning Pod with no service-account token, no host paths, a read-only root filesystem, a private writable scratch volume, `allowPrivilegeEscalation: false`, a runtime-default seccomp profile, and a bounded deadline. Its capability set drops `ALL` and adds only `CHOWN` and `FOWNER`: imported preparation uses those generic filesystem operations for package-owned ownership and ACL intent, while the read-only root and projections leave only private scratch writable. It has no ambient capabilities, network/namespace capability, runtime socket, or privileged security context. Inputs contain identities and metadata rather than secret bytes. The manager accepts only strict canonical plan output matching the requested input and compatibility digests. Any hook that escapes the generic recorder/workspace or needs another forbidden capability fails planning without access to manager or node state. This worker is the ordinary c9s image linked with the imported Go package; it does not contain or invoke the containerlab executable and it does not require a change to containerlab.

Generated artifact bytes remain out of the plan. The target Pod's preparation init container reruns the same imported deterministic generation against its mounted explicit payload/certificate inputs and verifies every planned path, mode, and digest before application containers start. This keeps sensitive bytes inside the target Pod while making planner nondeterminism a visible failure.

Generic application operations recorded while an imported `Deploy` hook reconstructs container state are replayed in their original order as typed post-start file, stdin, and exec actions. They complete before the opaque imported `PostDeploy` hook, which in turn completes before topology-declared exec commands. Because Kubernetes executes lifecycle hooks for different application containers independently, a recorded sequence that crosses container boundaries fails with the generic `cross-container-lifecycle` capability until a Pod-level orchestrator can preserve that ordering; c9s does not infer an exception from the emitting kind or component identity.

Lifecycle and endpoint workers reconstruct the imported Node by running `Deploy` and runtime-info hooks against the same side-effect-free generic recorder used by planning. They verify the reconstructed container identities and ordered operation types, targets, commands, wait modes, destinations, write modes, and artifact digests against the accepted plan before switching that Node instance to the live application or endpoint runtime. Rehydration therefore restores package-owned in-memory state without executing file, stdin, or process operations a second time; any drift fails as the generic `deployment.replay` invariant before `PostDeploy` or endpoint work runs.

When an imported application lifecycle hook requests `StreamLogs`, the application-local runtime connects to a plan-scoped Unix socket shared with the fixed connectivity sidecar. Only that sidecar receives a projected, rotating token for a dedicated direct-runtime service account. Its namespace RoleBinding permits only `get`, `list`, and `watch` on Links plus `get` on Pods and Pod logs; it grants no ImageRequest, workload mutation, image-import, or nested-runtime authority. Device containers receive neither the token nor Kubernetes API access. The broker verifies its own downward-API Pod UID, derives the runtime-ID-to-container mapping from the accepted normalized plan and the running Pod's application-container order, and rejects every other target before opening a follow stream for that same Pod. This preserves package-owned log-driven lifecycle behavior without a runtime socket, kind dispatch, or Kubernetes credentials in device images.

The imported runtime's `GetHostsPath` operation is represented as a typed append of the package-generated artifact to the existing runtime-owned file. It never becomes a replacement copy and the lifecycle runner does not select this behavior by destination path or kind identity. Ordinary `CopyToContainer` remains a typed replacement; mounted-file replacement falls back only on the generic kernel condition that prevents atomic rename.

Generated filesystem inventory distinguishes regular byte artifacts, symbolic links, and directories. It records package-relative ownership plus bounded extended-attribute names and value digests; attribute values remain out of the plan and are regenerated inside preparation. A link records its target and target digest as generic plan data. Preparation recreates leaves atomically, applies directory metadata deepest-first, verifies every regenerated metadata digest, and never follows a link in any destination parent. Its security context has the same scratch-confined `CHOWN`/`FOWNER` boundary as planning. Byte-copy, stdin, and save actions accept only regular artifacts; directory mounts may expose package-generated links, modes, ownership, and POSIX ACLs contained within the verified artifact tree. This preserves package filesystem structure without naming the emitting kind or weakening the preparation escape boundary.

The adapter translates recorded operation types, never kind identities. There are no kind-named switches, copied vendor defaults/templates, initializer allowlists, canonical-kind dispatch maps, or manual fixture registrations in c9s. Containerlab's imported hooks remain the sole source of container, component, generated-file, lifecycle, readiness, interface, and save behavior. Identical generic operations have identical Kubernetes semantics regardless of which current or future kind emitted them.

The normalized plan contains:

- logical Node and component identities, namespace-sharing relationships, and stable container names;
- OCI image references and optional digest, entrypoint/command overrides, environment, user, working directory, ports, stop signal, and image-health probe translation;
- typed file inputs and outputs, mounts, tmpfs/shared memory, persistence targets, devices, sysctls, capabilities, privilege, and security profiles;
- typed prepare, pre-start, post-start, readiness, post-stop, save, and interface-fixup actions with explicit target container/namespace;
- management interfaces, addresses, routes, DNS, certificate inputs, endpoint names/MTU, link-apply mode, and readiness dependencies; and
- schema version, compatibility baseline, normalized input digest, and planner build identity.

Arbitrary shell strings are retained only where containerlab already defines user `exec` intent; kind-owned lifecycle behavior is represented by typed actions whenever possible. Plans refer to Secret/ConfigMap/payload identities rather than embedding secret bytes. Normalized serialization is stable and fixture-testable.

The plan schema and Kubernetes realization remain c9s-owned; standalone containerlab is unchanged. Registry-driven tests instantiate every live kind and alias and run common scenario classes through the same recorder. A kind remains `planned`, not `compatible`, until its emitted generic operations are representable and its applicable direct-runtime conformance passes, but enabling it never requires c9s kind code.

Alternatives considered:

- Adding a planner to containerlab would create unnecessary cross-repository coupling for a Kubernetes-specific consumer and make c9s delivery depend on upstream changes.
- Calling imported hooks in the manager or against the real host/runtime would execute side effects; the isolated planning Pod, controlled workspace, and recording runtime are mandatory.
- Copying the full kind registry or maintaining patched kind implementations in c9s would create a fork and make baseline bumps unverifiable.
- Embedding raw Kubernetes API objects in the plan would couple kind intent to rendering and make fixture-level validation harder.

### 3. Resolve image configuration without pulling device layers through c9s

Some compatibility decisions require OCI configuration such as image entrypoint, command, environment, labels, architecture, and Docker healthcheck. The controller adds a metadata resolver that fetches only registry manifests and configuration blobs, using same-namespace Kubernetes pull secrets and explicit global registry trust policy. It never downloads layers or imports an image. The returned digest and normalized OCI config are explicit planner inputs; the kubelet still performs the only device-image pull and launch.

Per-launcher insecure-registry and pull-through behavior is removed. Private authentication maps to `imagePullSecrets`. Registry mirrors, insecure transport, and node-specific certificate trust remain cluster-runtime administration; if controller metadata access needs a private CA or explicitly allowed HTTP endpoint, c9s uses a separate narrowly scoped registry-metadata trust configuration and documents that it must match the cluster's pull path. Metadata resolution failure prevents planning rather than producing an image-dependent guess.

Alternatives considered:

- Mounting CRI sockets into each workload preserves privileged runtime coupling and does not provide a portable config shape.
- Starting a probe container merely to force an image pull adds an observable throwaway workload and still relies on CRI-specific verbose status.
- Ignoring OCI healthchecks or defaults violates the compatibility contract.

### 4. Keep one Recreate Deployment per launcher group, but replace its contents

The existing primary/group ownership and `Deployment` Recreate strategy remain useful. The renderer changes from one launcher container to the following ordered Pod:

1. a short-lived preparation init container that validates the plan and stages generated/downloaded artifacts into plan-scoped `emptyDir`, Secret, ConfigMap, or PVC volumes;
2. a restartable privileged connectivity init container with a startup probe that succeeds only after the accepted cold-start interface set is present; it continues as a sidecar for Link reconciliation and direct runtime status actions; and
3. one regular application container for each grouped logical Node or planned chassis component, using the actual device images.

The controller stores the non-secret normalized plan in an immutable, owner-referenced ConfigMap named by its digest. Secret and payload references are mounted independently. The plan digest and the complete accepted cold-start Link digest annotate the Pod template. A Node/group/config change creates a new plan and recreates the Pod. A Link-only change is applied live when every affected kind plan permits it; otherwise the declared restart or recreation action is used.

Each immutable cold plan also owns one bounded, mutable connectivity-revision ConfigMap whose stable name includes that cold plan digest. It contains only the planner-produced interface input, interface plan, interface-wait actions, and the cold and effective-live plan digests. The controller accepts a Pod-retaining revision only when every non-interface planning input is unchanged and every changed Link endpoint declares `Live` or `Restart` through the imported package. The helper merges the projected interface state into its immutable cold input and plan, retaining cold-only container metadata until a later legitimate recreation, and accepts the revision only when normalization reproduces the exact effective-live plan digest. This follows the package contract: imported planning may rebuild creation-time metadata from the new endpoint inventory even though `Live` requires no lifecycle action and `Restart` retains the existing container specification. Application and preparation containers do not mount this revision. The helper's readiness probe compares the currently projected effective-live digest with its last successfully applied digest, so a pending or rejected update is not reported ready. A `Live` update changes this ConfigMap without changing the Pod template. For `Restart`, the manager first verifies that exact helper readiness command through the Pod exec subresource, then invokes a fixed shell-independent c9s binary inside every affected direct application container to signal PID 1 with its planned stop signal; kubelet `RestartPolicy=Always` restarts the container in the same Pod. A plan-digest marker plus observed container ID and restart count make this reconciliation idempotent. Only `Recreate` or non-connectivity input change uses a new cold-plan-scoped ConfigMap and rolls the Deployment while the old Pod retains its prior revision until termination.

Container names are deterministic DNS labels derived from logical Node and component identity. The plan maps each Node to its readiness-owning containers so the controller can translate `Pod.status.containerStatuses` back into bounded per-Node status. A standalone Node still has one Deployment/Pod; `network-mode: container:<primary>` adds application containers to the primary's Pod; one component Node may add several application containers without creating extra logical Node objects.

The retained `LauncherProfile.resources` field applies to each logical Node's primary application container during migration. Kind plans supply required component minima/distribution; a later API revision may expose per-component overrides only if real matrix cases require them. `extraEnv` is not retargeted from launcher to device because that would be a silent semantic change.

Alternatives considered:

- One Pod per component cannot represent container namespace sharing as one logical chassis and complicates atomic lifecycle.
- Jobs or bare Pods lose the existing declarative restart and rollout ownership.
- A manager-only renderer cannot perform in-namespace preparation after volumes and the sandbox exist.

### 5. Use a restartable connectivity init-sidecar, with node cleanup only for host-owned state

The connectivity helper shares the device Pod network namespace. As a restartable init container, it starts after file preparation, creates the planned initial interfaces, and holds its startup probe false until ordering requirements are met; Kubernetes then starts regular device containers while the helper remains running. It watches only Links for its group, validates the Pod/Node/Link UIDs and plan digest, and applies live changes idempotently.

The helper also invokes the imported Node's generic `DeployEndpoints` and `PostDeployEndpoints` hooks in that order after c9s has created the accepted interfaces and before it publishes cold-start readiness. Containerlab normally calls these hooks from the host namespace while `ExecFunction` temporarily enters the device namespace, and some package-owned fixups depend on that distinction. The helper therefore opens its own Pod network namespace, temporarily enters the worker host network namespace through one read-only namespace-file mount, supplies the retained Pod namespace path through the generic runtime, invokes the imported hooks, and restores the Pod namespace before continuing. The host namespace file is mounted only into the fixed privileged helper: device containers receive neither that handle, host PID access, a host filesystem mount, nor a runtime socket. If an endpoint hook also requests an application exec, filesystem, stdin, log, event, or container-lifecycle operation that this boundary cannot address faithfully, reconciliation fails with that generic runtime capability rather than executing it in the helper or recognizing the kind.

Certificate projections follow the same execution boundary. Preparation alone may receive the CA signer needed to issue or reproduce package-requested material. An endpoint or application lifecycle worker receives only the CA certificate and the already-issued certificate/private-key pairs for its opaque target Node identities; the CA signing key and unrelated Node credentials are never mounted into that worker.

For ordinary cross-Pod Links, the helper adapts the existing VXLAN and slurpeeth transports to terminate directly in the Pod namespace and addresses peers through current Pod identity. Each direct Node fabric Service is headless and publishes its single selected Pod before readiness; the plan carries that stable same-namespace Service name as generic peer-transport identity. The helper resolves exactly one current Pod address, prepares the package-selected interface even while no peer exists, reconciles the resolved address periodically without Pod list/watch authority, and keeps connectivity unready until the peer is usable. A Pod-IP change updates only the owned tunnel endpoint, while a Link rewire changes the Link/Node-UID-derived owner and removes the former endpoint exactly. Same-Pod, loopback, and local veth state stays inside the sandbox and is removed automatically with it. Management intent is realized on the planned management interface while the Kubernetes Pod IP remains available for API, DNS, probes, Services, and tunnel transport. Static addresses are controller-allocated for uniqueness and advertised through the c9s management overlay; unsupported overlapping or cluster-inaccessible requests fail planning.

The nested launcher's SR Linux DNS-forwarding repair is not a direct-runtime requirement by default. It repaired a path that crossed the launcher Pod, an inner Docker management network, and device-owned namespace state. Direct conformance first boots the unmodified device image as the application container using only imported containerlab behavior and verifies management addressing, DNS, Service, and external reachability. If those observations pass, c9s deletes the old repair without replacing it. If they fail, the evidence must identify a generic missing Kubernetes/runtime capability. c9s may then implement that capability once for identical generic plan operations, but it must not select a kind or vendor, copy device-internal namespace/interface names from the launcher or documentation, or require a containerlab patch. When imported behavior emits no representable operation and no kind-opaque platform capability can satisfy the observation, the affected conformance remains failed instead of gaining a vendor workaround.

A direct slurpeeth endpoint is one UID-owned veth pair in the Pod namespace: the device receives the package-selected interface name and the transport binds the deterministic helper-side peer. The connectivity helper renders only generic segment ID, peer address, and helper-interface values through the imported slurpeeth configuration and wire-protocol types. The pinned library's process-global manager cannot provide a resettable, race-free reconnect lifecycle, so a child of the same fixed c9s binary owns that generic listener, packet socket, and serialized per-destination reconnect boundary while remaining wire-compatible. It binds its stable listener before dialing peers, publishes readiness only while every destination is connected, and removes readiness on peer loss. A complete config or resolved peer-IP change replaces that exact child without restarting the Pod or application containers; removing all segments stops it. Parent-death signaling prevents an orphan after an abrupt helper exit. This process boundary contains no Node kind, vendor, device runtime, or Kubernetes credential.

Host Links necessarily create host-namespace state that can outlive a force-deleted Pod. A small privileged DaemonSet owns only host endpoints and orphan cleanup. The Pod helper requests operations by immutable Link/Node/Pod UID, and the daemon labels/aliases every host object with those identities. It does not launch containers, pull images, or implement kind behavior. Link finalizers handle normal deletion; the daemon reconstructs desired host state from Kubernetes and sweeps UID-orphaned objects after crashes or rescheduling.

This is simpler for the current Kubernetes floor than installing or rewriting the cluster's primary CNI, while still giving strict pre-application ordering. If conformance proves a kind cannot tolerate the standard Pod sandbox plus planned management/interface setup, that is an architecture failure to fix deliberately; it is not permission to add a nested fallback. A c9s CNI would then replace this decision through a reviewed design revision.

Alternatives considered:

- A regular sidecar races application startup.
- A one-shot init container cannot reconcile live Link changes.
- A cluster-wide chained CNI affects every worker's primary networking and is unnecessary unless the complete matrix proves Pod-sandbox preparation insufficient.
- A node agent for all links increases host privilege and makes ordinary Pod cleanup less automatic.

### 5a. Revision: fabric transports terminate in the worker host namespace

Direct SR Linux dataplane evidence invalidated the in-Pod transport assumption of decision 5:
kinds that take ownership of the Pod's primary interface (SR Linux renames it and strips its
address) leave the Pod network namespace without underlay routes, so a VTEP or TCP transport
terminated inside that namespace cannot reach its peer even though the device itself keeps
working. This is a generic property of interface-owning kinds, not a vendor defect, so the
transport moved out of the device-owned namespace entirely.

Every cross-Pod Link endpoint is now realized by the node-local host-endpoint daemon as a plain
veth leg into the Pod - exactly the mechanism host Links already use - plus a host-namespace
transport the daemon owns and selects:

- both endpoints on one worker: a tc mirred patch between the two host-side legs, no
  encapsulation at all;
- endpoints on different workers: a VTEP per endpoint (VNI = the Link's allocated tunnel id,
  underlay = worker node addresses) patched to the host-side leg.

The device always sees a plain veth, which every kind tolerates; Pod-IP churn never touches the
device-visible interface; the daemon re-derives desired state from Links, Nodes, and Pods,
labels every object with immutable UID ownership, sweeps orphans, and reports per-Link
transport readiness back to the connectivity helper, which holds cold-start readiness until
every fabric transport converges. Peer moves (rescheduling) converge through the daemon's
periodic sweep without waiting for the owning helper's next request.

`Link.spec.connectivity` values `vxlan` and `slurpeeth` are retained as accepted input and both
map onto this controller-selected realization; the slurpeeth userspace TCP transport and the
in-Pod VXLAN termination are removed from the direct runtime. This is an intentional
compatibility change: the wire semantics (L2 point-to-point, MTU intent, live rewire, cleanup,
rescheduling) are preserved, while the transport flavor becomes a c9s implementation detail the
way the goal's portable-semantics rule prescribes.

### 5b. Revision: management identity is always addressed and reachable in-Pod

Direct SR OS evidence exposed the management-plane half of the same interface-ownership
property: imported post-deploy hooks run application-locally, and upstream packages dial the
node's management address from there (SR OS waits on NETCONF and saves its config through it).
Two generic gaps broke this. First, the controller allocated management addresses only when the
operator declared a management policy, while containerlab always addresses management; in
direct mode the Pod address is the real management plane (SR-SIM adopts the Pod's primary
interface address into its BOF), but it exists only at runtime. Second, a kind that owns the
Pod's primary interface strips the Pod namespace of addresses and routes, so no in-Pod dial to
the Pod's own address can even leave the kernel.

Both are closed kind-opaquely:

- every application container learns the Pod address through the downward API, and every
  in-Pod lifecycle boundary (post-deploy, deploy-endpoints, readiness, save) completes the
  management entry of any logical Node the controller left unaddressed with that address -
  after the immutable input identity is validated, so the completion is a runtime realization,
  never an input change;
- the host-endpoint daemon gives each direct Pod a management loop: one owned veth pair whose
  host side hairpins traffic for the Pod's own address through the worker namespace back into
  the Pod's primary interface, addressed from a worker-local /31 out of 198.18.0.0/16
  (RFC 2544 space). The Pod-side route is a single /32 for the Pod's own address, inert for
  kinds that leave the primary interface alone (the kernel's local route wins) and
  load-bearing the moment a device strips it.

The connectivity helper requests the loop with its Pod identity, the daemon authorizes it
against live Kubernetes state, reports readiness the helper gates on, and sweeps loops whose
Pods are gone. Operator-declared management policies keep precedence; runtime completion adds
entries only where the controller allocated nothing.

The identity must survive the whole pipeline: the plan records one management entry per logical
Node so the package-declared management interface rides into rehydration; CIDR allocations are
split into the bare-address and prefix-length fields packages consume; the deployment-replay
recorder reports the prefix through its network settings (packages refresh their Cfg from
them); and the preparation container records the Pod's address, prefix, and default gateway
while the primary interface is still pristine, because interface-owning devices strip it before
the PostStart boundary runs.

### 5c. Revision: systemd images and runtime-CLI sessions

Arista cEOS conformance exposed two more generic properties of imported packages. First,
systemd-based NOS images mount a fresh tmpfs over `/run` at boot, shadowing anything mounted
below `/var/run`; every application-visible c9s lifecycle mount therefore lives under
`/var/lib/clabernetes`. Second, packages open interactive CLI sessions by spawning their
container runtime's CLI (`docker exec -it <container> <cli>`) and screen-scraping it. The
direct application runtime presents that surface — its runtime name is `docker` and the
lifecycle binary publishes `docker`/`podman` links to itself — through a fail-closed shim that
accepts exactly `exec` against the plan-declared target container and runs the command on its
own pseudo-terminal, application-locally, since the lifecycle boundary already executes inside
that container. The shim behaves as the terminal: terminal-directed side-band sequences
(OSC and capability queries) are stripped from the forwarded stream and echo is disabled, so
screen-scraping callers see only application output; c9s's own processes run terminal-silent
(`TERM=dumb`) at these boundaries while the session command receives a real terminal identity.

### 6. Render portable policy directly and reject Docker-only semantics

The planner/renderer maps OCI and Node intent to Kubernetes fields with explicit rules:

- leave `command` and `args` empty to preserve image defaults; set them only for planned overrides;
- merge Node environment over image defaults according to OCI semantics and map env files through preparation;
- map capabilities, privilege, run-as user, seccomp/AppArmor, sysctls, devices, tmpfs, `/dev/shm`, DNS, resources, image pull policy, and pull secrets to native Pod/container fields;
- use ConfigMap/Secret projected inputs, `emptyDir`, generic ephemeral/PVC volumes, and `subPath` only when their update and permission semantics match the declared bind;
- reject arbitrary host binds, unsupported propagation, unapproved host devices, Docker-only security options, and invalid network modes before creating a workload; and
- execute post-start/user exec through a typed c9s lifecycle runner targeting the direct container, with completion recorded as a condition/event.

VM-backed kinds remain ordinary device application containers running their packaged qemu/vrnetlab entrypoint and receive `/dev/kvm`, tap interfaces, huge pages, privilege, and resources through the plan. c9s does not launch the VM itself. If a VM image internally uses a supervisor, that is part of the network-device image, not a c9s second container runtime.

### 7. Preserve LauncherProfile identity but remove obsolete fields

Keeping the CRD name avoids an otherwise cosmetic API migration; its documented meaning becomes reusable Node workload policy. The final `LauncherProfile` retains exposure, scheduling, primary application-container resources, portable persistence, probes, Kubernetes image-pull Secrets and a default application image-pull policy. Security, privilege, devices, component resources, and other kind-owned container requirements remain imported-plan data; in particular, `privilegedLauncher` is never reinterpreted as device privilege. An explicit Node/containerlab image-pull policy takes precedence over the profile default, which takes precedence over the global Config default.

The breaking alpha migration has the following exact field outcomes. A retained field keeps its path unless a replacement is shown.

| Old field | Final disposition | Required migration |
| --- | --- | --- |
| `LauncherProfile.spec.imagePull.pullSecrets` | Retained as same-namespace Pod `imagePullSecrets` | Ensure each named Secret exists in every workload namespace. |
| `LauncherProfile.spec.imagePull.pullThroughOverride` | Removed; not equivalent to a Kubernetes pull policy | Configure the node runtime mirror/pre-pull behavior; set new `spec.imagePull.policy` only when a Kubernetes `Always`, `IfNotPresent`, or `Never` default is intended. |
| `LauncherProfile.spec.imagePull.insecureRegistries` | Removed | Configure every eligible node runtime; add only the exact controller metadata exception to `Config.spec.imagePull.registryMetadataTrust` when needed. |
| `LauncherProfile.spec.imagePull.dockerDaemonConfig` and `.dockerConfig` | Removed | Use same-namespace Docker-config `pullSecrets`; configure daemon transport/mirrors on cluster nodes. |
| `LauncherProfile.spec.deployment.persistence` | Retained | The PVC stores package-planned persistent device artifacts, not a launcher/containerlab working directory. |
| `LauncherProfile.spec.deployment.privilegedLauncher` | Removed with no automatic replacement | Device privilege comes exclusively from the imported package plan. |
| `LauncherProfile.spec.deployment.launcherImage`, `.launcherImagePullPolicy`, `.launcherLogLevel`, and `.extraEnv` | Removed with no automatic replacement | Manage c9s controller/helper images and logs as release deployment policy; express actual device environment only in supported Node/containerlab input. |
| `LauncherProfile.spec.deployment.containerlabDebug`, `.containerlabTimeout`, and `.containerlabVersion` | Removed with no per-workload replacement | Upgrade the pinned c9s containerlab module for new package behavior; use controller diagnostics for c9s logging. |
| `LauncherProfile.spec.mgmt.ipv4-subnet`, `.ipv4-range`, `.ipv4-gw`, `.ipv6-subnet`, `.ipv6-range`, and `.ipv6-gw` | Retained | They define the direct management overlay and allocation policy. |
| `LauncherProfile.spec.mgmt.network`, `.mtu`, and `.external-access` | Removed | Pods have no Docker management network; use planned management semantics, Link MTU where applicable, and Kubernetes Services for exposure. |
| `Config.spec.imagePull.registryMetadataTrust` | Retained, global, and controller-only | Configure matching node-runtime trust separately. Profiles cannot weaken it. |
| `Config.spec.imagePull.pullThroughOverride`, `.criSockOverride`, `.criKindOverride`, `.criHostsDir`, `.dockerDaemonConfig`, and `.dockerConfig` | Removed | Use cluster node-runtime administration, global/profile pull Secrets, and the new Kubernetes pull-policy default. |
| `Config.spec.deployment.resourcesByContainerlabKind` | Removed | Imported plans own kind requirements; use generic `resourcesDefault` or an explicit LauncherProfile override without c9s kind matching. |
| `Config.spec.deployment.privilegedLauncher`, `.containerlabDebug`, `.containerlabTimeout`, `.containerlabVersion`, `.launcherImage`, `.launcherImagePullPolicy`, `.launcherLogLevel`, and `.extraEnv` | Removed | No launcher exists in the final runtime and none of these values are retargeted to device containers. |
| `Config.spec.deployment.resourcesDefault` and `.nodeSelectorsByImage` | Retained as generic direct-workload defaults | Conflicting selectors across a grouped/component workload fail preflight. |

`Config.spec.imagePull.policy` and `Config.spec.imagePull.pullSecrets` are added as global defaults corresponding to `LauncherProfile.spec.imagePull.policy` and `.pullSecrets`. Explicit empty profile pull Secrets clear the global list. `registryMetadataTrust` is deliberately outside this inheritance chain because it controls controller transport trust, not Pod pull policy. Legacy Topology fields that mirror the removed Config or LauncherProfile fields have the same disposition and are included in the same preflight; compilation never emits them into direct resources.

Before replacing the CRDs, a read-only upgrade preflight inspects the stored unstructured `Config`, `LauncherProfile`, and `Topology` objects under the old schema. It reports every *present* removed path, including explicit false, zero, or empty values, as a stable diagnostic containing object kind, namespace/name, JSON path, disposition, and replacement guidance. Results are sorted by object identity and path, never include field values or Secret bytes, and produce a non-zero exit when any incompatible path is present. The tool does not rewrite objects because several old fields have no equivalent and launcher policy must not be silently retargeted to device containers. After the cut, structural schemas reject removed or unknown fields. Existing `launcherProfileRef` and applied-profile status identity remain valid.

Generated clients, OpenAPI, Helm CRDs, values, examples, and status vocabulary change together at this boundary. The migration release remains rollback-compatible only before that cut; rollback after CRD replacement requires reinstalling the prior release with its matching CRDs and restoring backed-up resources.

### 8. Replace launcher observations with Pod, helper, and plan observations

The Node controller watches owned Pods and reads application/init container status directly. Preparation, connectivity, lifecycle action, and application probe results use standard Pod readiness gates where possible and compact c9s conditions/events otherwise. The status mapping is plan-derived and UID/digest checked, so a stale Pod cannot update a replacement Node.

`kubectl logs` and `kubectl exec` target a deterministic application container (or require `-c` for components). c9s APIs for save, packet capture, and exec resolve Node-to-container/interface from the applied plan and use Kubernetes exec or helper RPC, never Docker/containerlab. Kubernetes Events name the logical Node, component, plan digest, and failing typed action without copying secret material.

### 9. Treat the matrix as work inventory, not documentation metadata

Each canonical kind progresses independently through `inventoried`, `planned`, `rendered`, `booted`, and `compatible`; aliases inherit only after their resolution is verified. `compatible` requires all applicable automated or recorded scenarios and is invalidated by a baseline, planner, renderer, or relevant helper change. Generally obtainable images run in automation. Restricted images use a documented harness that emits a signed/checksummed evidence record containing image digest, planner/runtime revisions, scenario results, date, and environment facts; rendering alone cannot advance status.

The multi-worker suite uses task-scoped namespaces and labels and covers traffic, live changes, forced Pod deletion, rescheduling, controller/helper restarts, partial updates, all Link flavors, and orphan cleanup. Direct manifests, the Topology compiler, and clabverter compare normalized plan digests before their runtime observations are compared. After evidence is recorded, iterative runs remove their task-scoped completed or failed planning Pods, Jobs, stale workloads, and superseded diagnostics. Final acceptance removes every task-created namespace and cluster resource unless a specific retained diagnostic is reported. Cleanup selects resources by task namespace, release, owner, and labels and never sweeps unrelated completed Pods or user workloads.

## Risks / Trade-offs

- **[Imported containerlab APIs mix intent with side effects and may drift]** → Execute them only in locked-down planning/preparation workers against controlled generic recorders/workspaces, audit escape attempts, and fail on an unrepresentable operation class rather than adding a kind-specific adapter.
- **[Restartable connectivity init containers add privilege to every device Pod]** → Give the helper a fixed c9s image, minimal capabilities where tests permit, no device-image launch APIs or sockets, scoped RBAC/Link watches, read-only plan input, and UID validation. Track a CNI migration only if conformance demonstrates a hard need.
- **[OCI metadata access can differ from kubelet registry access]** → Separate metadata trust/auth diagnostics from pull status, support Kubernetes pull Secrets and explicit CA/HTTP policy, compare resolved digest with kubelet `imageID`, and fail closed on mismatch.
- **[Static management addresses can conflict with Pod networking]** → Keep the Kubernetes Pod IP intact, allocate/validate management addresses centrally, use a distinct planned overlay/interface behavior, and reject overlaps or unreachable modes before boot.
- **[Large component plans or generated configs can exceed API limits]** → Store only normalized non-secret plans in immutable ConfigMaps, keep payloads in referenced objects/PVCs, enforce size ceilings, and split typed artifacts by digest when necessary.
- **[Commercial evidence can become stale without CI access]** → Encode invalidation inputs in evidence records and make the release gate treat stale/missing evidence as incompatible.
- **[Removing alpha Docker fields is disruptive]** → Provide preflight reporting, a temporary feature-gated migration release, explicit replacements, and a documented no-in-place-fallback boundary.
- **[Device images may assume Docker-specific behavior not expressible by Kubernetes]** → Make the incompatibility visible in planning and define one tested portable c9s adapter/runtime semantic; never weaken or ignore it silently.

## Migration Plan

1. Introduce the exact 0.78.0 registry inventory, plan schema, conformance matrix, generator, and release gates without changing the current runtime.
2. Pin and import the unmodified containerlab module, land the c9s-owned plan schema and generic recorder/workspace adapter, and drive every live kind and alias through registry-parameterized conformance. Add OCI metadata resolution and plan validation.
3. Add a temporary explicit `nested|direct` development mode. Implement the direct Deployment renderer, preparation init, restartable connectivity init-sidecar, and host cleanup daemon. There is no automatic fallback: a direct-plan failure stays failed.
4. Expand generic operation support until native, VM-backed, component-based, and restricted-image families all pass without kind-specific c9s logic. Add direct operations and entry-path equivalence while retaining nested mode solely as an A/B oracle in non-production conformance.
5. Run the full multi-worker and vendor evidence matrix. Make direct mode the default only after every baseline entry and behavior is compatible.
6. In one breaking API/runtime cut, remove nested mode, launcher code/image/RBAC, Docker/containerlab layers, ImageRequest/import paths, obsolete Config/LauncherProfile fields, and legacy status vocabulary. Regenerate and inspect every API artifact and update documentation, examples, release notes, and preflight migration tooling.
7. Validate unit, race, lint, generated artifacts, images, production manifests, all generally obtainable images, recorded restricted-image scenarios, and the complete authorized multi-worker suite. Record required evidence, then remove task-scoped completed/failed Pods, Jobs, workloads, namespaces, and diagnostics; report any intentional retention. Only this validated and cleaned state can close the change.

Before step 6, rollback selects nested mode and recreates affected workloads. After the breaking release, rollback means reinstalling the prior c9s release and its compatible CRDs after backing up user resources; the final direct runtime deliberately contains no nested emergency path.
