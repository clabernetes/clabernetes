## Why

Containerlab nodes on one local Docker management network can reach listening ports without host
publication, while c9s nodes are reached through Kubernetes Services whose ports must be declared.
Portable topologies therefore need a c9s-only declaration for internal Service ports that remains
inert when the same topology is run directly by containerlab.

## What Changes

- Define `c9s.run/exposePorts` as a reserved, definition-only Containerlab node label.
- Interpret its comma-separated entries as destination-port declarations using the existing c9s
  port grammar: `port` or `port/{tcp,udp}`.
- Trim and canonicalize directive entries, deduplicating them semantically with ordinary topology
  ports and with other directive entries.
- Consume the directive after topology defaults, kinds, and node fields have been flattened and
  ordinary port bindings normalized, but before source labels are rendered as Kubernetes metadata.
- Fail compilation with a source diagnostic for any empty or malformed directive entry; do not
  silently produce a topology with an unreachable dependency.
- Make in-cluster Topology compilation and `clabverter --emit-crs` produce identical Node port
  intent through the shared compiler.
- Preserve the source label in the embedded topology used by local containerlab, where it remains
  an inert Docker label and does not publish a host port.
- Document the directive, its inheritance and exposure-policy interactions, and add compiler and
  conversion-path coverage.

No CRD, persisted Node API, launcher allocation, Service rendering, or local containerlab port
publication behavior changes are required.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `topology-resource`: Specify the reserved source directive, its effective-node inheritance,
  validation and canonicalization rules, and its conversion into `Node.spec.ports`.

## Impact

- `controllers/topology/` compiler diagnostics and rendering.
- Shared label and port parsing constants/utilities.
- Clabverter primitive manifests and fixtures.
- Topology and service-exposure documentation.
- Downstream consumers that import the shared compiler, including containerlab's c9s runtime.
