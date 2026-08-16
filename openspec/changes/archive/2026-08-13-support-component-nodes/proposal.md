## Why

Containerlab can expand one logical node, such as a distributed Nokia SR-SIM chassis, into multiple nested component containers that share one network namespace. Clabernetes currently assumes every logical Node has a same-named nested container, so an otherwise healthy component-based node fails launcher discovery and readiness.

## What Changes

- Discover component containers by their containerlab root-node label when no same-named nested container exists.
- Track every expanded component for generic readiness and use the component that owns the shared network namespace for application probes.
- Mount a shared grouped payload destination only once so components can reference the same license path without producing an invalid Kubernetes Pod.
- Accept explicit containerlab `veth` links with brief or structured node/interface endpoints and canonicalize structured endpoints to the c9s Link representation.
- Document how component-based nodes, SR-SIM chassis, shared payloads, and supported `veth` syntax behave.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `topology-resource`: Explicit `veth` links with representable endpoints become supported source topology input.
- `node-lifecycle`: One logical Node may expand into multiple nested component containers whose shared readiness, network namespace, and payload mounts are realized as one launcher workload.
- `documentation-site`: User documentation describes component expansion, readiness, shared license mounting, and supported structured `veth` endpoints.

## Impact

The change affects topology YAML parsing and compilation, launcher nested-container discovery and readiness, grouped Deployment volume rendering, SR-SIM guidance, architecture documentation, and focused controller/launcher tests. It introduces no CRD schema, API version, or dependency change.
