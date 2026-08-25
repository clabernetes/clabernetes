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

- **Cross-node wires are sidecar-realized.** The transport is an implementation detail: each
  endpoint feeds a sidecar-to-sidecar wire keyed by the Link's allocated wire id, which
  carries any link MTU without underlay coordination and propagates carrier state between the
  ends. The
  device always sees a plain veth; wire semantics (L2 point-to-point, MTU intent, carrier on
  peer loss, live rewires, cleanup) are preserved.
- **The management network keeps containerlab semantics.** There is no Docker management
  bridge, but every node still gets a controller-allocated management address on a shared
  management subnet spanning the whole topology -- peers are reachable by management address
  device-to-device, and the gateway answers Pod-locally. Docker-only `mgmt` fields
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
  connectivity sidecar, which places the worker-side veth end through a read-only worker
  namespace handle; the pair dies with the Pod, leaving no worker residue.
- **Rewiring through a Topology replaces the Link object.** Compiled Link names encode both
  endpoints, so changing either endpoint's interface in the definition deletes the old Link and
  creates a new one -- the former endpoints are removed and recreated *on both sides*, exactly
  like deleting and re-adding the wire. Editing a Link custom resource in place keeps its
  identity, and only the endpoint you changed is touched; the peer keeps its interface. Either
  way, the lifecycle action follows each kind's declared link-apply mode -- and runtime state a
  device applied to a recreated interface (most visibly a linux-kind `exec` address) does not
  survive the recreation, matching a containerlab redeploy of that wire.

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
