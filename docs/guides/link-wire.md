---
title: Link wire semantics
description: What the cross-Pod link transport emulates faithfully — carrier, loss, jumbo frames — and what it does not.
---

Links between device Pods cross the Kubernetes Pod network through the Clabernetes **wire**: a
transport between the connectivity sidecars of the two Pods that carries whole Ethernet frames
over UDP. This guide describes what that wire emulates like a physical cable, so you know which
lab behaviors you can trust, and where the emulation deliberately stops.

## What the wire emulates faithfully

### Carrier

Pulling a port behaves like unplugging a cable. When a device takes a link interface down —
admin-down, `shutdown`, interface deletion — the peer device sees **loss of carrier**
(`NO-CARRIER` / `LOWERLAYERDOWN`) on its own interface within milliseconds, while that
interface stays administratively up. Protocols reconverge on carrier loss, not on hold-timer
expiry. When the interface returns, carrier restores the same way.

The same applies to endpoint loss: if a device Pod dies, every link terminating on it goes
carrier-down at its peers within the wire's heartbeat timeout (a few seconds). When the Pod is
rescheduled and converges, carrier restores without any manual action.

The signal cannot lie about the datapath: carrier advertisements, liveness heartbeats, and the
link frames themselves share one socket and one network path, so a link never shows carrier
while its frames have no way through.

### Loss

The wire preserves datagram semantics end to end. A frame is either delivered whole or dropped
whole; nothing is retransmitted, acknowledged, or reordered by recovery logic. Packet loss you
inject or that the cluster network produces stays visible to the lab exactly once, which keeps
loss testing, BFD behavior, and convergence measurements meaningful. (Contrast a TCP-based
transport, where the wire itself would repair loss and add head-of-line latency artifacts.)

One consequence of fragmentation is loss amplification for large frames: a 9500-byte frame
crossing a 1500-byte Pod network is about seven fragments, and the loss of any one drops the
whole frame — so jumbo-frame loss rate is roughly the underlay loss rate multiplied by the
fragment count. Real jumbo links amplify bit-error rates similarly, but keep it in mind when
measuring loss with large packets over a lossy underlay.

### Any MTU, anywhere

Frames up to the link MTU — containerlab's 9500-byte default included — cross any cluster
regardless of the Pod network MTU, because the wire fragments to whatever the local underlay
carries. See the [Link MTU](/docs/guides/mtu) guide.

### Transparency

The wire is a transparent L2 pipe: it forwards frames with arbitrary source and destination
MACs, VLAN tags, and EtherTypes, so bridging, LACP-free bonds, and tagged sub-interfaces behave
as on a direct cable.

## What the wire does not emulate

- **Timing fidelity.** Latency and jitter are those of the Kubernetes Pod network plus a
  userspace forwarding step in each sidecar — not a calibrated link. Do not benchmark
  microsecond-scale timing behavior across cross-Pod links.
- **Line-rate throughput.** Every cross-Pod frame is handled in userspace by both sidecars.
  Throughput is far above what software network OS dataplanes forward, so it is virtually never
  the bottleneck in a lab, but it is not a kernel-offloaded path and will not carry multiple
  gigabits per second per link.
- **Strict ordering under extreme conditions.** Fragments of a frame travel back-to-back on one
  path and reordering is rare, but the wire does not resequence frames the underlay reorders.

## Observability

The connectivity sidecar logs wire events — carrier transitions, peer sessions, and periodic
per-link drop counters classified by cause (reassembly expiry, memory cap, stale peer
generation, oversize, send-queue full):

```
kubectl logs <device pod> -c device-connectivity
```

Links inside one Pod (same-Pod, loopback) and host links are plain kernel interfaces and do not
use the wire; their carrier and loss behavior is native Linux behavior.
