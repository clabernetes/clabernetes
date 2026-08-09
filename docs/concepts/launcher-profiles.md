---
title: Launcher profiles
description: Reusable Kubernetes and launcher policy referenced explicitly by network Nodes.
---

A `LauncherProfile` contains policy for the launcher workload that realizes one or more Nodes. It
keeps Kubernetes deployment concerns separate from the containerlab-shaped Node payload.

Typical profile settings include:

- launcher CPU and memory
- scheduling and tolerations
- service exposure
- image pulling
- status probes
- persistence and launcher privileges

## Explicit references

A Node references at most one LauncherProfile in the same namespace:

```yaml
apiVersion: c9s.run/v1alpha1
kind: LauncherProfile
metadata:
  name: lab-policy
spec:
  resources:
    requests:
      memory: 4Gi
      cpu: "2"
  statusProbes:
    enabled: true
---
apiVersion: c9s.run/v1alpha1
kind: Node
metadata:
  name: srl1
spec:
  launcherProfileRef:
    name: lab-policy
  kind: nokia_srlinux
  image: ghcr.io/nokia/srlinux:26.3
```

Profiles are not selected by labels and are not merged into inheritance chains. Fields set on the
referenced profile take precedence over global `Config` defaults; fields it omits continue through
global resolution.

If a Node names a profile that does not exist, c9s does not silently fall back to global defaults.
The Node remains unrealized until that explicit reference resolves.

## Profiles generated from Topology

The [Topology compiler](/docs/concepts/topology) normally creates one shared LauncherProfile and
references it from every generated Node. A Node with distinct resource policy receives a complete
dedicated profile rather than a partial child profile.

## Related documentation

- [Resource and scheduling guide](/docs/guides/resource-management)
- [Image pull guide](/docs/guides/image-pull)
- [LauncherProfile reference](/docs/crd-reference#launcherprofile-crd)
