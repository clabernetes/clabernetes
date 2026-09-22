---
title: Differences from containerlab
description: Intentional semantic differences between a containerlab host and the direct Kubernetes runtime.
icon: GitCompareArrows
---

c9s consumes unmodified kind behavior from containerlab 0.78.0, but a Kubernetes cluster is
not a Docker host. The differences below are deliberate: each one preserves the *lab*
semantics while replacing a Docker-host mechanism with its Kubernetes-native equivalent.
Everything else that cannot be represented fails at compile or planning time with a clear
diagnostic. Nothing is silently dropped.

## Networking

- **Cross-node wires are sidecar-realized.** The transport is an implementation detail: each
  endpoint feeds a sidecar-to-sidecar wire keyed by the Link's allocated wire id, which
  carries any link MTU without underlay coordination and propagates carrier state between the
  ends. The
  device always sees a plain veth; wire semantics (L2 point-to-point, MTU intent, carrier on
  peer loss, live rewires, cleanup) are preserved.
- **The management network keeps containerlab semantics.** There is no Docker management
  bridge, but every node still gets a controller-allocated management address on a shared
  management subnet spanning the whole topology. Peers are reachable by management address
  device-to-device, and the gateway answers Pod-locally. Docker-only `mgmt` fields
  (`network`, `bridge`, `mtu`, `external-access`, `skip-when-unused`, `driver-opts`) are
  accepted and ignored with a warning; the address-policy fields keep their meaning for the
  management overlay.
- **Management neighbors are resolved on demand.** The kernel queues the first packet while
  the sidecar installs the peer directory's authorized address and tunnel mapping. Idle peers
  consume no IPv4 neighbor or forwarding entries, and unknown destinations are not flooded.
  Contacted peers retain their entries until they leave the directory; communicating with
  every peer can still produce all-to-all state. IPv6 gateway proxy entries continue to track
  every IPv6 peer.
- **Management overlay is enabled by default.** Set `spec.disableManagement: true` on a Topology, or
  `spec.mgmt.disabled: true` on a NodeProfile, to omit management address allocation and the
  management mesh. Omit explicit `mgmt-ipv4` and `mgmt-ipv6` node addresses when using this
  option. Kubernetes Pod networking and declared data links remain active; management-subnet
  addresses and management peer names are unavailable. Devices that require a configured
  management interface may need the overlay enabled.
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
  creates a new one: the former endpoints are removed and recreated *on both sides*, exactly
  like deleting and re-adding the wire. Editing a Link custom resource in place keeps its
  identity, and only the endpoint you changed is touched; the peer keeps its interface. Either
  way, the lifecycle action follows each kind's declared link-apply mode. Runtime state a
  device applied to a recreated interface (most visibly a linux-kind `exec` address) does not
  survive the recreation, matching a containerlab redeploy of that wire.

## Naming and metadata

- **Node names are Kubernetes object names.** Kubernetes only takes lowercase DNS labels, so a
  node name it cannot carry is sanitized: the name is lower-cased, every other character becomes
  `-`, a name starting with a digit is prefixed with `clab-`, and a name longer than 63
  characters is truncated and suffixed with a hash. `R1` becomes `r1`, `PE_1` becomes `pe-1`.
  Every reference follows the node: links, `network-mode: container:<node>`, and the node-keyed
  policy on the Topology (`filesFromConfigMap`, `filesFromSecret`, `filesFromURL`, `resources`,
  `statusProbes`) are still written with the name the definition uses. Two node names that differ
  only in something Kubernetes cannot carry (`R1` and `r1`) would become one object, and that
  fails compilation. The namespace is the topology boundary.
- **`labels` become Kubernetes labels** on the emitted Node, its Deployment, and Pods
  (selectable with `kubectl`) instead of Docker container labels. Labels Kubernetes would
  reject, reserved `c9s.run/` keys, and controller-owned keys fail compilation.

## Lifecycle

- **Device Pods prefer an even spread across workers.** c9s counts direct device Pods across
  their namespace, including devices in separate Deployments. This is a soft scheduling
  preference; node selectors, affinity, taints, and available resources still determine where
  a Pod can run, and a single-worker cluster remains supported.
- **Containers restart with their Pod.** `restart-policy` accepts `always` and
  `unless-stopped`; Docker's `no` and `on-failure` cannot exist in a shared Pod and are
  rejected at compile time.
- **A failing `exec` command does not stop the node.** The topology's `exec` list runs as a
  Kubernetes PostStart hook, and a failed hook would take the container down; a command that
  fails is instead reported on the node's container log and the remaining commands still run,
  which is what containerlab does. Commands a kind's own deployment records stay fail-closed.
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

- **No arbitrary host binds.** Files come from ConfigMaps, Secrets, digest-pinned URLs, generated
  artifacts, and PVC-backed persistence; the preparation init container stages and
  digest-verifies everything before the device starts. Arbitrary host paths are rejected.
- **Runtime binaries use a bounded worker cache.** The preparation container alone mounts
  `/var/lib/clabernetes/runtime-binary-cache`, which retains four content-verified binary
  versions (at most 256 MiB each). When this cache and the Pod's storage share a filesystem
  supporting reflinks, preparation creates an independent copy-on-write binary instead of
  writing the same executable into every Pod. Application containers receive only their own
  read-only file, with their own file metadata and SELinux label. Unsupported filesystems use
  ordinary copies and therefore still need disk space for one runtime binary per Pod.
- **The kubelet pulls images.** Pull policy and pull Secrets are Kubernetes-native; there is
  no image import, pull-through, or per-lab insecure-registry setting. c9s reads only registry
  metadata and fails readiness if the running image diverges from the planned digest.
- **`env-files` and config-engine `config` are rejected.** Environment variables go in `env`,
  and templated configuration must be rendered before the definition is applied.
