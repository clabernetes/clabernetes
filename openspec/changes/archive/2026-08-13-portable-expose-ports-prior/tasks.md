## 1. Compiler behavior

- [x] 1.1 Define the reserved `c9s.run/exposePorts` source directive constant
- [x] 1.2 Consume, validate, canonicalize, and deduplicate directive ports during topology compilation
- [x] 1.3 Emit fatal diagnostics for malformed directive entries and keep the directive out of Node metadata

## 2. Conversion coverage

- [x] 2.1 Add compiler tests for inherited port merging, deduplication, directive consumption, and invalid values
- [x] 2.2 Update clabverter fixtures and assertions for regular Topology and `--emit-crs` output

## 3. Documentation and portable topology

- [x] 3.1 Document the directive grammar, local-runtime behavior, and LauncherProfile exposure policy
- [x] 3.2 Add the gNMIc metrics port directive to the srl-telemetry-lab topology

## 4. Validation

- [x] 4.1 Run clabernetes unit tests, lint, documentation checks, and diff hygiene checks
- [x] 4.2 Deploy the telemetry lab through clabverter and verify Prometheus reaches `gnmic:9273`
- [x] 4.3 Build the containerlab c9s runtime against the local compiler, deploy the lab, verify the target, and destroy the lab
