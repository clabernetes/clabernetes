# Revise NAT precedence: device-owned NAT before the interposition fallback

## Why

Kind-conformance testing on a live cluster showed the sidecar's destination-translation
precedence breaks Docker fidelity: a connection receives exactly one destination-NAT binding,
and the interposition dstnat chain (hooked at priority −110) consumed it before device-owned
x_tables/iptables-nft chains (fixed at −100) were ever consulted. Under Docker the device's own
NAT is the only destination translation in its namespace, and vrnetlab-style wrappers rely on
it to forward declared management ports onward to a nested guest — the old precedence broke
their Pod-IP management path entirely (MikroTik ROS) and stalled NX-OS/Catalyst SSH sessions
after password submission.

## What Changes

- The sidecar's destination-translation chain hooks at priority −90 (after every device-owned
  x_tables/iptables-nft PREROUTING chain at −100) instead of −110, so device-owned destination
  NAT is evaluated first and the sidecar's declared-port translation binds only flows the
  device leaves untranslated.
- Source-translation precedence is unchanged (srcnat at 90, ahead of device-owned POSTROUTING
  at 100): the Pod-identity source invariants must bind before a device-owned masquerade can
  pin a flow to a management-scoped source.
- The direct-connectivity spec sentence "translation state MUST take precedence over any
  packet-translation state a device programs in the shared namespace" is narrowed to
  source-translation state; destination translation becomes an explicit fallback.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `direct-connectivity`: the management interposition translation requirement changes from
  "sidecar translation state MUST take precedence over any device-programmed packet
  translation" to "device-owned destination-NAT chains MUST be evaluated before the sidecar's
  declared-port destination translation, which acts as the fallback; sidecar source-translation
  state keeps precedence."

## Impact

- `internal/directruntime/nat_linux.go`: `interpositionDestinationPriority` −110 → −90
  (implemented on `direct-c9s`, commit d05f115).
- No API, chart, or configuration surface changes.
- Behavior change is observable only for devices that program their own destination NAT in the
  shared namespace; devices without NAT rules see identical behavior. Validated live on
  `nokia_srlinux` (no device NAT — unchanged), `cisco_n9kv`/`cisco_cat9kv` (SSH stalls
  resolved), and `mikrotik_ros` (device DNAT now reachable).
