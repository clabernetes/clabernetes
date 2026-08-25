---
title: Link MTU
description: How Clabernetes sizes link MTUs over the Kubernetes network and what to change for larger frames.
---

This guide explains what MTU your lab links get, why, and what to change when your lab needs
larger frames.

## What happens by default

A link between two device Pods crosses the Kubernetes Pod network inside a VXLAN tunnel. That
encapsulation adds 50 bytes to every frame, so the largest frame a link can carry is:

```
effective link MTU = Pod network MTU - 50
```

On a typical cluster with a 1500-byte Pod network, every cross-Pod link therefore presents a
**1450-byte MTU** to the devices. Clabernetes applies that one effective value to every
interface in the path — the device-facing interface, the sidecar plumbing, and the tunnel — so
the interfaces never disagree. Devices and protocols derive their behavior from the interface
MTU they see (IS-IS hello padding, OSPF MTU checks, TCP MSS), and because both ends of a link
always agree, self-consistent labs work without any topology changes.

Links that never cross the Pod network — both endpoints in the same Pod, loopbacks, and host
links — are not encapsulated and are not bounded this way.

## Requesting a specific MTU

An explicit MTU on a link is honored exactly whenever it fits the Pod network:

```yaml
apiVersion: c9s.run/v1alpha1
kind: Link
metadata:
  name: leaf1-eth1-leaf2-eth1
spec:
  endpointA:
    nodeName: leaf1
    interfaceName: eth1
  endpointB:
    nodeName: leaf2
    interfaceName: eth1
  mtu: 1400
```

(or `mtu:` on a link in a containerlab topology consumed by the Topology compiler.)

If the requested MTU is larger than the Pod network can carry encapsulated, Clabernetes bounds
it to the effective maximum instead of programming an MTU whose frames the tunnel would
silently drop. The connectivity sidecar reports the clamp in its container log, including the
Pod network MTU that would be required to honor the request:

```
kubectl logs <device pod> -c connectivity
```

## Running labs that need 1500-byte or jumbo frames

The lab-side MTU ceiling is set by your cluster's Pod network MTU, which Clabernetes cannot
change. To honor a lab MTU of `N`, the Pod network MTU must be at least `N + 50`:

| Lab needs | Pod network MTU must be at least |
| --- | --- |
| 1500 | 1550 |
| 9000 | 9050 |
| 9500 (containerlab's veth default) | 9550 |

Where the Pod network MTU comes from is CNI-specific — for example `mtu` in the Cilium
ConfigMap, `veth_mtu` for Calico, or the interface MTU the CNI auto-detects from the worker's
network. The underlying worker network (switches, VM vNICs, cloud VPC) must also carry the
larger size, and the CNI's own encapsulation overhead, if any, comes on top.

## Known limitation: mixed worker MTUs

Each Pod bounds its link MTUs against its **own** underlay. If workers have different network
MTUs, the two ends of a link can realize different values and frames larger than the smaller
end may drop in one direction. Keep worker network MTUs uniform across the cluster. A
controller-negotiated per-link minimum is the planned upgrade path for mixed-MTU clusters.
