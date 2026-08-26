## 1. Pinned Module and Live Registry

- [x] 1.1 Pin the exact containerlab 0.78.0 module identity in `go.mod` and `go.sum` without committing a kind-name catalog.
- [x] 1.2 Construct the authoritative imported registry and expose deterministic canonical/alias data to planning and parameterized tests.
- [x] 1.3 Add tests that construct and validate the live imported registry without comparing it to a c9s-maintained list, so added kinds and aliases participate automatically.
- [x] 1.4 Record the linked module version and live-registry digest in device plans and reject replaced or unversioned dependencies.
- [x] 1.5 Add source verification that prevents kind-named dispatch or concrete kind implementation imports from entering the generic direct runtime.

## 2. Imported Containerlab Planning Adapter

- [x] 2.1 Add an intentionally pinned `github.com/srl-labs/containerlab` Go module dependency, construct its authoritative kind registry through the generic adapter, and forbid a local replacement, fork, or required sibling-repository artifact.
- [x] 2.2 Define the c9s-owned versioned runtime-neutral planning input, plan, typed action, error, normalization, and serialization API.
- [x] 2.3 Implement a c9s recorder/adapter over exported containerlab registry, kind, and container-configuration APIs using explicit OCI and topology inputs.
- [x] 2.4 Run adapter evaluation in a locked-down, deadline-bounded planning Pod and add escape tests proving it can perform no real image pull, container launch, unsupplied runtime inspection, implicit host access, privileged namespace mutation, or host/network mutation.
- [x] 2.5 Remove every c9s kind allowlist, vendor switch, copied default/template, canonical-kind dispatch map, and manual fixture registration from planning; add repository-wide negative verification for their return.
- [x] 2.6 Execute imported `Init`, preparation, deployment, component, lifecycle, readiness, interface, and save hooks in phase-appropriate locked-down planning/preparation/lifecycle workers against controlled generic runtime/filesystem boundaries using only explicit inputs.
- [x] 2.7 Translate recorded container, file, mount, security, device, exec, namespace, component, management, interface, readiness, and save operation types into deterministic runtime-neutral plans without inspecting kind identity.
- [x] 2.8 Drive every canonical kind and alias discovered from the live imported registry through parameterized planning scenarios and prove no c9s-maintained kind list or fixture registration participates.
- [x] 2.9 Add a dependency-bump gate proving a synthetic newly registered imported kind flows through planning without c9s source changes, while genuinely unrepresentable generic operations fail with structured diagnostics independent of kind identity.

## 3. OCI Image Metadata and Planning Reconciliation

- [x] 3.1 Implement a registry metadata resolver for manifests, indexes, config blobs, platform selection, immutable digest resolution, and normalized OCI config without downloading layers.
- [x] 3.2 Resolve Kubernetes Docker-config pull Secrets, private registry authentication, explicit CA bundles, and explicitly allowed insecure metadata endpoints with redacted diagnostics.
- [x] 3.3 Add resolver caching keyed by reference, credentials, platform, and trust policy with bounded lifetime and digest validation.
- [x] 3.4 Feed explicit image config, payload identity, management allocation, and accepted interface inventory into the planner and surface structured missing/unrepresentable-input conditions.
- [x] 3.5 Compare the planned digest with kubelet `imageID` and fail readiness on a material mismatch rather than silently running a different image.
- [x] 3.6 Add controller tests proving planning failure creates no new workload and cannot mutate the last successfully applied workload.

## 4. Direct Workload Renderer

- [x] 4.1 Add a temporary explicit `nested|direct` development setting with direct failure closed and no automatic fallback.
- [x] 4.2 Add immutable owner-referenced plan ConfigMaps, plan/input digests, size ceilings, garbage collection, and tests that exclude secret bytes.
- [x] 4.3 Render a preparation init container and typed plan/payload volumes that stage and verify generated, ConfigMap, Secret, URL, certificate, license, and persistence artifacts.
- [x] 4.4 Render one direct application container for a standalone Node while preserving OCI defaults unless the plan overrides them.
- [x] 4.5 Render grouped logical Nodes as separate application containers in the primary Node's Pod with deterministic names and conflict validation.
- [x] 4.6 Render component/distributed-chassis plans as directly visible application containers with validated namespace ownership and readiness mapping.
- [x] 4.7 Map plan/profile image pull policy, pull Secrets, environment, user, working directory, ports, resources, devices, capabilities, privilege, seccomp/AppArmor, sysctls, tmpfs, shared memory, and DNS to Kubernetes fields.
- [x] 4.8 Map bind and persistence semantics to projected, ephemeral, and PVC volumes and reject unsupported host/path/propagation semantics before workload creation.
- [x] 4.9 Preserve Recreate ownership, profile/scheduling policy, labels, annotations, Services, PVC adoption, and independent group lifecycle in direct mode.
- [x] 4.10 Add renderer/conformance tests asserting device images are regular containers and no Docker socket, dockerd, containerlab executable, nested-device launcher, or image-import mount is present.

