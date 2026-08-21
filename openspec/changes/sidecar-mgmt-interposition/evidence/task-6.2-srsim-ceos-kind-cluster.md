# Task 6.2 — SR-SIM and cEOS daemonless validation on the kind cluster (2026-08-21)

Cluster `c9s-direct-links`, manager `daemonless-4`. Namespace `vendor-lab`: `nokia_srsim`
(`ghcr.io/clab-labs/nokia_srsim:25.10.r1`, license via ConfigMap at
`/opt/nokia/sros/license.txt`, `mgmt-ipv4: 172.80.80.21`) pinned to worker, and `ceos`
(`ghcr.io/clab-labs/ceos:4.33.1F`, `mgmt-ipv4: 172.80.80.31`) pinned to worker2, both under a
management policy `ipv4-subnet: 172.80.80.0/24`. Private registry access through
`LauncherProfile.spec.imagePull.pullSecrets`.

## SR-SIM (pass)

- License loaded (`TiMOS license from cf3:/license.txt`), BOF adopted the interposed identity:
  `address 172.80.80.21/24 active`; the kernel device leg is stripped as the kind always does,
  while `c9s0` keeps the Pod address and the transport table keeps its default route.
- SROS management SSH answers at 172.80.80.21 (`SSH-2.0-OpenSSH_9.9`) — in a real direct Pod
  there is no competing listener, so no rig-era caveats apply.
- Pods 2/2 with the plan-derived contract; no daemon involved anywhere.

## cEOS (pass)

- EOS renamed the synthetic leg to `Management0` and its running config carries
  `interface Management0 / ip address 172.80.80.31/24` — rendered **at plan time** through
  containerlab's own template from the allocated identity, including
  `ip route 0.0.0.0/0 172.80.80.1` (the sidecar gateway). No runtime re-render, no post-deploy
  address-fixup hook, no kind-conditional code.
- EOS's namespace-global rewrites (gated blackholes, `lo` re-addressed to /24) coexist with the
  sidecar-owned transport table, which keeps the CNI default and connected routes.
- Outbound through the nftables translation: `Cli ping 1.1.1.1` 3/3 from the management plane.
- 2/2 Running in 44 s.

Together with task 6.1 this covers the three validated adapter shapes in Kubernetes proper:
internal-namespace (SR Linux), datapath-adoption (SR-SIM), same-namespace (cEOS).
