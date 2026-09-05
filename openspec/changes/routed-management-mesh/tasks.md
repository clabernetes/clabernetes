# Tasks: Routed Management Mesh

## 1. Peer directory contract

- [x] 1.1 Extend the peer directory entry with the Pod address; add the fixed shard count,
      shard naming, stable shard assignment, shard rendering, and merged shard reading helpers;
      add the deterministic tunnel-endpoint MAC derivation from a management IPv4 address; unit
      tests.
- [x] 1.2 Read the directory from shards at the launch boundary and in the sidecar, caching by
      file fingerprint so unchanged ticks do not re-parse; hosts rendering consumes the parsed
      peers; unit tests.

## 2. Plan contract

- [x] 2.1 Remove the peer-discovery transport name from the interposition mesh contract and its
      validation; update mapper, codec, controller, and sidecar tests.

## 3. Controller

- [x] 3.1 Carry Pod addresses into the namespace peer directory from the Pod cache (newest
      non-terminating Pod per direct-node UID), render the fixed shard set, write only changed
      shards, and delete the legacy single ConfigMap; unit tests.
- [x] 3.2 Remove the headless mesh discovery Service (render, reconcile, constants) and delete a
      stale one on namespace reconcile; remove the mesh-member Pod label; unit tests.
- [x] 3.3 Project the shard ConfigMaps into the existing peer-directory volume (all optional);
      renderer tests.

## 4. Sidecar realization

- [x] 4.1 Replace the bridge shape with one synthetic pair (device leg ↔ router leg carrying the
      gateway identity) and a routed VTEP (learning off, MAC derived from the own management
      IPv4, no flood entries); transport table carries the own address via the router leg and the
      subnet via the VTEP; sysctl baseline gains proxy ARP with zero delay on the router leg and
      drops the bridge entries.
- [x] 4.2 Converge per-peer state from the spec: permanent neighbor entries and forwarding entries
      on the VTEP, NDP proxy entries on the router leg for IPv6 peers, exact stale removal;
      reconcile on directory change, on the cold pass, and on a periodic resync tick.
- [x] 4.3 Move the management segment clamp to an `inet` forward chain matching ingress on the
      router leg and the VTEP for both address families, keeping the unsupported-kernel
      tolerance; unit tests.
- [x] 4.4 Extend the isolated-namespace interposition test to the routed shape: pair, VTEP
      identity and MTU, table routes, proxy ARP sysctls, peer entry reconciliation (add, shrink,
      unchanged), idempotency, and re-assertion after a device strips routes.

## 4b. Findings from kind conformance and scale runs

- [x] 4b.1 Router leg address without a kernel prefix route; sweep leftovers (single-namespace
      replies chose the router leg's connected route: IOL IPv6).
- [x] 4b.2 IPv6 mesh state only while the router leg carries the IPv6 gateway (EOS disables
      IPv6 in the namespace); IPv6 device-leg addressing pre-boot only (SR-SIM keeps ownership).
- [x] 4b.3 Ingress-scoped own-address rules for kernel-held addresses ahead of a re-homed local
      lookup (SONiC moves the local rule; vrnetlab NAT is bound to the device leg).
- [x] 4b.4 Sidecar conntrack zone for the sidecar legs, locally originated traffic, and
      management-sourced ingress on any interface (double netfilter traversal defeated
      vrnetlab's DNAT; SR Linux resolver queries leave through its internal gateway pair and
      their replies never met a zone-0 masquerade); early demux off in the Pod namespace
      (socket-owned replies were dropped in the forward path).
- [x] 4b.5 HTTP readiness endpoint on TCP 14791 with a transport-filter accept; paced
      interposition re-assertion; compare-before-write sysctls.
- [x] 4b.6 Imported packages receive the management subnets (vrnetlab `DOCKER_NET_V4_ADDR`).
- [x] 4b.7 Gateway hairpin for kernel-held addresses: gateway-bound replies of a nested guest
      (vrnetlab) cross the pair to the router leg, where the Pod-address translation reverses
      (the Pod-address path to vr-sros was closed without it).
- [x] 4b.8 Direct watchdog pass paced by readiness: a ready Node re-runs its pipeline every
      five minutes (jittered) instead of every minute; at 64 idle Nodes the manager spent
      about 92 m CPU and 3 uncached API reads per second on the minute pace.
- [x] 4b.9 Device leg brought up at creation only: a per-tick `LinkSetUp` raced SR Linux's
      down/rename/up of its management interface (one of five SR Linux nodes lost gNMI after
      a runtime rollout while SSH and ping, answered by the pod kernel, kept it ready).
- [x] 4b.10 Device-leg blackhole keyed on the leg's current name, resolved through the router
      leg's veth peer (SR Linux renames the leg; a name-keyed rule detached and the device's
      consumed frames looped through the router leg, aborting the sidecar's SSH readiness
      dials with time exceeded).