## 5. Direct Preparation, Status, and Operations

- [x] 5.1 Implement the preparation binary's typed file generation, URL verification, permissions, ownership, secret-safe logging, and idempotency.
- [x] 5.2 Translate OCI healthchecks and plan/profile application checks into startup/readiness behavior with exact startup allowance semantics.
- [x] 5.3 Map Pod init/application container status and readiness gates back to each logical Node using applied plan identity and UID checks.
- [x] 5.4 Replace launcher status markers and Docker health inspection with bounded Kubernetes-native Node conditions, observations, and events.
- [x] 5.5 Implement direct-container exec and logs selection for standalone, grouped, and component Nodes.
- [x] 5.6 Implement typed post-start commands, save, events, and packet capture against direct containers/interfaces with authorization and audit tests.
- [x] 5.7 Boot SR Linux directly with the unmodified image and imported package behavior and prove management addressing, DNS, Service, and external reachability before adding any repair; delete the nested forwarding assumption when those observations pass, or specify and implement only the demonstrated kind-opaque generic capability when they fail.

## 6. Direct Connectivity

- [x] 6.1 Render the privileged restartable connectivity init-sidecar with cold-start plan input, scoped RBAC, startup gating, and no runtime/image launch access.
- [x] 6.2 Refactor common endpoint and tunnel logic for direct Pod namespaces with stable Node/Pod/Link UID ownership.
- [x] 6.3 Implement same-Pod/grouped and loopback Links with interface-name, MTU, conflict, live-change, and cleanup tests.
- [x] 6.4 Implement direct VXLAN Links across same and different workers with current peer discovery, allocation, traffic, rewire, and cleanup tests.
- [x] 6.5 Implement direct slurpeeth Links across same and different workers with process ownership, restart, traffic, rewire, and cleanup tests.
- [x] 6.6 Implement the minimal host-endpoint DaemonSet/RPC with immutable UID requests, host object ownership metadata, finalizers, and orphan sweeping.
- [x] 6.7 Implement host Links with collision rejection, traffic, normal deletion, force deletion, Pod rescheduling, and unrelated-host-state safety tests.
- [x] 6.8 Apply kind plan link modes so live, restart, and recreate changes perform exactly the declared lifecycle action.
- [x] 6.9 Remove link-change Pod-template rollouts for live-capable plans while retaining complete cold-start interface digests.
- [x] 6.10 Add controller/helper restart, stale Pod, Node name reuse, partial failure, and idempotent recovery tests for every Link flavor.

## 7. Management, Services, and Cluster Image Policy

- [x] 7.1 Implement unique IPv4/IPv6 management allocation and direct management-overlay/interface realization without replacing the Kubernetes Pod transport address.
- [x] 7.2 Add management route, DNS, certificate SAN, Service, static address, and dual-stack reachability tests for representative kind families.
- [x] 7.3 Make exposure Services target direct device Pods/ports and remove Docker port publication and proxy assumptions.
- [x] 7.4 Replace per-launcher insecure registry and pull-through behavior with Kubernetes `imagePullSecrets`, documented cluster-runtime registry configuration, and explicit controller metadata trust policy.
- [x] 7.5 Add preflight diagnostics for unsupported registry, static-address, management overlap, host device, and security-policy inputs.

## 8. API and Controller Migration

- [x] 8.1 Define the breaking alpha API field migration for LauncherProfile and Config, including portable replacements and removed-field preflight reporting.
- [x] 8.2 Remove launcher image, log level, extra env, privilege, containerlab debug/timeout/version, Docker config, insecure registry, pull-through, CRI socket/kind/hosts, by-kind resource defaults, and Docker-only management fields from API sources.
- [x] 8.3 Update Node/Link status vocabulary and printer columns for plan, direct containers, preparation, connectivity, and applied profile identity without storing full plans or user intent.
- [x] 8.4 Remove or replace ImageRequest and all controller/image-import flows once direct Kubernetes pulling covers every matrix scenario.
- [x] 8.5 Regenerate deepcopy clients, OpenAPI, CRDs, assets, chart schemas, and fixtures; inspect and test every generated delta.
- [x] 8.6 Add upgrade preflight and migration tests proving obsolete fields are reported and never silently reinterpreted.

## 9. Compiler and External Entry Paths

- [x] 9.1 Make Topology compilation reject every unrecognized or unrepresentable Containerlab field with sorted structured diagnostics and remove lossy warning-mode output.
- [x] 9.2 Preserve all plan-relevant defaults, kinds, payloads, labels, ports, management, grouping, components, and connectivity in emitted primitives.
- [x] 9.3 Update direct manifest generation to use identical validation and prove normalized plan equivalence with in-cluster compilation.
- [x] 9.4 Update clabverter output and tests to produce only directly representable resources or fail before output.

