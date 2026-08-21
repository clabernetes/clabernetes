# Evidence: srl-telemetry-lab full-stack integration (post-completion conformance)

Date: 2026-08-21. Cluster: kind `c9s-direct-links` (2 workers), manager `daemonless-12`.

Deployed the unmodified upstream [srl-telemetry-lab](https://github.com/srl-labs/srl-telemetry-lab)
(`st.clab.yml`, client links at MTU 1400) through `clabverter --naming non-prefixed` as a Topology
resource: 5x SR Linux 25.10 (2 spine ixr-d3l, 3 leaf ixr-d2l), 3 multitool clients, gnmic,
prometheus, grafana, alloy, loki — 13 Nodes, 9 vxlan Links, custom management subnet
`172.80.80.0/24` with fixed per-node `mgmt-ipv4`.

## Defect found and fixed

The interposition contract derived `InboundPorts` only from planned container ports, so the
auto-expose default NOS port set (22, 57400, 830, ...) was Service-exposed but never DNAT-translated
to the interposed management address. gnmic -> `leaf1:57400` was refused on every SR Linux node.
Earlier conformance missed this because SR Linux also runs an sshd in the Pod root namespace, so the
SSH checks succeeded without the translation ever existing; declared ports (`gnmic:9273`,
`alloy:1514/udp`) always worked.

Fix: `ManagementInput.InboundPorts` (additive schema field) carries the controller's auto-expose
default set into planning; `interpositionContract` merges it after planned container ports with
Pod-wide claim semantics (a declared port of any group member is never shadowed). Auto-expose
disabled keeps translation to declared container ports only. `deviceplan` and `controllers/node`
tests cover merge, dedup, claim, validation, and profile gating.

Operational note: the planner rejects unknown input fields, so the manager image and
`DEVICE_RUNTIME_IMAGE` must move together on upgrades.

## Verified end to end

- All 13 Nodes ready in under 50 s; readiness gates green.
- SR Linux fabric: eBGP underlay + iBGP EVPN overlay established on all nodes over the in-pod
  stitched VXLAN transport (mixed same-worker and cross-worker).
- Client dataplane: IPv4 + IPv6 0% loss client1<->client2/3 across the EVPN L2 domain.
- containerlab `exec` directives applied (client addressing, iperf3 servers); binds delivered via
  `filesFromConfigMap`.
- gnmic: gNMI/TLS subscriptions to all 5 nodes via `<node>:57400` service DNS (containerlab cert
  parity: `clab-profile` issued by the namespace device CA).
- prometheus scrapes `gnmic:9273` (242 interface-state series); grafana healthy, both datasources
  connected, Network Telemetry dashboard provisioned.
- Syslog: SR Linux resolves `alloy` and exports UDP/1514 through the interposition DNAT; loki holds
  streams for all five sources.
- Traffic (`traffic.sh start all` equivalent): 2x 8 IPv6 streams at 200 Kbit/s each, 0 retransmits
  at t=138 s; prometheus shows matching `interface_traffic_rate` with ECMP spread over both spines.