- [x] 4b.11 Sidecar probe sockets marked and steered across the pair for kernel-held addresses
      (the ingress-scoped rules of 4b.3 left the sidecar's own readiness dial to local delivery,
      which vrnetlab's device-leg-bound translation never saw: vr-sros SSH readiness was refused
      while external SSH through the pod address worked).
- [x] 4b.12 Segment clamp also on transport ingress (exposed-port sessions): SR-SIM raises its
      leg to MTU 9000 and sized segments from a Pod-network client's SYN; the router leg
      dropped them silently and an SSH session through the pod address never showed a prompt.

## 5. Documentation and validation

- [x] 5.1 Update the architecture, installation, lab operations, and MTU documentation for the
      routed shape, the directory shards, the removed Service, and the broadcast limitation.
- [x] 5.2 `make lint`, `make test`, and the isolated-namespace tests; validate the change with
      `openspec validate`.
- [x] 5.3 Cluster validation on a multi-worker cluster with the srl-telemetry-lab: every node
      ready, gnmic streaming from all routers by name, prometheus scraping gnmic, grafana
      reaching prometheus, cross-worker management ping and TCP, DF ping at and above the mesh
      path size showing fragmentation-needed, peer reschedule convergence, and no legacy
      discovery objects left in the namespace.

## Validation evidence (2026-09-04, k8s-vms: 3 nodes, Calico, Pod MTU 1480)

- Unit: `make lint` clean; `make test` green except the pre-existing
  `TestPackageGeneratedDirectoryMetadataFlowsWithoutKindMapping` xattr failure; the isolated
  network-namespace interposition, clamp, NAT, and transport-filter tests ran as root.
- srl-telemetry-lab (13 nodes, namespace `st`): all ready; gnmic streams from all five routers
  by name (about 520 series per leaf, 460 per spine); prometheus scrapes gnmic with fresh
  samples; grafana healthy with both datasources; every peer pingable by name.
- Kernel shape per Pod: `eth0` paired with `c9sr0` (gateway MAC), `c9sm0` routed VTEP with
  learning off and MAC `06:c9:<mgmt IPv4>`, transport table `own/32 dev c9sr0` plus
  `172.80.80.0/24 dev c9sm0`, twelve neighbor and twelve forwarding entries, no bridge,
  proxy ARP on `c9sr0` with zero delay; peers resolve to the gateway MAC in the device ARP table.
- MTU: mesh elements at 1430; DF ping 1402 passes, 1403 answered with fragmentation-needed
  (mtu 1430); with the device port raised to 1500 the SYN leaves the device with mss 1460 and
  arrives at the peer with mss 1390 (clamp verified on the wire); iperf3 over management
  262 Mbit/s same worker, 207 Mbit/s cross worker.
- Cross worker: client2 and spine2 moved to the other worker; directory shards and every peer's
  forwarding entries followed; ping, DF ping, ssh and gNMI ports, iperf3, and an SR Linux
  originated management ping all pass across workers; gnmic resubscribed to spine2 on its own.
