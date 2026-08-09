## 1. API Types and Generated Artifacts

- [x] 1.1 Rename the NodeProfile API type, registration, clients, and CRD to LauncherProfile
- [x] 1.2 Remove selector, priority, connectivity, and per-node payload fields from LauncherProfile while retaining management-network compatibility
- [x] 1.3 Add optional same-namespace `launcherProfileRef` and ConfigMap-backed payload attachments to Node spec
- [x] 1.4 Add standard conditions and applied LauncherProfile name/UID/generation fields to Node status
- [x] 1.5 Add validated, defaulted `vxlan`/`slurpeeth` connectivity intent to Link spec
- [x] 1.6 Regenerate deepcopy code, CRDs, OpenAPI documents, typed/fake clients, and UI API types

## 2. LauncherProfile Resolution

- [x] 2.1 Replace selector/priority profile matching with Config defaults plus one explicitly referenced LauncherProfile
- [x] 2.2 Preserve unset versus explicit false, empty, and zero semantics for every profile override field
- [x] 2.3 Fail closed and set `LauncherProfileResolved=False` when an explicit profile reference is missing
- [x] 2.4 Record the successfully applied LauncherProfile identity and generation in Node status
- [x] 2.5 Add a Node field index for `launcherProfileRef` and enqueue only referencing launcher groups on profile events
- [x] 2.6 Enforce primary-profile inheritance and reject conflicting profile references in grouped Nodes
- [x] 2.7 Add unit tests for default-only, explicit, missing, deleted, updated, and grouped profile behavior

## 3. Node Payload and Lifecycle

- [x] 3.1 Move ConfigMap-backed per-node file attachment rendering from profiles to Node payload reconciliation
- [x] 3.2 Ensure URL- and ConfigMap-backed payloads are materialized for standalone and grouped Nodes
- [x] 3.3 Verify direct Nodes reconcile deployments, services, PVCs, exposure allocations, probes, and status without a Topology
- [x] 3.4 Add tests for independent Node create, update, delete, grouping changes, and unrelated-resource isolation

## 4. Link Lifecycle and Connectivity

- [x] 4.1 Resolve connectivity exclusively from each Link and remove launcher/profile-wide connectivity resolution
- [x] 4.2 Update Link validation and status allocation for default VXLAN, explicit slurpeeth, local, and host Links
- [x] 4.3 Refactor launcher connectivity management to support the flavor declared by each terminating Link
- [x] 4.4 Reconcile both former and new endpoint launchers when Link endpoints or connectivity change
- [x] 4.5 Add tests for mixed connectivity flavors, live flavor changes, deterministic interface conflicts, and unresolved endpoints
- [x] 4.6 Record resolved endpoint Node UIDs and delete Links whose bound Nodes are deleted or replaced
- [x] 4.7 Add tests for endpoint deletion, same-name Node replacement, order-independent creation, host endpoints, and unrelated Node deletion
- [x] 4.8 Log Node deletion events and each associated Link lifecycle deletion with its reason

## 5. Topology Resource and Direct Manifest Generation

- [x] 5.1 Render LauncherProfiles before Nodes and stamp every compiled Node with an explicit profile reference
- [x] 5.2 Generate complete dedicated LauncherProfiles for Nodes with distinct topology launcher policy
- [x] 5.3 Render topology connectivity onto every emitted Link and remove profile-level connectivity output
- [x] 5.4 Render all per-node payload attachments onto emitted Nodes
- [x] 5.5 Preserve deterministic naming, owner references, drift correction, pruning, and bounded aggregate Topology status
- [x] 5.6 Update clabverter direct output to match compiler-emitted Node, Link, and LauncherProfile specs
- [x] 5.7 Add compiler and golden-manifest tests for shared profiles, per-node overrides, link connectivity, and direct output parity

## 6. Migration, RBAC, and User Surfaces

- [x] 6.1 Add LauncherProfile and updated Node/Link permissions to manager, launcher, Helm, and generated RBAC assets
- [x] 6.2 Implement cleanup or migration handling for obsolete NodeProfile and legacy connectivity resources
- [x] 6.3 Update UI queries and visualization to consume Node, Link, LauncherProfile, and resource-local statuses
- [x] 6.4 Update examples and end-to-end fixtures from profile selectors to explicit `launcherProfileRef`
- [x] 6.5 Document the primary Node/Link API, auxiliary high-level Topology resource, profile migration, bounded-object scaling, and temporary management-network compatibility field

## 7. Verification

- [x] 7.1 Run API generation checks and verify generated artifacts are reproducible
- [x] 7.2 Run unit tests for API resolution, Node reconciliation, Link allocation, launcher materialization, and topology compilation
- [x] 7.3 Run direct-resource and Topology-compiler end-to-end suites
- [x] 7.4 Verify large generated labs contain no aggregate authoritative object and unrelated Link/profile events do not enqueue unaffected Nodes
- [x] 7.5 Run linting and confirm documentation, examples, CRDs, clients, and UI types are synchronized
