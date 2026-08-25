---
title: Link MTU
description: Link MTUs just work on any cluster; the Pod network MTU only affects wire efficiency.
---

This guide explains what MTU your lab links get and how the Kubernetes Pod network MTU relates
to it (short version: it doesn't bound anything).

## What happens by default

Every link gets **containerlab's default link MTU of 9500** unless the topology requests
something else — the same value the lab would have under containerlab on a bare host. This
holds on any cluster, including a typical 1500-byte Pod network: links between device Pods
cross the Pod network through the Clabernetes wire, a sidecar-to-sidecar transport that
segments each frame to whatever the local Pod network carries and reassembles it at the far
end. The Pod network MTU never bounds a link's MTU, and there is nothing to configure.

Both ends of a link always present the same MTU, so devices and protocols that derive behavior
from the interface MTU (IS-IS hello padding, OSPF MTU checks, TCP MSS) work self-consistently
without topology changes.

## Requesting a specific MTU

An explicit MTU on a link is honored exactly:

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

## How the Pod network MTU still matters: efficiency

The wire fragments each frame to the local Pod network MTU. A 9000-byte frame over a
1500-byte Pod network crosses as about seven UDP datagrams and is reassembled on arrival; over
a jumbo (9000+) Pod network it crosses as one. Fewer fragments mean less per-frame overhead and
less exposure to underlay packet loss (a frame is dropped whole if any one of its fragments is
lost — see the [link wire semantics](/docs/guides/link-wire) guide). Raising the Pod network MTU is
therefore an optional performance improvement, never a correctness requirement.

Mixed worker MTUs need no special handling: each sender fragments to its own Pod network MTU,
and reassembly does not care what size the fragments were.

## Links that never cross the Pod network

Links with both endpoints in the same Pod, loopbacks, and host links are plain kernel
interfaces. They follow the same MTU rules — requested value honored exactly, containerlab
default when unset — without any wire involvement.
