---
title: Operating a lab
description: What c9s creates in a namespace, how to read Node, Link, and Topology status, and how to reach logs and packets.
---

This guide covers the day-to-day view of a running lab: the objects c9s creates, how to read
their status, and how to get at logs and packets.

## What c9s creates in a namespace

For every Node:

- a Deployment and Pod named after the Node; grouped Nodes (`network-mode: container:<primary>`)
  share the primary's Pod
- the expose Service named after the Node, a headless `<node>-wire` Service for wire peer
  discovery, and one headless Service per declared alias
- immutable plan ConfigMaps named `<node>-plan-<digest>`, `<node>-plan-input-<digest>`, and
  `<node>-connectivity-<digest>`, replaced when the plan changes
- a PersistentVolumeClaim named after the Node when persistence is enabled

Per namespace:

- the `c9s-peer-directory-0` to `c9s-peer-directory-7` ConfigMaps (the peer directory) that
  map node names to management addresses and to the Pods realizing them
- a `direct-device-ca-...` Secret holding the lab CA, plus one `<node>-certificates-...`
  Secret per Node that sets `certificate.issue`

Do not edit these objects. The controller owns them and reverts drift.

Planning uses a shared Deployment named `clabernetes-planner-pool` in the manager namespace,
with four reusable workers by default. New plans run in fresh processes inside those workers;
unchanged Nodes reuse cached plans. Configure worker count with the Helm value
`plannerPool.replicas`. With `plannerPool.enabled: false`, each new plan instead uses a
short-lived `<node>-planner-...` Pod in the lab namespace.

## Node status

```bash
kubectl get nodes.c9s.run
kubectl get nodes.c9s.run -o wide
```

`READINESS` is `ready`, `notready`, or `unknown`, and `RUNSTATE` is `Running` or `Stopped`.
The wide view adds the container,
preparation, and connectivity conditions, the plan digest, and the applied NodeProfile.
`kubectl describe` shows every condition:

| Condition | Meaning |
| --- | --- |
| `NodeProfileResolved` | the referenced NodeProfile exists |
| `PlanApplied` | the current spec was planned and rendered; when `False`, the reason names the cause, for example `PlanPending`, `ImagePullSecretMissing`, `OCIMetadataResolveManifest`, `PlanRejected`, or `DeploymentInvalid` |
| `Prepared` | the preparation init container staged and verified every file |
| `ConnectivityReady` | every interface of the cold-start plan exists |
| `ContainersReady` | every application container of the Node runs and passes its probes |
| `LinkLifecycleAction` | the action taken for the latest Link-only change: `live` or `recreate` (`restart` also recreates the Pod) |
| `DeviceStateReset` | progress of a device-state reset, see [Persistent storage](/docs/guides/persistence) |
| `Stopped` | `True` once a stopped Node's Pod is gone; `False` with reason `Stopping` while it terminates, or `Running`, see [Stopping, starting, and restarting a Node](#stopping-starting-and-restarting-a-node) |

A spec change flips readiness to `notready` at once, with `PlanApplied=False` and reason
`PlanPending`, and readiness returns when the new plan is applied. Wait for a Node with:

```bash
kubectl wait nodes.c9s.run/srl1 --for=jsonpath='{.status.readiness}'=ready --timeout=10m
```

Every condition change also produces an event on the Node:

```bash
kubectl get events --field-selector involvedObject.kind=Node,involvedObject.name=srl1
```

The status also records the allocated management address under `status.directManagement` and
the exposed ports under `status.exposedPorts`.

## Stopping, starting, and restarting a Node

A Node's `spec.runState` is `Running` by default. Setting it to `Stopped` scales the Node's
Deployment to zero; the Node, its Deployment, Services, PersistentVolumeClaim, and Links stay.
Setting it back to `Running` starts one Pod again:

```bash
kubectl patch node.c9s.run srl1 --type merge -p '{"spec":{"runState":"Stopped"}}'
kubectl wait node.c9s.run/srl1 --for=condition=Stopped=True --timeout=5m

kubectl patch node.c9s.run srl1 --type merge -p '{"spec":{"runState":"Running"}}'
kubectl wait node.c9s.run/srl1 --for=jsonpath='{.status.readiness}'=ready --timeout=10m
```

