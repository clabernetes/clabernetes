# Tasks: Sidecar-Owned Connectivity, Daemonless

## 1. Backend validation and dependencies

- [x] 1.1 Promote `github.com/google/nftables` to a direct dependency and build a minimal NAT operations seam (dedicated table, dstnat/srcnat chains at priorities ahead of x_tables) behind an interface alongside `LinkOperations`
- [x] 1.2 Validate nftables precedence equivalence against the Spike 2 flows (SNAT both shapes, DNAT, coexistence with device-programmed iptables chains); record the result as evidence
- [x] 1.3 Add the `SIOCETHTOOL` TX-checksum-offload helper to `internal/directruntime/links_linux.go` with a unit test

## 2. Allocation and plan schema

- [x] 2.1 Make `compileDirectManagement` always produce a complete identity: apply containerlab's default management subnet and gateway convention when no policy is declared, remove the empty-result path, and fail closed on any node whose identity cannot be allocated; unit tests
- [x] 2.2 Add `ManagementInterfaceSelector` value `Interposed` and the additive `ManagementPlan.Interposition` struct (device-leg name, MAC, gateway addresses, declared inbound ports, cluster transport CIDRs) to `deviceplan/types.go` + codec validation/normalization + deepcopy
- [x] 2.3 Derive the interposition profile in `deviceplan` from the evaluated containerlab node config (interface name defaulting to the containerlab primary-interface contract, MAC when the kind sets one); keep `kind_agnostic_test.go` passing; extend `registry_conformance_test.go` so every live registry kind yields a complete profile
- [x] 2.4 Render management identity at plan time: route allocated address + gateway through `applyManagementInput` during planning, remove `completeRuntimeManagement`'s Pod-identity synthesis (keep runtime DNS discovery), and remove the now-dead Pod-address recording path; update `management_internal_test.go`

## 3. Sidecar interposition runtime

- [x] 3.1 Implement the interposition stage in `internal/directruntime`: rename the CNI interface to the sidecar-owned name preserving addresses/routes, create the synthetic veth pair with plan-specified name/MAC, assign the management address to the device leg and the gateway to the router leg
- [x] 3.2 Apply the unconditional baseline: transport policy table + `iif`/`to` rules, device-leg `forwarding=0`, router-leg `accept_local=1`, namespace forwarding, TX offload off on both legs
- [x] 3.3 Program NAT through the seam from 1.1 (both source shapes + declared-port DNAT) and wire the stage into `runConnectivity` before device start, gated on the plan selector; keep `validateManagementPodTransportOverlap` failing closed
- [x] 3.4 Add re-assertion to the revision tick: reconcile only sidecar-owned rules/routes/sysctls/NAT idempotently after device mutations; never touch device-owned state
- [x] 3.5 Add readiness conditions (`CNIUnderlayPreserved`, `ManagementTranslationReady`) to the readiness markers consumed by the startup/readiness probes
- [x] 3.6 Unit tests with extended fakes covering: interposition ordering before device start, re-assertion after simulated device mutation, pod-IP-change re-render, and fail-closed paths

## 4. Sidecar fabric and host links

- [x] 4.1 Implement in-pod stitched VXLAN fabric in `internal/directruntime`: per-interface veth device leg + VTEP (VNI from `TunnelID`, remote from `PeerTransport` via the resolver seam) + tc redirects, with MTU from the plan; converge on the revision tick when peers move
- [x] 4.2 Implement in-pod slurpeeth realization as a sidecar-managed process matching the legacy launcher behavior
- [x] 4.3 Implement host-Link realization through the read-only worker netns handle (veth end moved and named in the worker namespace); renderer mounts the handle only when the plan contains host Links
- [x] 4.4 Replace `reconcileDaemonEndpoints` with the sidecar realization paths; remove the `HostEndpointReconciler` seam; unit tests for fabric convergence, peer restart, and host-Link lifetime coupling

## 5. Daemon removal

- [x] 5.1 Delete `internal/hostendpoint/` and its tests; delete the hidden `device-runtime host-endpoint-daemon` CLI subcommand; remove the daemon socket volume/mount from the renderer and its Options wiring
- [x] 5.2 Delete `charts/clabernetes/templates/host-endpoint-daemonset.yaml` and related values/RBAC; regenerate golden chart fixtures; ensure `make lint` chart checks pass
- [x] 5.3 Sweep the tree for dead references (`hostendpoint`, daemon socket paths, DaemonSet docs strings) and update `docs/architecture.mdx` and management docs for the daemonless model; document the one-time upgrade cleanup for daemon-era worker state

## 6. Cluster validation and conformance evidence

- [x] 6.1 Deploy to the kind cluster (`make try-c9s` flow with locally built images) with an SR Linux topology using a management policy subnet + fixed `mgmt-ipv4`; verify device sees the policy address, Pod transport invariants, outbound SNAT, Service/DNAT SSH, and cross-worker fabric traffic
- [x] 6.2 Repeat for SR-SIM (25.10/26.7 with licenses) and cEOS (main-table hijack survival, chain precedence); validate a topology with no management policy gets containerlab default-subnet identities
- [x] 6.3 Validate forced deletion and worker inspection: no daemon, no sockets, no residue; multi-worker recovery e2e passes against the sidecar owner
- [x] 6.4 Update/extend e2e in `e2e/topology/direct/` for the daemonless model

## 7. Wrap-up

- [x] 7.1 Record identified upstream containerlab contributions in evidence notes
- [x] 7.2 Run `make lint`, `make test`, `make test-race`, regenerate and inspect all generated artifacts, `make check-docs`; state which checks ran in the final report
