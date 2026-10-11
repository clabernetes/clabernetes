# Tasks: Multiple Management Networks

## 1. Vocabulary and compilation

- [ ] 1.1 Accept `mgmt` as a list of networks with the single-mapping compatibility form; carry
      per-network address policy and the node `mgmt-net` selection through the compiler; reject
      `driver: macvlan` and `mgmt.tailscale` with substitution diagnostics; unit tests.
- [ ] 1.2 Render per-network management policies on NodeProfiles and the selected network name on
      Nodes; regenerate CRDs, OpenAPI, and clients; update the management network guide and the
      differences page.

## 2. Allocation and peer directory

- [ ] 2.1 Walk per-network pools with per-network pinned-address and collision checks; resolve
      each Node's policy from its selected network; unit tests.
- [ ] 2.2 Extend the peer directory entry with the per-network address map and per-network
      membership; keep the shard shape and fingerprint caching; unit tests.

## 3. Interposition

- [ ] 3.1 Realize one mesh segment per joined network: device leg, gateway pair, VNI, gateway
      MAC, VTEP, and filter chain derived per network; cold-start interface set and route
      assertion become per-segment; sidecar unit tests.
- [ ] 3.2 Converge per-peer neighbor/forwarding state per network on directory change, cold pass,
      and resync tick; validate stale-entry removal when a node leaves one network only.

## 4. Plan contract and imported lifecycle

- [ ] 4.1 Carry the selected network identity in the management input/plan; bump the plan schema
      version; feed `WithMgmtNet` with the node's selected network during evaluation and
      post-deploy replay; deviceplan and adapter tests.
- [ ] 4.2 Validate that every node's selected network resolves in the namespace policy before
      planning, and that DNS/hosts rendering picks the right network per interface.

## 5. E2E

- [ ] 5.1 Add a multi-network scenario: two management subnets, per-network reachability, a node
      in each network, and a cross-network isolation check; wire it into the CI suite matrix and
      the workflow validator.
