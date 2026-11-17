# Design: Link Impairments

## Context

containerlab programs an impairment as a root `netem` qdisc on the endpoint interface, for the
packets that interface sends. In c9s that interface is realized and owned by the connectivity
sidecar of the device Pod, which already re-asserts other state it owns on every pass.

## Decisions

### The impairment lives on the Link endpoint

An impairment is a property of the wire as the user drew it, so it belongs in Link spec next to
the MTU, not in a Node or in status. Each endpoint carries its own, because netem only shapes
egress: impairing both directions takes one per side, exactly like containerlab. A host endpoint
cannot be impaired, since its interface lives in the worker namespace; the node endpoint can.

### Impairment-only changes are Live

The sidecar reprograms a qdisc on the running interface; the device never notices. The
connectivity transition therefore separates interfaces whose only change is their impairment:
they mark the transition changed and their Node affected, but do not raise the required mode to
the Link's declared mode. Any other endpoint change keeps its existing classification, so an
impairment cannot hide an MTU change behind a Live revision.

### The sidecar owns its qdisc through a handle

The sidecar installs its qdisc with the fixed handle `c9:` and treats only a root netem qdisc
with that handle as its own. When a Link declares no impairment, the sidecar removes its own
qdisc and leaves any other root qdisc alone, so an impairment a user or device installed by
hand (for example with `tc` in a linux-kind `exec`) survives. When a Link declares one, it wins
and replaces whatever root qdisc is present. This keeps ownership stateless across sidecar
restarts.

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
