## Why

Operators who integrate lab devices into a cluster's routed network want a device's LoadBalancer
address to equal the management address its network operating system actually uses.
`useNodeMgmtIpv4Address` and `useNodeMgmtIpv6Address` were introduced when containerlab assigned
management addresses inside the launcher Pod, so they could only read a pinned `mgmt-ipv4` or
`mgmt-ipv6`. The direct runtime now allocates every management address in the controller, but the
options still ignore allocated addresses: every Node must be pinned by hand, and an unpinned Node
silently gets an unrelated, provider-assigned LoadBalancer address.

## What Changes

- **BREAKING**: `useNodeMgmtIpv4Address` and `useNodeMgmtIpv6Address` make a Node's LoadBalancer
  expose Service request the management address realized for that Node by the applied device
  plan, whether it was pinned or allocated by c9s. This is the same address reported in
  `status.directManagement`. Unpinned Nodes that previously received a provider-chosen address now
  request their allocated management address.
- Update the API field descriptions, the service-exposure and management-network guides, and the
  0.9 release notes, including the pool and per-namespace subnet requirements and the migration
  for topologies that pin only some Nodes.
- No API fields are added or removed.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `service-exposure`: Define how a LoadBalancer expose Service requests the Node's realized
  management address, including allocated addresses.

## Impact

- Expose Service rendering and the direct Node reconciler are affected; the Topology and
  NodeProfile field descriptions and therefore the generated CRDs, OpenAPI output, and CRD
  documentation views change text only.
- Topologies that enable the options and pin only some Nodes, with a LoadBalancer pool covering
  only the pinned addresses, leave the unpinned Nodes' Services pending until those Nodes are
  pinned or the allocation range is moved into the pool.
- No runtime, device-plan, or management-allocation behavior changes. Whether the requested
  address is honored remains the LoadBalancer provider's decision.
