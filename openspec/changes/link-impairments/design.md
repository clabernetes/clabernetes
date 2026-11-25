# Design: Link Impairments

## Context

containerlab programs an impairment as a root `netem` qdisc on the endpoint interface, for the
packets that interface sends. That only works when the device sends through its interface's
qdisc: SR-SIM sends around it, so containerlab links SR-SIM with `veth-stitch` and impairs a
stitch interface it owns in the middle of the link instead. In c9s every cross-Pod wire already
has such a middle hop: device leg, veth, the sidecar-owned sidecar leg, the userspace fabric
wire, and the same in reverse on the peer. The connectivity sidecar of the device Pod realizes
and owns these legs and already re-asserts other state it owns on every pass.

## Decisions

### The impairment lives on the Link endpoint

An impairment is a property of the wire as the user drew it, so it belongs in Link spec next to
the MTU, not in a Node or in status. Each endpoint carries its own, because netem only shapes
egress: impairing both directions takes one per side, exactly like containerlab. A host endpoint
cannot be impaired, since its interface lives in the worker namespace; the node endpoint can.

### The impairment sits where no device can bypass it

An impairment applies to the packets its endpoint sends. On a cross-Pod wire those packets leave
the sending device, cross the fabric wire, and the receiving Pod's sidecar writes them into its
own sidecar leg through the kernel, without bypassing the qdisc, on their way to the receiving
device. The receiving sidecar therefore programs the impairment on that leg: the plan carries it
as the wire endpoint's `PeerNetem`, and the codec rejects it anywhere else. Measured on the
SR-SIM e2e topology, a 100ms delay on SR-SIM's own interface left the round trip at 1.1ms with
0 packets through the qdisc, while the same delay on the peer's sidecar leg raised it to 101ms.

The leg also never moves into the device namespace, is never renamed or adopted by a device,
and is not the device's to configure, so the sidecar neither loses the impairment to a device
nor fights a qdisc a device or user installs on its own interface.

The sending sidecar's own leg is no alternative: it reads the device's frames through a packet
socket, which receives them before any ingress qdisc runs. An extra stitch hop in the sending Pod
would work but adds a veth pair and a redirect per interface for the same effect.

A rate limit consequently queues and drops at the receiving end of the wire, like a slow link in
the middle of the path, rather than back-pressuring the sending device's queue. A full queue
drops the frame the wire injects, which the wire already counts as an injection drop.

Links within one Pod (`same-pod`, `loopback`) and host links have no sidecar leg, so the plan
carries their impairment as the endpoint's own `Netem` and the sidecar programs it on the
endpoint interface as containerlab does; a device that bypasses that qdisc is not impaired on
them. Giving those links a sidecar-owned stitch hop lifts that limit.

### Impairment-only changes are Live

The sidecar reprograms a qdisc on the running interface or sidecar leg; the device never
notices. A wire endpoint's impairment changes the peer's plan, so it is the peer Node's live
transition. The
connectivity transition therefore separates interfaces whose only change is their impairment:
they mark the transition changed and their Node affected, but do not raise the required mode to
the Link's declared mode. Any other endpoint change keeps its existing classification, so an
impairment cannot hide an MTU change behind a Live revision.

### The sidecar owns its qdisc through a handle

The sidecar installs its qdisc with the fixed handle `c9:` and treats only a root netem qdisc
with that handle as its own, on a sidecar leg and an endpoint interface alike. When a Link
declares no impairment, the sidecar removes its own qdisc and leaves any other root qdisc alone,
so an impairment a user or device installed by hand (for example with `tc` in a linux-kind
`exec`) survives. When a Link declares one, it wins and replaces whatever root qdisc is present.
This keeps ownership stateless across sidecar restarts.

### Changes recreate the qdisc

The kernel changes an existing netem qdisc in place and keeps the rate and corruption a request
omits, and netlink omits them when they are zero. Changing the sidecar's own qdisc in place
would therefore leave a removed rate or corruption behind, and the convergence check would
reprogram it on every pass. The sidecar deletes and re-adds its own qdisc instead.

### Re-assertion cost

The cold pass and every new revision visit all endpoints, so a restarted sidecar also removes
impairments the plan dropped. Steady-state ticks visit only the endpoints that declare an
impairment, which bounds the per-tick netlink work to the impaired interfaces.

## Limitations

- **Delay ceiling of 4m.** The vendored netlink library programs delay and jitter in 32-bit
  kernel ticks (`TCA_NETEM_LATENCY64` is not supported), which overflow after about 4.5 minutes
  on kernels with 15.625 ticks per microsecond. The API and compiler reject longer delays rather
  than letting them wrap. containerlab programs 64-bit delays, so a containerlab topology with a
  longer delay fails to compile with a located diagnostic. Upgrading the netlink library, or
  encoding the 64-bit attributes, lifts the ceiling.
- **Jitter ceiling of 2.147483647s.** The kernel caps jitter there; the API rejects longer
  jitter instead of capping it silently, like containerlab.
- **Links without a sidecar leg.** A `same-pod`, `loopback`, or host Link is impaired on the
  endpoint interface, so a device that sends around its interface's qdisc, such as SR-SIM, is
  not impaired on it. A sidecar-owned stitch hop on those links, like containerlab's
  `veth-stitch`, lifts the limit.
