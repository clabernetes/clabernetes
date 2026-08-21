---
title: Differences from containerlab
description: Intentional semantic differences between a containerlab host and the direct Kubernetes runtime.
icon: GitCompareArrows
---

c9s consumes unmodified containerlab kind behavior, but a Kubernetes cluster is not a Docker
host. The differences below are deliberate: each one preserves the *lab* semantics while
replacing a Docker-host mechanism with its Kubernetes-native equivalent. Everything else that
cannot be represented fails closed at compile or planning time with a structured diagnostic --
nothing is silently dropped.

## Networking

- **Cross-node wires are controller-realized.** `Link.spec.connectivity: vxlan | slurpeeth`
  remains accepted input, but the transport is an implementation detail: same-worker endpoint
  pairs are patched directly in the worker host namespace, cross-worker pairs use VXLAN keyed
  by the Link's cluster-wide tunnel id. The device always sees a plain veth; wire semantics
  (L2 point-to-point, MTU intent, live rewires, cleanup) are preserved. The slurpeeth
  userspace TCP transport is retired.
- **The management network is the Pod network.** There is no Docker management bridge; the Pod
  address is the node's management identity and is always addressed and reachable in-Pod, even
  for kinds that take ownership of the Pod's primary interface. Docker-only `mgmt` fields
  (`network`, `bridge`, `mtu`, `external-access`, `skip-when-unused`, `driver-opts`) are
  accepted and ignored with a warning; the address-policy fields keep their meaning for the
  management overlay.
- **Exposure is a Service, not a host port.** `ports` entries are destination ports only; the
  host half of Docker-style `host:container` pinning is dropped with a warning because it only
  ever described the local Docker host. Reachability comes from per-node Services
  (LoadBalancer by default).
- **`aliases` become Services.** Each declared network alias is realized as an extra
  same-namespace headless Service selecting the node's Pod, so lab members resolve the alias
  exactly like the node's own name.
- **`mgmt-net` and `macvlan` endpoints are rejected.** They require host networking a cluster
  does not provide. Host Links (`host:<interface>`) are supported through the node-local
  host-endpoint daemon.
- **Rewiring through a Topology replaces the Link object.** Compiled Link names encode both
  endpoints, so changing either endpoint's interface in the definition deletes the old Link and
  creates a new one -- the former endpoints are removed and recreated *on both sides*, exactly
  like deleting and re-adding the wire. Editing a Link custom resource in place keeps its
  identity, and only the endpoint you changed is touched; the peer keeps its interface. Either
  way, the lifecycle action follows each kind's declared link-apply mode -- and runtime state a
  device applied to a recreated interface (a linux-kind `exec` address, a NOS's binding to the
  old interface) does not survive the recreation, matching a containerlab redeploy of that
  wire.

## Naming and metadata

- **Node names are Kubernetes object names.** They must be DNS-1123 labels, and the namespace
  is the topology boundary.
- **`labels` become Kubernetes labels** on the emitted Node, its Deployment, and Pods --
  selectable with `kubectl` -- instead of Docker container labels. Labels Kubernetes would
  reject, reserved `c9s.run/` keys, and controller-owned keys fail compilation.

## Lifecycle

- **Containers restart with their Pod.** `restart-policy` accepts `always` and
  `unless-stopped`; Docker's `no` and `on-failure` cannot exist in a shared Pod and are
  rejected at compile time.
- **`stages` are rejected.** Multi-node boot orchestration gates the nodes of one lab against
  each other on a single host; direct device Pods start independently. `startup-delay` is
  honored per node.
- **Docker runtime selection has no meaning.** `runtime`, `auto-remove`, `pid-mode`,
  `cgroupns-mode`, and `cpu-set` are rejected with diagnostics stating why; the cluster's
  container runtime runs every device container, and `cpu`/`memory` become ordinary Kubernetes
  container limits.
- **`credentials` bytes are rejected.** Secret material belongs in referenced Kubernetes
  Secrets; imported kind default credentials still apply.

## Files and images

- **No host binds.** Files come from ConfigMaps, Secrets, digest-pinned URLs, generated
  artifacts, and PVC-backed persistence; the preparation init container stages and
  digest-verifies everything before the device starts. Arbitrary host paths are rejected.
- **The kubelet pulls images.** Pull policy and pull Secrets are Kubernetes-native; there is
  no image import, pull-through, or per-lab insecure-registry setting. c9s reads only registry
  metadata and fails readiness if the running image diverges from the planned digest.
