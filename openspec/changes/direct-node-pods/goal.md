# Goal: Run Containerlab Nodes as Direct Kubernetes Containers

## Outcome

Replace the nested launcher runtime with a Kubernetes-native runtime in which every Containerlab
network-device image runs as a first-class application container in a c9s-managed Pod. Device Pods
MUST NOT run an inner Docker daemon, launch nested device containers, or require the containerlab
executable at runtime.

The completed runtime MUST preserve full multi-vendor compatibility. It is not sufficient to prove
the architecture with only `linux`, Nokia SR Linux, or another small subset of kinds. Every node kind
and supported topology behavior that the current nested runtime can realize using the supported
containerlab baseline MUST have an equivalent direct-Pod realization, or the project MUST explicitly
remove that behavior from its public compatibility contract in a separately reviewed breaking
change. The direct runtime MUST NOT silently fall back to nested Docker or containerlab.

Thin c9s-owned init containers, sidecars, CNI components, or node agents are allowed when they are
needed to prepare configuration, create and maintain links, or report status. These helpers MUST NOT
act as a second container runtime. The network-device container remains visible to and managed by
the kubelet through the cluster CRI, with its image, lifecycle, logs, resource usage, and status
represented directly in Kubernetes.

## Compatibility Baseline

### Non-negotiable dependency-update invariant

Containerlab is the sole source of node-kind and vendor knowledge. c9s MUST treat kind and vendor
identifiers as opaque values and MUST obtain all kind-specific behavior from the live registry and
hooks of the unmodified imported `github.com/srl-labs/containerlab` package.

When containerlab introduces a new kind, updating only the pinned containerlab module version and
its generated dependency lock data MUST make that kind available to c9s. That update MUST NOT
require any c9s source edit, kind entry, alias entry, switch case, allowlist, copied default,
template, fixture, compatibility-matrix row, per-kind test case, or adapter. Tests and compatibility
reports MUST enumerate and parameterize themselves from the imported registry. A dependency update
may expose a missing generic runtime capability, but such a failure MUST be expressed in terms of
that capability and fixed once at the generic package-to-Kubernetes boundary—not by recognizing the
new kind or vendor. This invariant also means the goal MUST NOT require any containerlab repository
change, fork, or c9s-specific hook.

The compatibility baseline is the complete set of node kinds and node/topology behaviors supported
by the intentionally pinned `github.com/srl-labs/containerlab` Go module version that c9s declares
as supported. c9s MUST consume that dependency unmodified: this goal does not require patches,
forks, or companion changes in the containerlab repository. A versioned compatibility matrix MUST
be generated from the imported registry and used as an exhaustive test inventory. It MUST NOT be a
second hand-maintained kind catalog or implementation dispatch table. Updating the pinned
containerlab dependency MUST automatically discover and support newly registered kinds without any
kind-specific c9s source, fixture, matrix, switch, allowlist, or adapter change. Changing the module
version and its dependency lock data MUST be sufficient to import a new kind. Verification may fail
only when imported behavior reaches a genuinely unsupported generic Kubernetes capability; it MUST
NOT fail merely because a kind name, alias, vendor, component layout, or vendor-specific behavior is
new to c9s.

Compatibility includes, where supported by the corresponding kind:

- vendor-specific initialization, image selection, entrypoint, command, environment, sysctls,
  capabilities, privilege, security profiles, devices, tmpfs, shared memory, and resource policy;
- startup configuration generation and enforcement, config-engine variables, licenses,
  certificates, URL and ConfigMap payloads, bind semantics, persistence, and post-start commands;
- device-specific pre-deploy, post-deploy, readiness, interface naming, interface fixups, and
  management-plane behavior;
- ordinary Nodes, `network-mode: container:<primary>` launcher groups, and component-based or
  distributed chassis that expand one logical Node into multiple cooperating containers;
- VXLAN, slurpeeth, same-Pod, loopback, and `host` Link realizations, including MTU, live rewiring,
  cleanup, Pod recreation, and rescheduling;
- Kubernetes-native image pulling, private registries, pull secrets, and an explicit replacement
  for any current per-launcher insecure-registry or pull-through behavior that cannot map directly
  to a Pod;
- management IPv4/IPv6 intent, DNS, exposure Services, static-address behavior, exec, logs, save,
  events, packet capture, readiness, and status reporting;
- direct Node/Link manifests, Topology compilation, and clabverter output.

Where Docker and Kubernetes have genuinely different semantics, the project MUST define one
portable c9s behavior, document the difference, reject unrepresentable input before workload
creation, and add a conformance test. Ignoring a field or accepting a partially working kind is not
compatibility.