## 10. Kind and Behavior Conformance

- [x] 10.1 Add automated direct boot, management, dataplane, readiness, and cleanup coverage for every generally obtainable baseline image (linux/SR Linux in `e2e/topology/direct`, SR OS in `e2e/topology/srsim`, cEOS in `e2e/topology/ceos`; VM kinds recorded in evidence pending restricted-image harness images in CI).
- [x] 10.2 Validate Linux and Nokia SR Linux native-container families through real boot, management, DNS, Service, external-reachability, and dataplane tests, running SR Linux first without any nested-launcher forwarding replacement and rejecting kind-specific c9s remediation as evidence.
- [x] 10.3 Validate Nokia SR OS/SR-SIM component behavior, shared namespaces, licensing, management, and dataplane tests.
- [x] 10.4 Validate Arista cEOS configuration, interface fixups, management, and dataplane tests.
- [x] 10.5 Validate a Cisco XR/vrnetlab family with KVM/tap, configuration, management, and dataplane tests.
- [x] 10.6 Validate Juniper VM-based families with components where applicable, KVM/tap, configuration, management, and dataplane tests.
- [x] 10.7 Add repeatable restricted-image harnesses and current recorded evidence for every commercial/license-gated matrix entry before marking it compatible.
- [x] 10.8 Add conformance for startup configs, variables, licenses, certificates, persistence, DNS, Services, probes, exec, logs, save, events, packet capture, and every supported security/storage behavior.

## 11. Remove the Nested Runtime

- [x] 11.1 Make direct mode the default after the complete compatibility gate passes and run an upgrade soak with no fallback.
- [x] 11.2 Remove the temporary mode switch and all nested launcher topology materialization, Docker startup/discovery/health, containerlab invocation/download, image import, proxy, and nested SR Linux repair code.
- [x] 11.3 Remove the launcher binary/image target, Docker/containerlab/nerdctl layers, launcher RBAC/service account, chart values, Make targets, release artifacts, and CI publication.
- [x] 11.4 Remove obsolete constants, utilities, tests, dependencies, generated fields, examples, and documentation made unreachable by nested-runtime deletion.
- [x] 11.5 Add repository-wide negative verification that shipped device workloads and images contain no nested runtime path or obsolete compatibility field.

## 13. Lab Compatibility Hardening

- [x] 13.1 Downgrade cosmetic containerlab constructs (Docker-only management fields, host-pinned ports) from compile errors to accepted-and-ignored warnings with exact diagnostics.
- [x] 13.2 Accept `group` and `topology.groups` with imported inheritance semantics and carry the group name as a Node label.
- [x] 13.3 Wire the remaining schema vocabulary (healthcheck, stages, aliases, credentials, startup-delay, restart-policy, cpu/memory, image-pull-policy, hostname, link-apply-mode) through the vocabulary gate into the existing device-plan support, keeping `runtime`/`auto-remove`/`pid-mode` as documented rejections.
- [x] 13.4 Validate the reference telemetry lab end to end: fabric, traffic through telemetry, gNMI collection, and by-name syslog into Loki.

## 12. Documentation and Full Acceptance

- [x] 12.1 Rewrite architecture, Node, Link, LauncherProfile, management, image, operations, and troubleshooting documentation for direct Pods.
- [x] 12.2 Publish generated per-kind compatibility, intentional Kubernetes semantic differences, required cluster capabilities, restricted-image procedures, release notes, and destructive upgrade/rollback guidance.
- [x] 12.3 Run focused package tests and renderer/fixture checks throughout implementation, recording any environment-dependent skips.
- [x] 12.4 Run `make test`, `make test-race`, `make lint`, `make verify-generated`, `make check-docs`, and all runtime image builds; inspect resulting changes.
- [x] 12.5 Run the authorized task-scoped multi-worker suite covering every Link flavor, traffic, live changes, Pod deletion/rescheduling, controller/helper restart, partial updates, and recovery; after recording evidence, remove task-scoped completed/failed planning Pods, Jobs, stale workloads, and superseded diagnostics by namespace, release, owner, and labels without touching unrelated resources.
- [x] 12.6 Run all available vendor boot/dataplane scenarios and verify current restricted-image evidence; retain diagnostics only when their exact identity and reason are documented, and remove every other task-scoped Pod, Job, workload, namespace, and cluster/lab resource.
- [x] 12.7 Audit every acceptance criterion against current files, generated artifacts, images, matrix records, runtime evidence, and live cluster inventory; prove no unreported task-scoped resource remains before archiving the change.
