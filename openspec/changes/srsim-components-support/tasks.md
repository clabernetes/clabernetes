## 1. Merge Current Main

- [x] 1.1 Merge the current `main` branch into the component-support branch and preserve both the component documentation requirements and the containerd registry-hosts requirements.
- [x] 1.2 Verify the merge has no conflict markers, retains generated artifacts from `main`, and leaves unrelated changes untouched.

## 2. Harden Component Discovery

- [x] 2.1 Extend component inspection metadata and the pure resolver to validate unique component names, exactly one namespace owner, discovered `container:<target>` references, cycles, and shared namespace resolution.
- [x] 2.2 Preserve exact-node lookup for ordinary and explicitly grouped Nodes, root-node fallback for expanded components, all-component readiness, and owner-based application probes.
- [x] 2.3 Add unit tests for external namespace targets, cycles, missing IDs, invalid metadata, and successful owner resolution independent of inspection order.
- [x] 2.4 Add command-boundary tests for exact label lookup, root-node fallback, Docker inspect failures, and malformed inspect output.

## 3. Validate Shared Payload Mounts

- [x] 3.1 Normalize grouped payload destinations and compare ConfigMap name, key, and mode before rendering the launcher Deployment.
- [x] 3.2 Return a reconciliation error for conflicting sources or modes while retaining one mount for identical shared payloads.
- [x] 3.3 Add renderer and reconciliation tests for identical absolute paths, equivalent relative paths, normalized paths, and conflicting payload definitions.

## 4. Harden Explicit veth Compatibility

- [x] 4.1 Validate scalar and structured endpoint values during YAML decoding and preserve canonical brief endpoint representation.
- [x] 4.2 Keep explicit `veth` compilation support, reject malformed endpoints before Link emission, and remove the unreachable `veth` diagnostic branch.
- [x] 4.3 Add parser/compiler tests for brief, structured, mixed, empty, malformed, wrong-shaped, and unsupported explicit link inputs.

## 5. Reproduce the Reported SR-SIM Topology

- [x] 5.1 Add a regression fixture for issue #269 using a Nokia SR-SIM node with `components: []` and a relative license path.
- [x] 5.2 Add component-group coverage proving every expanded container affects readiness and the namespace owner receives application probes.
- [x] 5.3 Attempt live fixture validation when available; not run here because the Docker daemon and SR-SIM image are unavailable.

## 6. Update Documentation

- [x] 6.1 Update the architecture and SR-SIM guides with namespace ownership validation, `components: []`, shared-payload conflict behavior, and component-versus-explicit-group semantics.
- [x] 6.2 Document brief and structured `veth` endpoint boundaries and the direct/containerlab runtime topology shape.
- [x] 6.3 Verify the documentation-site spec includes both component-support requirements and the current `main` containerd registry-hosts requirements.

## 7. Verification

- [x] 7.1 Run focused launcher, controller, topology, and Containerlab utility tests.
- [x] 7.2 Run `go test ./...`, `make lint`, and `make verify-generated`.
- [x] 7.3 Run `make check-docs` and static link validation without launching browser/Wrangler tests.
- [x] 7.4 Run strict OpenSpec validation and inspect the complete diff before archiving the change.
