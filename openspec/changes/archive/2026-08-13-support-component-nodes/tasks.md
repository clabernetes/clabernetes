## 1. Topology Compatibility

- [x] 1.1 Accept scalar and structured node/interface endpoints and canonicalize both to the brief c9s representation.
- [x] 1.2 Compile explicit `veth` links while retaining errors for unsupported explicit link types.
- [x] 1.3 Add parser and compiler regression coverage for a structured SR-SIM `veth` link.

## 2. Component Node Lifecycle

- [x] 2.1 Discover all nested component containers by their root-node label and identify exactly one shared network-namespace owner.
- [x] 2.2 Track all expanded components for generic readiness and use the namespace owner for application probes.
- [x] 2.3 Deduplicate repeated grouped payload destination paths when rendering the shared launcher Pod.
- [x] 2.4 Add unit coverage for component discovery invariants and grouped shared payload mounts.

## 3. Documentation

- [x] 3.1 Document component expansion, shared network-namespace readiness, and supported explicit `veth` syntax in the architecture guide.
- [x] 3.2 Update the Nokia SR-SIM guide with a component-based topology, shared license behavior, and the boundary between containerlab card expansion and c9s orchestration.

## 4. Verification

- [x] 4.1 Run focused parser, compiler, Deployment renderer, and launcher tests.
- [x] 4.2 Run all non-E2E Go tests and the full Go linter.
- [x] 4.3 Validate distributed SR-7 through direct clabernetes conversion and `containerlab --runtime clabernetes`, including shared network namespace and cross-worker dataplane connectivity.
- [x] 4.4 Run strict OpenSpec validation and the static documentation checks.
