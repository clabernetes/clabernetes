## Why

c9s currently runs each network device as a Docker container nested inside a privileged launcher Pod, so Kubernetes cannot directly own the device image, lifecycle, logs, resources, or status. Replacing that launcher with direct device containers requires one exhaustive compatibility contract: a partial runtime for only easy kinds would silently break the multi-vendor behavior c9s inherits from containerlab.

## What Changes

- Add a versioned compatibility inventory generated from the supported containerlab kind registry without creating a second kind dispatch catalog in c9s.
- Add a deterministic, runtime-neutral device plan for kind defaults, application containers, component containers, files, mounts, security, lifecycle actions, management intent, and interface requirements.
- Render network-device images as first-class application containers in c9s-managed Pods, including grouped Nodes and component/distributed-chassis kinds, with Kubernetes-native image pulling, storage, DNS, resources, security, observability, and lifecycle status.
- Replace containerlab topology materialization, nested Docker startup/discovery/health, image import, and runtime containerlab commands with c9s preparation and connectivity components that do not act as a second container runtime.
- Realize same-Pod, VXLAN, slurpeeth, loopback, and host Links directly, including live reconciliation, cleanup, Pod recreation, rescheduling, and multi-worker operation.
- Treat launcher-only runtime repairs as migration hypotheses: first prove the unmodified device image and imported containerlab behavior in a direct Pod, then add only a demonstrated kind-opaque Kubernetes capability rather than porting vendor names or commands.
- Preserve direct Node/Link manifests, Topology compilation, and clabverter output while rejecting any input whose semantics cannot be represented directly.
- **BREAKING** Make the direct runtime the only runtime; remove the launcher image, inner Docker/containerlab paths, Docker-specific registry/image-import behavior, and obsolete API/configuration fields after their replacements are complete.
- Document the supported containerlab baseline, per-kind conformance state, portable Kubernetes semantics, intentional differences, migration, and upgrade behavior.

## Capabilities

### New Capabilities

- `device-planning`: Defines the pinned containerlab compatibility baseline, live imported-registry inventory, deterministic runtime-neutral device plans, registry-parameterized conformance, and a version-bump gate that requires no c9s kind port.
- `direct-device-runtime`: Defines direct application-container Pods, grouped and component Nodes, preparation/lifecycle helpers, Kubernetes-native image and storage behavior, and device observability.
- `direct-connectivity`: Defines direct realization and reconciliation of every supported Link flavor across same-Pod and multi-worker placements.
- `direct-runtime-conformance`: Defines the boot, dataplane, lifecycle, recovery, integration, and licensed-vendor evidence required before a kind or runtime behavior is compatible.

### Modified Capabilities

- `node-lifecycle`: Replaces launcher and nested-container materialization/readiness with direct device plans, containers, and Kubernetes lifecycle observation.
- `link-lifecycle`: Replaces launcher-scoped tunnel allocation and observation with direct Pod/network connectivity reconciliation and cleanup.
- `launcher-profiles`: Migrates launcher realization policy to direct Pod realization policy and removes containerlab-version, nested-CRI, and pull-through-launcher behavior.
- `topology-resource`: Requires compilation and direct manifest generation to preserve all representable direct-runtime intent and fail on semantic loss.
- `runtime-dns-forwarding`: Retires the nested SR Linux forwarding repair as an assumed requirement, proves direct management and DNS reachability using the unmodified image and imported package behavior, and permits remediation only for an evidenced generic runtime capability.

## Impact

- API sources in `apis/v1alpha1`, their generated clients/OpenAPI/CRDs, and migration compatibility for `Node`, `Link`, `LauncherProfile`, `Config`, and status fields.
- Node, Link, Topology, ImageRequest, and manager controllers; launcher logic is replaced and ultimately removed.
- Runtime images, Helm values/RBAC, helper binaries, CNI or node-agent deployment where the compatibility matrix proves they are necessary, and removal of Docker/containerlab layers.
- Containerlab integration and kind knowledge: c9s imports an unmodified, intentionally pinned containerlab Go module and invokes its registry and node hooks through generic recording/runtime/filesystem boundaries. Containerlab exclusively owns kind knowledge; c9s contains no per-kind planner, defaults, templates, allowlist, fixture registration, or vendor switch. A normal module bump automatically admits new registered kinds without another c9s source change.
- Unit, renderer, generated-artifact, image, multi-worker e2e, vendor boot/dataplane, failure-recovery, topology, and clabverter test suites.
- Documentation, compatibility matrix, examples, release notes, migration guide, and explicit handling of commercial or license-gated validation evidence.