While a Node is stopped:

- Its readiness is `notready` and its `Stopped` condition is `True` once no Pod of it remains.
  The Topology counts it as not ready, so a Topology that was running reports `degraded`.
- Every Link to it goes carrier-down at its peers, like a pulled cable, and comes back up once
  the Node runs again. The peers keep running.
- The Topology and Node controllers keep reconciling. Applying a changed Topology updates the
  stopped Node, which stays stopped and boots the updated definition when started. The
  Topology compiler never sets `runState`, so an apply never starts a stopped Node.
- With persistence enabled, saved device state is kept on the claim and survives the stop.

The Node API is the only control: do not scale the Deployment by hand, the controller reverts
it. Grouped Nodes (`network-mode: container:<primary>`) share the primary's Pod, so only the
primary Node's `runState` counts; it stops and starts every Node of the group, and the
secondaries' `runState` has no effect. Under startup admission (`rollout.batchSize` on the
Topology or the global `Config`), a stopped Node takes no admission slot and starting it again
waits for a slot like a new Node.

To restart a Node, annotate it with an opaque token of your choice:

```bash
TOKEN=$(date +%s%N)
kubectl annotate node.c9s.run srl1 c9s.run/restart-requested="$TOKEN" --overwrite
kubectl wait node.c9s.run/srl1 --for=jsonpath='{.status.observedRestartToken}'="$TOKEN" --timeout=10m
kubectl wait node.c9s.run/srl1 --for=jsonpath='{.status.readiness}'=ready --timeout=10m
```

