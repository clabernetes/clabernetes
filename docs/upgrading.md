---
title: Upgrading
description: Breaking changes and upgrade steps for c9s releases.
icon: ArrowUpCircle
---

## Node spec is a curated containerlab subset

**Breaking change:** The Node spec no longer mirrors the whole containerlab node definition. It now
carries only the vocabulary a launcher pod can actually realize, and unknown fields are **rejected**
at apply time instead of being silently ignored.

Re-apply any Node that uses a removed field. `kubectl apply` names the offending field, i.e.
`unknown field "spec.runtime"`.

A Topology `definition:` needs no edit. It is a native containerlab topology, so it is still
accepted as-is: fields clabernetes cannot realize are dropped and each one is logged with its line,

```text
line 19: field restart-policy is not supported by clabernetes and was omitted from the topology
```

`clabverter` prints the same warnings while converting, before anything reaches the cluster. What
still fails is a definition that is malformed, or one where a *supported* field holds an unusable
value -- dropping that would quietly change your lab.

### Removed fields

| Removed | Use instead |
| --------- | ------------- |
| `publish`, `sandbox`, `kernel`, `wait-for`, top-level `SANs` | Nothing -- containerlab itself removed these, so they made the launcher fail to parse the topology. `SANs` moved to `certificate.sans` |
| `cpu`, `cpu-set`, `memory` | `LauncherProfile.resources` -- the pod's requests/limits are what actually bound a node |
| `image-pull-policy` | `LauncherProfile.imagePull` |
| `healthcheck` | `LauncherProfile.statusProbes` -- docker healthcheck status is invisible to Kubernetes |
| `runtime` | Nothing -- the launcher runs exactly one runtime (docker) |
| `auto-remove` | Nothing -- the pod owns node lifecycle, and a removed container leaves a ready pod with no node |
| `labels` | `metadata.labels` on the Node -- in a Topology `definition:` this is automatic, see below |
| `group`, `position` | Nothing -- these feed containerlab graphs and inventories, which c9s does not produce |
| `startup-delay` | Nothing -- it staggers boots on one host; pods start independently |
| `aliases` | Nothing -- docker network aliases only resolve inside a single pod |
| `extras.mysocket-proxy` | Nothing -- mysocketctl is gone, along with the `publish` field that fed it |

`stages` is likewise unsupported: stage ordering gates the nodes of one lab against each other,
which assumes the whole lab on one host.

### Added fields

`devices`, `cap-add`, `privileged`, `tmpfs`, `security-opts`, `shm-size`, and
`suppress-startup-config` are now available, and `certificate` gained `key-size`,
`validity-duration`, and `sans`.

These require containerlab 0.78.0, which the launcher image now ships. If you override
`containerlabVersion`, 0.78.0 is the floor -- older releases reject the new fields outright.

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

`owner: roman` lands on the emitted Node's `metadata.labels`, and from there on the launcher
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

Uninstalling c9s deletes all Topology, Node, Link, LauncherProfile, ImageRequest, and Config
resources when the CRDs are removed.

### Upgrade steps

1. Export your manifests before uninstalling:

   ```bash
   kubectl get topologies,nodes,links,launcherprofiles,imagerequests,configs -A -o yaml > backup.yaml
   ```

2. Uninstall the existing c9s install and remove its CRDs:

   ```bash
   make uninstall-c9s
   ```

   Or manually uninstall the Helm release, delete all `*.c9s.run` and
   `*.clabernetes.containerlab.dev` CRDs, then delete the `c9s` namespace.

3. Update every manifest to use the new API version:

   ```yaml
   apiVersion: c9s.run/v1alpha1
   ```

4. Install the new c9s release (Helm install).

5. Re-apply your updated manifests.

### kubectl resource names

| Legacy | New |
| ------ | --- |
| `kubectl get nodes.clabernetes.containerlab.dev` | `kubectl get nodes.c9s.run` |
| `kubectl get links.clabernetes.containerlab.dev` | `kubectl get links.c9s.run` |
| `kubectl get topologies.clabernetes.containerlab.dev` | `kubectl get topologies.c9s.run` |

There is no automatic migration of existing custom resource instances. Treat this as a clean
cutover.
