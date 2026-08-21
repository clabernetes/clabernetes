# Task 7.1 — Upstream containerlab contribution candidates

Facts the interposition implementation needed that the pinned containerlab (v0.78.0) does not
expose declaratively. Each stays realized generically in c9s until an upstream change lands;
none required kind-conditional code in c9s (the kind-agnostic AST gate still passes).

1. **Declarative TX-checksum-offload requirement.** Containerlab disables TX offload on the
   SR-SIM management veth imperatively inside node code (`nodes/sros/sros.go:420-431`,
   `utils.EthtoolTXOff`). c9s disables offload on both synthetic legs unconditionally (harmless
   where unneeded, mandatory for SR-SIM). Upstream shape: a node-registry attribute such as
   "management transport requires software checksums", so orchestrators outside containerlab's
   own deploy path can honor it without copying behavior.

2. **Management-gateway route rendering for SR OS.** cEOS's config template consumes
   `.MgmtIPv4Gateway` (`nodes/ceos/ceos.cfg`); the SR OS/SR-SIM templates do not render an
   off-subnet management default (containerlab clients are always on-subnet, Kubernetes clients
   are not). Validated stopgap from the spikes: `/bof router static-routes route 0.0.0.0/0
   next-hop <gateway>`. Upstream shape: honor the gateway field in the SR OS config/BOF
   templates the way ceos.cfg already does.

3. **Declarative management-interface contract.** The interposition profile derives the
   device-leg name from `NodeConfig.MgmtIntf` with containerlab's primary-interface contract
   (`eth0`) as fallback, and the MAC from `NodeConfig.MacAddress`. Both worked for every
   registry kind (conformance-gated), but they are conventions read out of configuration rather
   than declared facts; a registry-level "management interface expectation" would make the
   contract explicit.

4. **Loopback rewrite tolerance (informational).** cEOS re-addresses `lo` to `127.0.0.1/24`
   and blackholes `127.0.0.0/8`, which kills Docker's embedded loopback DNS under containerlab
   too (observed in the Spike 2 rig). Not a c9s defect — Kubernetes cluster DNS is a routable
   address — but worth an upstream containerlab docs note.
