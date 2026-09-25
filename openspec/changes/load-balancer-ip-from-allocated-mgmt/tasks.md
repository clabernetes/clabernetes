## 1. Service Rendering

- [x] 1.1 Pass each member's management identity, converted from the applied plan with the same
  function that fills `status.directManagement`, into expose Service rendering.
- [x] 1.2 Derive the requested LoadBalancer address from that identity by family as specified;
  verify rendering tests cover allocated IPv4 and IPv6, a pinned address, IPv4 precedence, a
  missing management identity, and non-LoadBalancer types.
- [x] 1.3 Verify a reconciliation test updates an existing Service when the realized address
  changes.

## 2. API Descriptions

- [x] 2.1 Update the `useNodeMgmtIpv4Address` and `useNodeMgmtIpv6Address` descriptions on
  Topology and NodeProfile, regenerate CRDs, OpenAPI output, and CRD documentation views, and
  verify `make verify-generated` reports no drift.

## 3. Documentation

- [x] 3.1 Update the service-exposure and management-network guides, including the pool and
  per-namespace subnet requirements, allocation stability, and provider support for requested
  addresses.
- [x] 3.2 Add the behavior change and its migration to the 0.9 release notes; verify
  `make check-docs` succeeds.

## 4. Validation

- [x] 4.1 Run `make test` and `make lint`, and inspect any formatter changes.
- [ ] 4.2 Add a focused direct e2e test asserting that an opted-in Node's expose Service requests
  its `status.directManagement` address for both an allocated and a pinned Node, and run it with
  `make test-e2e CLUSTER=existing` against the selected context.
- [ ] 4.3 Verify on a local KinD cluster with a LoadBalancer provider that the assigned address
  equals the device's management address and reaches the device.
