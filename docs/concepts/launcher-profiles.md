---
title: Launcher profiles
description: Reusable Kubernetes and launcher policy referenced explicitly by network Nodes.
icon: Settings2
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

When `statusProbes.enabled` is true, c9s considers a Node ready only after its nested Docker
container is running and is not paused, restarting, or dead. If the container image defines a
Docker healthcheck, that healthcheck must also report healthy. Optional TCP or SSH probe
configuration adds an application-level requirement; c9s does not infer ports, credentials, or
behavior from a containerlab kind or image name.

For an image without a Docker healthcheck, this generic signal is intentionally process-level: a
running network OS may still be booting services or converging protocols. Define a healthcheck in
the image, or configure an explicit TCP or SSH probe, when application-level readiness is required.

If a Node names a profile that does not exist, c9s does not silently fall back to global defaults.
The Node remains unrealized until that explicit reference resolves.

## Profiles generated from Topology

The [Topology compiler](/docs/concepts/topology) normally creates one shared LauncherProfile and
references it from every generated Node. A Node with distinct resource policy receives a complete
dedicated profile rather than a partial child profile.

## Related documentation

- [Resource and scheduling guide](/docs/guides/resource-management)
- [Image pull guide](/docs/guides/image-pull)
- [LauncherProfile reference](/docs/crd/launcher-profile)
