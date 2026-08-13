## 1. Compiler contract

- [x] 1.1 Define the reserved `c9s.run/exposePorts` source-directive constant and document its destination-port grammar.
- [x] 1.2 Consume the effective directive after topology inheritance and ordinary port normalization, canonicalizing protocol names and deduplicating against ordinary ports.
- [x] 1.3 Reject empty or malformed directive entries with deterministic diagnostics and ensure the directive cannot reach emitted Node metadata.

## 2. Conversion and runtime coverage

- [x] 2.1 Add compiler tests for node-level, default-level, and kind-level directives, protocol canonicalization, semantic deduplication, and multiple invalid entries.
- [x] 2.2 Cover both in-cluster Topology rendering and `clabverter --emit-crs`, including the absence of the directive from Kubernetes labels.
- [x] 2.3 Verify the existing Node allocation, launcher materialization, Service rendering, and exposure-policy paths consume the resulting `Node.spec.ports` without API changes.
- [ ] 2.4 Validate a downstream containerlab c9s runtime built against the shared compiler, including a non-default internal Service port.

## 3. Documentation and migration

- [x] 3.1 Document portable source labels, inheritance behavior, local Containerlab inertness, and the comma-separated grammar.
- [x] 3.2 Document that LauncherProfile exposure policy remains authoritative, including disabled exposure and auto-exposure cases.
- [ ] 3.3 Update the portable topology consumer that needs the port and remove any temporary application-specific port inference.

## 4. Validation and release

- [x] 4.1 Run focused compiler and clabverter tests, the relevant Go test suite, lint, documentation checks, and strict OpenSpec validation.
- [ ] 4.2 Deploy the portable topology through `clabverter`, verify the generated Node and Service carry the requested port, and verify the application endpoint.
- [ ] 4.3 Deploy the same topology through the containerlab c9s runtime, verify local host publication remains unchanged, and destroy the test deployment.