The controller replaces the Pod once for every token that differs from the last one it handled,
and `status.observedRestartToken` reports the token the current Pod was started with. The
annotation can stay in place, and removing it does not restart the Node again. Restarting a
grouped Node replaces the shared Pod, which restarts the whole group. A token set on a stopped
Node is handled when the Node starts. A restart keeps persistent device state; to also discard
it, use a [device-state reset](/docs/guides/persistence#resetting-a-node-to-its-startup-configuration).

Stopping, starting, and restarting need only `patch` on `nodes.c9s.run`, no rights on
Deployments or Pods.

## Link and Topology status

```bash
kubectl get links.c9s.run
```

This shows both endpoints, the allocated wire id, and the `Accepted` condition, which is
`False` when an endpoint Node does not exist or the endpoints are invalid. Dataplane state is
not on the Link: it is visible through the endpoint Nodes' `ConnectivityReady` condition and
the `clabwire` container log.

```bash
kubectl get topologies.c9s.run
```

This shows the Topology state (`deploying`, `running`, `degraded`, or `deployfailed`) and
readiness. The readiness refers to the current definition only once `status.observedGeneration`
equals `metadata.generation`. `status.error` names a compile error or name conflict that blocks
the Topology.

## Logs and shell access

Unqualified `kubectl logs` and `kubectl exec` target the device container:

```bash
kubectl logs deploy/srl1
kubectl exec -it deploy/srl1 -- sr_cli
```

The other containers of the Pod:

```bash
kubectl logs deploy/srl1 -c planner    # preparation: staged and preserved files
kubectl logs deploy/srl1 -c clabwire   # connectivity: wires, carrier, management mesh
```

Device Pods carry the labels `c9s.run/topologyNode` (the Node name) and, for Nodes compiled from
a Topology, `c9s.run/originalNodeName` (the node name the containerlab definition used), so a
Pod can be mapped back to its node without reading the Node. A grouped Pod carries the primary
Node's values:

```bash
kubectl get pods -l c9s.run/originalNodeName=R1
```

Chassis cards and grouped nodes are separate application containers. List them with
`kubectl get pod <pod> -o jsonpath='{.spec.containers[*].name}'` and select one with `-c`.

## Packet capture

The connectivity sidecar streams packets from the lab-link interfaces it realized for a Node.
Name the Node and one or more of its interfaces:

```bash
kubectl exec deploy/srl1 -c clabwire -- /clabernetes/manager node-runtime packet-capture \
  --nodeName srl1 --interface e1-1 --duration 30s > e1-1.pcap
```

The output is a pcap file for Wireshark or `tcpdump -r`. For several interfaces in one stream,
repeat `--interface` and ask for pcapng, which names each interface in its own Interface
Description Block:

```bash
kubectl exec deploy/srl1 -c clabwire -- /clabernetes/manager node-runtime packet-capture \
  --nodeName srl1 --interface e1-1 --interface e1-2 --format pcapng --duration 30s > srl1.pcapng
```

List the interfaces you can capture, with the peer at the other end of each link:

```bash
kubectl exec deploy/srl1 -c clabwire -- /clabernetes/manager node-runtime packet-capture \
  --nodeName srl1 --list-interfaces
```

```json
[{"node":"srl1","interface":"e1-1","alias":"ethernet-1/1","peerNode":"srl2","peerInterface":"e1-1"}]
```

- `--nodeName` is the Node object name. Nodes that share a Pod are each selected by their own
  name; `--nodeID <Node UID>` works too. Without either, `--list-interfaces` lists every Node in
  the Pod.
- `--interface` takes the interface name or the alias the Link used, such as `ethernet-1/1`
  for SR Linux `e1-1`. If any requested interface is not a link of that Node, nothing is
  captured. The management interface is never capturable.
- `--duration` (at most 1h) and `--packetLimit` (at most 1,000,000, counted across all
  interfaces) bound the capture; the first bound reached ends it. `--snapLength` defaults to
  256 KiB and takes 64 B to 1 MiB.
- Several captures may run on one Pod at once. To capture for longer than an hour, start the
  next capture before the previous one ends; each call writes a complete stream with its own
  header.
- Each capture writes JSON audit records with status `Started`, `Succeeded`, `Failed`, or
  `Denied` to stderr. Packet data goes only to stdout.

### Stop a capture when the client goes away

Kubernetes does not stop an exec'd process when `kubectl` exits or its connection drops. A
capture you interrupt with Ctrl+C keeps running in the Pod until its next write fails or its
bound is reached. Attach stdin and add `--stop-on-stdin-close` so the capture ends as soon as
the client is gone:

```bash
kubectl exec -i deploy/srl1 -c clabwire -- /clabernetes/manager node-runtime packet-capture \
  --nodeName srl1 --interface e1-1 --format pcapng --duration 1h --stop-on-stdin-close \
  | wireshark -k -i -
```

- The capture reads stdin and ignores its bytes. When stdin reaches EOF or a read fails, the
  capture ends like a reached bound: packets already captured are written, the exit status is
  0, and the final record is `Succeeded` with `reason` `client closed: stdin reached EOF`.
- Kubernetes closes the process's stdin when the client closes its stdin or the exec
  connection ends. Both transports deliver it: the websocket protocol `v5.channel.k8s.io`
  (sends a close signal for stdin; Kubernetes 1.29 or newer) and SPDY.
- Use the flag only with stdin attached (`kubectl exec -i`). Without `-i`, stdin is empty and
  the capture would end at once. Without the flag, stdin is not read.
- When the reader of stdout goes away, the next write fails and the capture ends the same way
  with `reason` `client closed: writing stdout: ...`, whether or not the flag is set.

## Network policies

Device Pods talk to each other over UDP: port 14789 carries the management mesh and port 14790
the link wires. The connectivity sidecar also watches Links through the Kubernetes API and
answers the kubelet's readiness probe on TCP 14791. If NetworkPolicies restrict traffic in a
lab namespace, allow both UDP ports between device Pods, TCP 14791 from the nodes, and egress
to the API server.

## Related

- [Nodes and Links](/docs/concepts/nodes-and-links)
- [Link wire semantics](/docs/guides/link-wire)
- [Persistent storage](/docs/guides/persistence)
- [Image pulling](/docs/guides/image-pull#troubleshooting)