- Reschedule convergence: a recreated client3 Pod was addressed after 3 s and reachable from
  peers 74 s later (kubelet ConfigMap sync bound), then ready and pingable by name.
- Legacy objects: no `c9s-management-mesh` Service and no single `c9s-peer-directory`
  ConfigMap remain in the namespace.

## Kind conformance and scale evidence (2026-09-04, same cluster)

- Kinds, each with IPv4 and IPv6 management policies and a probe Pod pinned to the other
  worker (ping by name, IPv6 ping, TCP 22, DF ping at and above the IPv4 and IPv6 mesh path
  sizes): cisco_iol, arista_ceos, nokia_srsim, sonic-vs, juniper_crpd, vjunosevolved,
  fortigate, cisco n9kv, c8000v, xrv9k, and nokia_sros (vr-sros). Every kind passes on the
  mesh; SSH over IPv6 stays closed where the device does not enable it (Cisco VMs); EOS
  disables IPv6 in the namespace and keeps an IPv4-only mesh; the sonic-vs image ships no
  sshd (ICMP verified).
- Pod-address path (Service exposure): verified into a kernel-held Linux node, a device-held
  node (SR-SIM), the vr-sros, n9kv, and c8000v nested guests (which needed the gateway hairpin
  of 4b.7), and cEOS. xrv9k passed the mesh checks before 4b.7 and was not re-verified after
  it: its 25-minute boot did not fit the remaining cluster time.
- Scale (30 single-container nodes, one namespace): every Pod ready 54 s after the Topology
  was applied; 8 directory shards; every Pod holds 29 neighbor and 29 forwarding entries;
  sidecar CPU 7 m per Pod at rest (130 m before the HTTP readiness endpoint and the paced
  re-assertion of 4b.5), API-server load at rest negligible.
- Scale (100 nodes, same cluster, other workloads present): 87 Pods ready 110 s after the
  Topology was applied; the remaining 13 never scheduled because the cluster's two workers
  ran out of Pod slots (110 each, 99 taken by other workloads), not because of the mesh. The
  ready Pods held one neighbor and one forwarding entry per scheduled peer (86) and all 99
  peer names; the eight shards were 719 to 842 bytes each; sidecar CPU 7 m average and 9 m
  maximum per Pod; manager 155 m while the namespace still had unschedulable Pods. A
  deploy wave also creates two short-lived helper Pods per node (planner and image pull),
  which compete for Pod slots with the node Pods during the wave.
- Scale (64 alpine nodes at 10 m / 16 Mi, no persistence, 80 % of the cluster's Pod slots):
  every Pod ready 81 s after the Topology was applied; adding a 65th node: Pod addressed
  after 7 s, ready after 34 s, reachable by name in both directions after 35 s. Every Pod
  converged to 63 peer entries; two sampled Pods still lacked 8 and 13 entries a few minutes
  after the wave (kubelet volume sync under the wave's load) and were complete afterwards.
  Sidecar CPU 7 m average per Pod at rest, 27 m peak while a Pod boots. Manager 92 m at rest
  before 4b.8, driven by the minute watchdog pass over 64 Nodes.
- Device leg ownership (4b.9): after the runtime rollout that carries the fix, all five SR
  Linux nodes of the telemetry lab show `mgmt0` as the pair's device leg with `mgmt0.0` inside
  their management namespace, every gNMI port answers, and gnmic streams from all five again
  (before the fix one node of five came up without its management interface after a rollout).
- Scale (65 idle Nodes, after 4b.8): manager CPU 3 m at rest, down from 92 m; the API traffic
  in a 60 s window at rest is dominated by lease renewals (241 lease PUTs) rather than the
  per-Node uncached reads that the minute pass had made the top cost. Sidecar CPU unchanged at
  7 m average.
