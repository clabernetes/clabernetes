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
- a short-lived planning Pod named `<node>-planner-...` while a new plan is computed
- a PersistentVolumeClaim named after the Node when persistence is enabled

Per namespace:

- the headless `c9s-management-mesh` Service that discovers management mesh members
- the `c9s-peer-directory` ConfigMap that maps node names to management addresses
- a `direct-device-ca-...` Secret holding the lab CA, plus one `<node>-certificates-...`
  Secret per Node that sets `certificate.issue`

Do not edit these objects. The controller owns them and reverts drift.

## Node status

```bash
kubectl get nodes.c9s.run
kubectl get nodes.c9s.run -o wide
```

`READINESS` is `ready`, `notready`, or `unknown`. The wide view adds the container,
preparation, and connectivity conditions, the plan digest, and the applied NodeProfile.
`kubectl describe` shows every condition:

| Condition | Meaning |
| --- | --- |
| `NodeProfileResolved` | the referenced NodeProfile exists |
| `PlanApplied` | the current spec was planned and rendered; when `False`, the reason names the cause, for example `PlanPending`, `ImagePullSecretMissing`, `OCIMetadataResolveManifest`, `PlanRejected`, or `DeploymentInvalid` |
| `Prepared` | the preparation init container staged and verified every file |
| `ConnectivityReady` | every interface of the cold-start plan exists |
| `ContainersReady` | every application container of the Node runs and passes its probes |
| `LinkLifecycleAction` | the action taken for the latest Link-only change: `live`, `restart`, or `recreate` |
| `DeviceStateReset` | progress of a device-state reset, see [Persistent storage](/docs/guides/persistence) |

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

Chassis cards and grouped nodes are separate application containers. List them with
`kubectl get pod <pod> -o jsonpath='{.spec.containers[*].name}'` and select one with `-c`.

## Packet capture

The connectivity sidecar streams a pcap for any interface it realized for a Node. Address the
Node by its UID and the interface by the name the Link uses:

```bash
NODE_UID=$(kubectl get nodes.c9s.run srl1 -o jsonpath='{.metadata.uid}')
kubectl exec deploy/srl1 -c clabwire -- /clabernetes/manager node-runtime packet-capture \
  --plan /var/run/clabernetes/plan/plan.json \
  --input /var/run/clabernetes/input/input.json \
  --connectivityRevision /var/run/clabernetes/connectivity-revision/revision.json \
  --nodeID "$NODE_UID" --interface e1-1 --duration 30s > e1-1.pcap
```

`--duration` and `--packetLimit` bound the capture, and the first bound reached ends it. The
output is a standard pcap file for Wireshark or `tcpdump -r`.

## Network policies

Device Pods talk to each other over UDP: port 14789 carries the management mesh and port 14790
the link wires. The connectivity sidecar also watches Links through the Kubernetes API. If
NetworkPolicies restrict traffic in a lab namespace, allow both ports between device Pods and
egress to the API server.

## Related

- [Nodes and Links](/docs/concepts/nodes-and-links)
- [Link wire semantics](/docs/guides/link-wire)
- [Persistent storage](/docs/guides/persistence)
- [Image pulling](/docs/guides/image-pull#troubleshooting)
