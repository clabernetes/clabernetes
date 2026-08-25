---
title: Upgrading
description: Breaking changes and upgrade steps for c9s releases.
icon: ArrowUpCircle
---

## The direct device runtime

The nested launcher-pod runtime is removed. The manager always runs every network-device image
as a regular application container in a c9s-managed Pod, with planning executed in short-lived
isolated worker Pods. There is no runtime selector and no fallback: a Node the direct planner
cannot realize stays failed with a structured diagnostic. Private registries and registry trust
are Kubernetes concerns (`imagePullSecrets`, cluster runtime configuration, and the
controller-only `Config.spec.imagePull.registryMetadataTrust`).

## The `connectivity` field is removed

**Breaking change:** `Topology.spec.connectivity` and `Link.spec.connectivity` no longer exist;
remove the field from your manifests. There is exactly one cross-Pod realization: the device
sees a plain veth interface, and each endpoint feeds the Pod's fabric wire keyed by the Link's
allocated tunnel id. Wire semantics (L2 point-to-point, MTU intent, live rewires, cleanup,
rescheduling) are unchanged; the experimental slurpeeth userspace TCP transport is retired.

## LauncherProfile is renamed to NodeProfile

**Breaking change:** the profile CRD is now `NodeProfile` (`kubectl get nodeprofiles`); the
spec is unchanged. Update your manifests:

- `kind: LauncherProfile` becomes `kind: NodeProfile`
- `Node.spec.launcherProfileRef` becomes `Node.spec.profileRef`
- `Node.status.appliedLauncherProfile` is now `Node.status.appliedProfile`, and the Node
  condition is `NodeProfileResolved`

## Slimmed statuses

`Link.status.error` is removed -- read the `Accepted` condition instead (its message carries
the same text). `Node.status.probeStatuses` is removed -- direct-runtime observations live in
`Node.status.conditions` and `Node.status.directContainers`.

## Node spec is a curated containerlab subset

**Breaking change:** The Node spec no longer mirrors the whole containerlab node definition. It now
carries only the vocabulary the direct runtime can actually realize, and unknown fields are
**rejected** at apply time instead of being silently ignored.

Re-apply any Node that uses a removed field. `kubectl apply` names the offending field, i.e.
`unknown field "spec.runtime"`.

A Topology `definition:` compiles fail-closed: a field the direct runtime cannot realize fails
compilation before any resource is created, with a sorted diagnostic naming the field and its
location. Deliberately rejected vocabulary gets a diagnostic stating why, i.e.

```text
unsupported-field topology.nodes.n1.runtime (line 19): field "runtime" is rejected: container
runtime selection is Docker-only; direct device Pods always use the cluster's container runtime
```

`clabverter` prints the same diagnostics while converting, before anything reaches the cluster.
Only constructs whose loss cannot change lab behavior inside the cluster warn instead of
failing: Docker-only management-network fields and the host half of Docker-style port pinning.

### Removed fields

| Removed | Use instead |
| --------- | ------------- |
| `publish`, `sandbox`, `kernel`, `wait-for`, top-level `SANs` | Nothing -- containerlab itself removed these. `SANs` moved to `certificate.sans` |
| `runtime` | Nothing -- devices run as regular Kubernetes containers |
| `auto-remove` | Nothing -- Kubernetes owns container lifecycle |
| `pid-mode`, `cgroupns-mode` | Nothing -- Docker namespace-sharing modes have no direct-Pod mapping |
| `cpu-set` | Nothing -- CPU pinning has no portable Pod mapping; use `cpu`/`memory` limits |
| `credentials` | Nothing -- credential bytes belong in referenced Secrets; imported kind default credentials still apply |
| `stages` | Nothing -- stage ordering gates the nodes of one lab against each other, which assumes the whole lab on one host |
| `labels` | `metadata.labels` on the Node -- in a Topology `definition:` this is automatic, see below |
| `position` | Nothing -- it feeds containerlab graphs, which c9s does not produce |
| `extras.mysocket-proxy` | Nothing -- mysocketctl is gone, along with the `publish` field that fed it |

### Added fields