## Target Architecture

The existing bounded control-plane model remains authoritative:

- `Node` describes one logical network node and its payload;
- `Link` describes one wire and owns its connectivity flavor;
- `LauncherProfile` (or a deliberately migrated replacement) owns Kubernetes realization policy;
- `Topology` remains an optional compiler into direct primitive resources.

The Node controller renders a workload whose application containers are the actual device
containers. A c9s preparation component may generate and stage kind-specific artifacts before the
device starts. A c9s connectivity component may start before the device, create the initial
interfaces, and continue reconciling Link changes. If sharing the Pod network namespace cannot
provide correct isolation or ordering for every supported kind, c9s may use a CNI plugin and a
privileged node agent instead. The final design MUST choose the simplest architecture that passes
the complete compatibility matrix; it MUST NOT preserve the nested runtime merely to avoid solving
kind or networking behavior.

Containerlab's vendor knowledge MUST be consumed exclusively from its unmodified, pinned Go module.
c9s MUST NOT reproduce containerlab kind defaults, templates, files, commands, environment,
security policy, devices, lifecycle hooks, component layouts, interface behavior, save behavior, or
readiness logic in kind-named Go code or data. The c9s integration MUST invoke the imported registry
and node behavior through generic recording/runtime/filesystem boundaries and translate only generic
operations into the runtime-neutral plan and Kubernetes realization. Controlled scratch storage and
recorders may supply explicit image, payload, certificate, management, interface, and lifecycle
inputs, capture generated artifacts, and prevent uncontrolled image, container, host, filesystem,
or network mutation. The generic boundary MUST cover containers, files, mounts, security, lifecycle
actions, components, and interfaces without branching on kind or vendor identity.

A newly registered containerlab kind MUST therefore flow through the same imported hooks and generic
operation translation as every existing kind. c9s conformance MUST enumerate kinds and aliases from
the live imported registry and generate or parameterize coverage from that registry; it MUST NOT
require a developer to add the kind to a c9s list or author a matching c9s kind fixture before it can
work. The only acceptable c9s work after a normal containerlab update is updating the pinned module
version and generated dependency data. If containerlab evolves the generic interface itself, c9s may
need a corresponding generic integration change, but never a vendor- or kind-specific port.

Nested-runtime repair behavior MUST NOT be carried into direct Pods merely because the launcher
previously required it. In particular, c9s MUST first run SR Linux directly and prove management,
DNS, Service, and external reachability using the container image and imported containerlab behavior
as-is. If that direct path works, the old launcher-specific `srbase-mgmt`, `mgmt0`, `mgmt0.0`, and
`mgmt0-0` forwarding repair MUST be deleted rather than ported. If direct evidence exposes a gap,
the implementation MUST describe and solve the missing generic Kubernetes/runtime capability
without recognizing SR Linux or copying its internal namespace/interface names. The OpenSpec
artifacts MUST be corrected to express this evidence-first requirement before implementing the
current SR Linux-specific forwarding task.

## Required Workstreams

1. Inventory the supported containerlab node-kind registry and current c9s behavior, then create the
   exhaustive compatibility matrix and fixtures.
2. Import an intentionally pinned, unmodified containerlab Go module; define a c9s-owned
   runtime-neutral device plan and generic recording/runtime/filesystem adapter that invokes
   containerlab behavior without any c9s kind catalog or vendor branches; and decide which generic
   realization logic belongs in the c9s controller, an init container, a long-running agent, or
   CNI/node-level components.
3. Render direct device Pods, including grouped and component-based Nodes, with Kubernetes-native
   image pulling, resources, security, storage, DNS, logging, exec, and lifecycle observation.
4. Replace topology materialization, inner Docker startup, nested-container discovery, Docker image
   import, Docker health inspection, and containerlab tool invocations with direct implementations.
5. Realize every Link flavor with correct startup ordering, namespace isolation, live reconciliation,
   deletion, restart, and cross-worker behavior.
6. Preserve or deliberately migrate LauncherProfile, Config, Node status, exposure, persistence,
   and management addressing without silent semantic loss.
7. Validate every imported vendor kind, including component/chassis kinds and VM-backed images,
   through registry-driven conformance while fixing only generic operation support, never by porting
   kind knowledge into c9s. Validate direct SR Linux management reachability before deciding that
   any nested-launcher forwarding repair remains necessary.
8. Make direct Pods the only runtime, remove the nested launcher implementation and obsolete API
   fields/controllers, and update documentation, examples, release notes, and upgrade guidance.

