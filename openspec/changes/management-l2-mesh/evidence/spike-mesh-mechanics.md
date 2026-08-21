# Evidence: mesh mechanics spike (plain docker)

Date: 2026-08-21. Host: WSL2 (`6.18.33.2-microsoft-standard-WSL2`). Three privileged multitool
containers on a dedicated docker network (10.99.0.0/24) as stand-in Pods, plus one real SR Linux.

Per-pod shape under test: device netns with synthetic `eth0` ↔ `c9sd0` (bridge port);
`c9sr0` (gateway 172.80.80.1/24, MAC `02:c9:00:00:00:01`) ↔ `c9sg0` (bridge port, isolated);
`c9sm0` VXLAN VTEP (VNI 4000, dstport 14789, bridge port, isolated); bridge `c9sb0` (no IP, pinned
MAC); head-end replication FDB (`00:00:00:00:00:00 dev c9sm0 dst <peer>` per peer);
`arp_ignore=1` + `disable_ipv6=1` on all mesh elements.

## Findings

1. **Device-to-device L2 across pods works end to end**: ARP resolves the peer device's real MAC
   through the mesh, ping 0% loss, TCP payload verified — including a 3-pod head-end replication
   triangle.
2. **WSL2 kernel has no `NF_TABLES_BRIDGE`** (`# CONFIG_NF_TABLES_BRIDGE is not set`): the
   originally sketched nftables-bridge gateway filter is not portable. Replaced by bridge **port
   isolation** on the gateway leg and the VTEP — containment then needs no packet filtering at all.
3. **ARP flux is the real hazard**: with defaults, a gateway ARP probe received replies from THREE
   MACs — the local bridge device (answers for any local address), the intended gateway leg, and
   the remote pod's bridge (mesh frames reach the remote root netns via bridge-self delivery;
   isolation cannot block the local-stack port). `arp_ignore=1` on the mesh elements reduces this
   to exactly one reply from the gateway leg; verified 3 probes → 3 replies.
4. **Own-address noise without containment**: before isolation+scoping, remote gateway replies
   arrived with the local gateway's own MAC as source (`received packet on c9sm0 with own address
   as source`) and the VXLAN FDB learned the gateway MAC toward the peer. Both effects gone after
   the fix; captures on the remote gateway port show zero frames.
5. **Bridge MAC must be pinned**: `c9sb0` inherited the lowest port MAC (observed sharing the VTEP
   MAC on one pod and the gateway-leg MAC on the other) — pinned explicitly in the design.
6. **SR Linux 25.10 adopts the bridged leg unchanged**: booted sharing a pod's netns with the
   synthetic `eth0` pre-addressed 172.80.80.21/24; `mgmt0.0` in `srbase-mgmt` carried the address,
   fake-lease synthesis unchanged. Bidirectional hardcoded-address management traffic proven:
   remote device → ping + SSH (`SSH-2.0-OpenSSH_9.2p1 Debian` from srbase-mgmt sshd) at
   172.80.80.21; SR Linux → ping 172.80.80.11 from `srbase-mgmt`.

Spike torn down completely (containers + network removed).
