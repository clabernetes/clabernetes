---
title: Launcher profiles
description: Reusable direct-workload Kubernetes policy referenced explicitly by network Nodes.
icon: Settings2
---

A `LauncherProfile` contains reusable Kubernetes policy for the direct device workload that
realizes one or more Nodes; the CRD name is retained from the earlier runtime. It keeps Kubernetes
deployment concerns separate from the containerlab-shaped Node payload.

Typical profile settings include:

- CPU and memory for a Node's primary application container
- scheduling and tolerations
- service exposure
- Kubernetes image pull policy and pull Secrets
- status probes
- persistence
- management overlay address allocation

Device privilege, capabilities, and devices are never profile policy: they come from the imported
containerlab package's plan.

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

c9s considers a Node ready only when every device application container representing it is running
and passes its Kubernetes probes: readiness behavior declared by the imported containerlab plan,
and any image-defined OCI healthcheck or Node `healthcheck` contract merged over it, translate
into container startup and readiness probes. When `statusProbes.enabled` is true, optional TCP or
SSH probe configuration adds an application-level requirement executed inside the device
container; c9s does not infer ports, credentials, or behavior from a containerlab kind or image
name.

For a kind whose imported plan declares no readiness behavior and an image without a healthcheck,
this generic signal is intentionally process-level: a running network OS may still be booting
services or converging protocols. Declare a `healthcheck`, define one in the image, or configure
an explicit TCP or SSH probe when application-level readiness is required.

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