Temporary feature gates or a `nested|direct` development mode are allowed during migration. They
are not part of the completed outcome: the goal is complete only after direct mode is the default
and the nested runtime has been removed.

## Acceptance Criteria

This goal is complete only when all of the following are true:

- No c9s device Pod contains dockerd, a Docker socket used to launch devices, the containerlab
  executable, or a nested network-device container.
- `kubectl get pods`, `kubectl logs`, `kubectl exec`, Pod container status, and Kubernetes resource
  accounting operate on the actual network-device container.
- An exhaustive registry-driven check proves that every imported kind has a direct runtime plan and
  conformance coverage without a c9s kind list; a newly registered kind works after only the
  containerlab module/dependency update, unless it exposes a missing generic capability that is
  reported independently of kind identity.
- Repository-wide verification proves c9s contains no kind-named planning switch, copied vendor
  default, vendor template, per-kind allowlist, or manually maintained registry mirror used to make
  planning work.
- Every generally obtainable test image has automated deployment coverage. Commercial or
  license-gated images have repeatable, documented conformance scenarios and recorded validation
  evidence before their kind is marked compatible.
- Multi-worker end-to-end suites cover link traffic, live Link changes, Pod deletion, Pod
  rescheduling, controller restart, partial topology updates, cleanup, and failure recovery.
- Representative native-container, VM-backed, and component-based vendors pass real boot and
  dataplane tests, including at least Linux, Nokia SR Linux, Nokia SR OS/SR-SIM, Arista cEOS, Cisco
  XR/vrnetlab, and Juniper VM-based families when their required images and licenses are available.
- VXLAN, slurpeeth, same-Pod/grouped, loopback, and host links have positive and cleanup tests.
- Startup configuration, licenses, persistence, management reachability, DNS, Services, probes,
  exec, logs, save, events, and packet capture have direct-runtime tests.
- Direct SR Linux management, DNS, Service, and external reachability are proven before any runtime
  repair is introduced; no nested-launcher forwarding behavior or internal SR Linux name is retained
  unless a generic, kind-opaque capability is demonstrated necessary by direct-Pod evidence.
- Direct primitive resources, Topology compilation, and clabverter produce equivalent running labs.
- The nested runtime code, image layers, Docker/containerlab configuration, image-import path, and
  obsolete compatibility fields are removed, with generated artifacts regenerated and inspected.
- User-facing migration and compatibility documentation identifies every intentional semantic
  difference and contains no unsupported claim of parity.

## Validation Authority and Expected Checks

Work on this goal is explicitly authorized to use the repository's real local integration
environment, including `make try-c9s`, `make test-e2e-local`, and the corresponding containerlab,
Kubernetes, container-runtime, image-build, deploy, destroy, and diagnostic commands needed to
prove direct execution and multi-worker connectivity. Tests may create, mutate, and clean up local
c9s test clusters and labs. They MUST use task-scoped resources, preserve unrelated clusters and
user workloads, and report cleanup or any retained diagnostic state. During iterative validation,
the implementation MUST regularly remove task-scoped completed or failed planning Pods, Jobs, stale
test workloads, and superseded diagnostic resources once their evidence is recorded. Final cleanup
MUST remove every resource created for the goal, including completed Pods and retained test
namespaces, unless a specific diagnostic resource is intentionally retained and reported. Cleanup
MUST be selected by task namespace, release, owner, and labels and MUST NOT delete unrelated
completed Pods or user resources.

Validation should progress from narrow unit and renderer tests to kind-plan conformance, focused
integration tests, `make test`, `make test-race`, `make lint`, `make verify-generated`, image builds,
and finally the authorized cluster-level suites. Expensive tests may be iterated selectively during
development, but the full relevant matrix is required before this goal can be declared complete.

## Explicit Non-Goals

- Shipping a direct runtime that supports only the easiest or currently available open images.
- Keeping nested containerlab as an automatic fallback for unported kinds.
- Claiming compatibility from manifest rendering alone without boot and dataplane validation.
- Requiring users to manually patch generated Pods to make a supported kind work.
- Preserving obsolete Docker-specific API fields when a clearer, reviewed migration is available.
- Patching, forking, or otherwise changing the containerlab repository to provide the c9s planning
  boundary.
- Porting a nested-launcher SR Linux forwarding repair without first proving that the direct
  container and imported package behavior leave the same generic reachability gap.
- Re-implementing or registering containerlab kind knowledge in c9s, including per-kind planners,
  copied defaults/templates, vendor switches, allowlists, and manually added fixtures required for a
  new kind to function.
