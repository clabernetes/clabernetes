## Why

Containerlab nodes on the same Docker management network can reach every listening container port,
but c9s nodes are isolated behind per-node Kubernetes Services and only declared/default ports are
reachable. Reusing the native `ports` field would unnecessarily publish an internal-only port on a
local Docker host, so portable topologies need a c9s-specific declaration that does not change local
runtime behavior.

## What Changes

- Add the definition-only `c9s.run/exposePorts` containerlab node label.
- Compile its comma-separated destination-port entries into `Node.spec.ports`, merging and
  deduplicating them with ordinary topology ports.
- Consume the directive before Kubernetes metadata rendering so it cannot become a controller-owned
  object label.
- Reject malformed directive entries instead of silently deploying an unreachable service.
- Make the behavior available to the in-cluster Topology compiler, `clabverter --emit-crs`, and
  containerlab's c9s runtime through their shared compiler.
- Document the portable-topology use case and label grammar.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `topology-resource`: Define how a reserved source label declares c9s-only exposed ports and is
  consumed into emitted Node intent.

## Impact

- Compiler and compiler diagnostics under `controllers/topology/`.
- The shared c9s label constants.
- Clabverter primitive manifests and golden fixtures.
- Topology and service-exposure documentation.
- Portable topology consumers such as containerlab's c9s runtime and srl-telemetry-lab.
- No CRD, Node API, launcher, Service reconciler, or local containerlab runtime behavior changes.
