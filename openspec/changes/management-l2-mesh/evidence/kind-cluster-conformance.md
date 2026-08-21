# Evidence: management mesh kind-cluster conformance (srl-telemetry-lab)

Date: 2026-08-21. Cluster: kind `c9s-direct-links` (2 workers, WSL2 kernel), manager
`daemonless-16`, upstream srl-telemetry-lab (13 nodes: 5x SR Linux 25.10, 8x linux kinds, mgmt
`172.80.80.0/24` with fixed addresses) deployed as a Topology resource.

## Cluster-found defects fixed during conformance (beyond the spike)

1. **Peer traffic from single-namespace kinds hijacked by the management policy rule.** The
   `to <mgmt-subnet>` rule (pref 902) pulled peer-address traffic of linux kinds into the
   isolated gateway leg. SR Linux (device stack behind the leg in its own namespaces) worked;
   linux kinds failed. Fix: the rule covers exactly the local device `/32` — hook and
   Pod-address translation flows keep the gateway path, peer addresses fall through to the
   device leg's connected route onto the mesh. Stale subnet-wide rules converge away.
2. **ARP flux through the device leg.** The spike's device lived in a separate netns; in-cluster
   single-namespace kinds share their stack with the gateway leg's `.1`, so remote linux kinds
   flux-answered mesh-flooded gateway ARP through their (non-isolated) device port: 15 replies
   to 3 probes. Fix: `arp_ignore=1` on the device leg joins the baseline.
3. **br_netfilter re-NATs bridged frames.** Kubernetes nodes load `br_netfilter` and Pod
   namespaces inherit `bridge-nf-call-iptables=1`, so every bridged frame traversed the Pod's
   netfilter a second time after the L3 gateway hop; conntrack clash resolution rewrote source
   ports (observed: same SYN with sport 42380 on `c9sg0`, 10047 on `c9sd0`) and DNAT replies
   left the Pod untranslated (gNMI `i/o timeout` on every SR Linux target). Fix: the sidecar
   sets `bridge-nf-call-{ip,ip6,arp}tables=0` in the Pod namespace (per-netns sysctl; node
   unaffected; skipped when the module is absent).

## Verified

- Mesh realization in every Pod: bridge `c9sb0` (pinned derived MAC), isolated gateway port and
  VTEP, 12 head-end FDB entries per Pod discovered through `c9s-management-mesh` (13 endpoints,
  publishNotReadyAddresses).
- Hardcoded-address management traffic, all kind combinations: linux→SRL ping + SSH (SR Linux's
  own `srbase-mgmt` sshd banner) at `172.80.80.11`; SRL→SRL (`leaf1`→`spine1`) and SRL→linux
  (`leaf1`→`client1`) from `srbase-mgmt`; linux→linux HTTP (prometheus API at
  `172.80.80.42:9090` returning live query results).
- Gateway containment: exactly 3 replies to 3 ARP probes, all from the local deterministic
  gateway identity; peer ARP resolves the peer device's real MAC (leaf1's `1a:7d:...` device
  leg).
- Reschedule convergence: leaf3's Pod deleted, rescheduled onto the other worker with a new Pod
  address; every peer's FDB converged (new entry present, stale entry removed, zero Pod
  restarts elsewhere) and peer-address traffic resumed.
- Coexisting paths unchanged: name-based Service DNAT (`leaf1:57400` gNMI-TLS, all five gnmic
  targets subscribed, zero errors steady-state), syslog UDP export from all five SR Linux nodes
  into loki via `alloy`, prometheus scraping, grafana datasources, and the lab's iperf traffic
  (8 IPv6 streams per client pair at the configured rate) with live `interface_traffic_rate`
  telemetry.
- Regression: full direct e2e suite green on the mesh runtime — TestMultiWorkerRecoveryDirect
  230s, TestLinuxDataplaneDirect 51s, TestDirectPacketCaptureOperation 62s, TestDirectSaveOperation
  98s, TestNodeLinkDirect 120s (`ok e2e/topology/direct 350.6s`).
- Repository checks: `make lint`, `make test` (838), `make test-race` (838), compatibility
  verify, `openspec validate` clean.

Out of scope / untested here: VM-backed kinds on the mesh (same synthetic-leg contract; pending
their general direct-runtime conformance), IPv6 management over the mesh (L2 carries it, but
IPv6 remains unvalidated by design), grouped multi-member Pods.
