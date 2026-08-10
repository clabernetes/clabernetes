---
title: Upgrading
description: Breaking changes and upgrade steps for c9s releases.
icon: ArrowUpCircle
---

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
