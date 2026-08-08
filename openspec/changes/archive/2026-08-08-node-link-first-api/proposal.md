## Why

A single Topology custom resource grows with every node, link, and inline configuration in a lab, so sufficiently large labs can exceed the Kubernetes/etcd object payload limit. Clabernetes needs a bounded-object API in which nodes and links can be created, updated, and reconciled independently while preserving a convenient path from Containerlab topology files.

## What Changes

- Introduce `Node` as the authoritative API for one self-contained Containerlab network node and its per-node payload.
- Introduce `Link` as the authoritative API for one point-to-point connection between two namespaced Nodes, including link-local connectivity configuration and controller-allocated operational status.
- Keep `Topology` as an auxiliary high-level resource for quickly defining a lab; its controller expands the definition into independently reconciled Node, Link, and LauncherProfile resources, while direct Node and Link creation does not require a Topology.
- Introduce `LauncherProfile` for reusable Kubernetes and launcher runtime configuration such as Pod resources, scheduling, image pull behavior, security, persistence, exposure, and probes.
- Add an optional, namespace-local `spec.launcherProfileRef` to Node. Nodes without a reference use global Config defaults; an explicitly missing profile prevents realization and is reported in Node status.
- Use one explicit LauncherProfile reference per Node instead of selector-, priority-, and merge-based profile attachment.
- Store the connectivity flavor on each Link so both endpoints consume one authoritative value.
- Keep per-device management addresses on Node and temporarily retain shared Containerlab management-network settings on LauncherProfile for existing Topology compatibility; their final ownership is deferred.
- Store per-node and per-link observations or allocations in each object's status so no aggregate status object grows proportionally with topology size.

## Capabilities

### New Capabilities

- `node-lifecycle`: Defines the self-contained Node API, optional LauncherProfile reference, realization behavior, and per-node status.
- `link-lifecycle`: Defines point-to-point Link intent, endpoint validation, connectivity ownership, allocation, and status behavior.
- `launcher-profiles`: Defines explicit reusable launcher runtime profiles and their reference/default/error semantics.
- `topology-resource`: Defines the auxiliary Topology resource and its high-level abstraction over bounded Node, Link, and LauncherProfile resources.

### Modified Capabilities

None.

## Impact

- Public CRDs, generated clients/OpenAPI, documentation, examples, UI types, and RBAC gain Node, Link, and LauncherProfile APIs while the existing Topology API remains supported.
- The manager is split into cooperating Node, Link, and optional Topology compiler controllers.
- Launcher startup and watch behavior changes to materialize configuration from its Node and only the Links that terminate on its node group.
- Existing Topology reconciliation, clabverter output, tests, and migration/cleanup behavior must target the new primitive resources.
- Existing Topology manifests require no migration; users and tooling may opt into the direct Node, Link, and LauncherProfile APIs.
