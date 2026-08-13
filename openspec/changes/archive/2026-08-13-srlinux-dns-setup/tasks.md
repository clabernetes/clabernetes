## 1. Runtime discovery

- [x] 1.1 Add structured Docker inspection models and a single inspection path for node labels, network mode, management gateway, and management IPv4 address.
- [x] 1.2 Resolve the configured management network from launcher settings with the `clab` default, and select only independent `srl` or `nokia_srlinux` containers without re-parsing `topo.clab.yaml`.
- [x] 1.3 Add an injectable command runner and bounded interface-readiness polling for `srbase-mgmt`, `mgmt0`, and `mgmt0-0`, preserving actionable node-specific errors.

## 2. Forwarding lifecycle

- [x] 2.1 Invoke the forwarding helper after containerlab deployment and node-container discovery, before connectivity and status probes.
- [x] 2.2 Apply the namespace-aware route replacements and IPv4 forwarding sysctl, then verify the resulting route and forwarding state with structured or exit-status checks.
- [x] 2.3 Make forwarding idempotent and gate launcher readiness on successful completion while preserving diagnostics and standalone containerlab behavior.

## 3. Verification

- [x] 3.1 Add unit tests for Docker inspection decoding, management-network selection, SR Linux kind selection, shared-network skips, and missing metadata failures.
- [x] 3.2 Add unit tests for interface retries, timeout diagnostics, exact command ordering, route replacement idempotency, verification failures, and readiness gating.
- [x] 3.3 Extend the topology e2e coverage to execute a DNS lookup from `srbase-mgmt` through the nested launcher Docker runtime and verify failure diagnostics when forwarding cannot be established.
- [x] 3.4 Run the focused launcher tests, relevant e2e checks when a cluster is available, and repository validation required by the changed code.
