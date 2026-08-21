# Lab compatibility hardening — real labs deploy with minimal or no source edits

Date: 2026-08-20. Reference lab: srl-labs/srl-telemetry-lab (5-node EVPN fabric, iperf
clients, gnmic/prometheus/grafana/alloy/loki). Vocabulary audited against upstream
`schemas/clab.schema.json`.

## Warning-class compile diagnostics

`CompilerDiagnostic` gains a warning class: constructs whose loss cannot silently change lab
behavior are accepted with a logged diagnostic instead of failing the compile. Genuinely
semantic constructs keep failing closed. Converted to warnings:

- Docker-only management fields (`mgmt.network/bridge/mtu/external-access/skip-when-unused/
  driver-opts`) — accepted and ignored; the address policy fields keep compiling as before.
- Host-pinned ports (`9090:9090`, `ip:host:container`) — the host half is dropped with a
  warning naming the kept Pod-side port; host pinning only ever described the local Docker
  host.

## Group vocabulary

`group` (node field) and the `topology.groups` section are accepted: group-scoped
configuration participates in the imported containerlab inheritance rules exactly as kinds do
(proven by unit test: a group env value reaches the flattened node), and the group name rides
onto the compiled Node as the `c9s.run/topologyGroup` label. The Node CRD/OpenAPI artifacts
regenerate accordingly and the API-inventory guard covers the new field.

## Reference-lab result

With those changes, the unmodified st.clab.yml compiles with warnings only. The remaining
source edits are genuine environment/topology semantics, not converter gaps:

- inter-node ports used by the stack (`gnmic 9273`, `alloy 1514/udp`) declared so the expose
  Services carry them;
- access links declared `mtu: 1400` because the lab's own EVPN-VXLAN overlay rides the c9s
  fabric inside a 1500-byte underlay (disappears on a jumbo underlay);
- SR Linux syslog `remote-server` changed from the docker mgmt-network static address
  `172.80.80.45` to the name `alloy` — SR Linux accepts hostnames there, the runtime DNS
  completion resolves it, and the logging leg then works by name.

Verified live end to end on the same cluster: all 13 nodes Ready, EVPN fabric established,
iperf at the expected 1.78 Mbps on both access leafs measured through gNMI -> gnmic ->
Prometheus, Grafana healthy, and Loki holding syslog streams from all five SR Linux nodes
(`source` label values leaf1-3/spine1-2) delivered over the by-name UDP syslog path.

## Remaining vocabulary wired (2026-08-20)

The remaining `node-config` schema vocabulary received its deliberate disposition:

- **Wired end to end** (vocabulary type, CRD schema, imported-inheritance flattening, plan,
  renderer): `startup-delay`, `restart-policy` (`always`/`unless-stopped`; `no`/`on-failure`
  fail compile with `unsupported-restart-policy` because a device container in a shared Pod
  always restarts with it), `image-pull-policy`, `cpu` and `memory` (device-container limits;
  memory converts through the same humanize rules Docker applies, so `512m` means megabytes,
  not Kubernetes millibytes), `healthcheck` (merged over the image OCI healthcheck into
  startup/readiness probes), `aliases` (node-scoped like upstream, each realized as an extra
  same-namespace headless Service selecting the node's Pod, validated as DNS-1035 and unique
  against node names and other aliases), and `link-apply-mode` (`live`/`restart`/`recreate`).
- **Documented rejections** with dedicated compile diagnostics stating why: `runtime`,
  `auto-remove`, `pid-mode`, `cgroupns-mode`, `cpu-set`, `stages` (multi-node boot
  orchestration the direct runtime does not implement), and `credentials` (credential bytes
  belong in referenced Secrets; imported kind defaults still apply).
- **Not in the baseline**: the earlier audit listed `hostname` and `cgroup-parent`, but neither
  exists in containerlab 0.78.0's `node-config` schema or Go `NodeDefinition` -- there is
  nothing to wire or reject.

Proven by unit coverage at every layer (vocabulary subset test with the new `HealthcheckConfig`
snapshot, compile flatten/reject tests, plan mapping tests including the memory-unit
conversion, alias Service render/reconcile/prune tests) plus the direct e2e suites rerun
against a live cluster after the change. The planner-change evidence invalidation fired as
designed and the conformance reruns recorded fresh evidence.
