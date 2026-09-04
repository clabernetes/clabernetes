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