The full portable containerlab node vocabulary is accepted: `devices`, `cap-add`, `privileged`,
`tmpfs`, `security-opts`, `shm-size`, `suppress-startup-config`, `group`, `startup-delay`,
`restart-policy` (`always`/`unless-stopped` -- Docker's `no` and `on-failure` cannot exist in a
shared Pod), `image-pull-policy`, `cpu` and `memory` (they become the device container's
Kubernetes limits), `healthcheck` (merged over the image-defined OCI healthcheck into container
startup/readiness behavior), `aliases` (each alias becomes an extra same-namespace headless
Service selecting the node's Pod), and `link-apply-mode`. `certificate` gained `key-size`,
`validity-duration`, and `sans`, and `topology.groups` participates in inheritance exactly like
kinds.

The direct planner imports containerlab 0.78.0 as its package baseline. There is no per-workload
version override: updating supported kinds and package behavior is an intentional Go module bump.

### `ports` are destination ports only

`ports` entries are now the port the node itself listens on, with an optional protocol:

```yaml
ports:
  - 22/tcp
  - 5201/udp
```

The docker style `host:container` form is rejected on Nodes. The pod-side port was never yours to
choose -- c9s allocates it and records both halves in `status.exposedPorts` -- so pinning it could
only collide. Two-sided entries inside a Topology `definition:` are normalized to their destination
port automatically, so pasted containerlab topologies keep working.

### `labels` become Kubernetes labels

There is no `spec.labels` on a Node, because Kubernetes already has a place for labels. Node labels
in a Topology `definition:` are carried there for you:

```yaml
topology:
  nodes:
    srl1:
      kind: nokia_srlinux
      labels:
        owner: roman
```

`owner: roman` lands on the emitted Node's `metadata.labels`, and from there on the device
Deployment and its pods -- so `kubectl get pods -l owner=roman` finds the lab. They inherit from
`defaults` and `kinds` exactly as `env` does. Unlike containerlab, these are *not* docker labels on
the node container.

Three kinds of label are dropped, each with a warning naming it: anything Kubernetes would reject as
a label (docker's keys and values are far more permissive), anything in the `c9s.run/` namespace,
and controller-owned identity or selector keys such as `app.kubernetes.io/name`.

c9s-owned labels now use the qualified `c9s.run/` namespace, for example
`c9s.run/topologyOwner` and `c9s.run/topologyNode`. Existing resources must be recreated or have
their labels refreshed during the upgrade; the old `clabernetes/...` label keys are no longer
written or selected. Annotation keys retain their existing `clabernetes/...` names.

## API group renamed to `c9s.run`

**Breaking change:** All c9s custom resources now use `apiVersion: c9s.run/v1alpha1` instead of
`clabernetes.containerlab.dev/v1alpha1`.

This release requires a **full uninstall and reinstall**. Do not `helm upgrade` an existing install
in place.

Uninstalling c9s deletes all Topology, Node, Link, NodeProfile, and Config
resources when the CRDs are removed.

### Upgrade steps

1. Export your manifests before uninstalling:

   ```bash
   kubectl get topologies,nodes,links,nodeprofiles,configs -A -o yaml > backup.yaml
   ```

2. Uninstall the existing c9s install and remove its CRDs:

   ```bash
   make uninstall
   ```

   Or manually uninstall the Helm release, delete all `*.c9s.run` and
   `*.clabernetes.containerlab.dev` CRDs, then delete the `c9s` namespace.

3. Update every manifest to use the new API version:

   ```yaml
   apiVersion: c9s.run/v1alpha1
   ```

4. Install the new c9s release (Helm install).

5. Re-apply your updated manifests.

The repository `make install` target performs the API-group check before Helm. It allows same-group
version changes, but refuses a legacy/new group crossing and prints the destructive cleanup command:

```bash
C9S_CONTEXT=<your-context> make uninstall
C9S_CONTEXT=<your-context> VERSION=latest make install
```

The installer reconciles the manager image reference while preserving unrelated fields in
the global `Config` resource. Development charts use immutable commit-scoped image tags; `0.0.0` is
a mutable main channel and is not a rollback target. For a reproducible rollback within one API
group, select an exact published chart version.

### kubectl resource names

| Legacy | New |
| ------ | --- |
| `kubectl get nodes.clabernetes.containerlab.dev` | `kubectl get nodes.c9s.run` |
| `kubectl get links.clabernetes.containerlab.dev` | `kubectl get links.c9s.run` |
| `kubectl get topologies.clabernetes.containerlab.dev` | `kubectl get topologies.c9s.run` |

There is no automatic migration of existing custom resource instances. Treat this as a clean
cutover.
