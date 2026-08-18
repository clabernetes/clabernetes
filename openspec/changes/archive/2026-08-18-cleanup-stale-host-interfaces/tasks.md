## 1. Interface Target Derivation

- [x] 1.1 Add a launcher helper that parses the rendered `topo.clab.yaml` and collects only `host:` link endpoint interfaces.
- [x] 1.2 Reuse or centralize the existing containerlab `/` to `-` interface-name sanitization.
- [x] 1.3 Deduplicate normalized candidates and exclude protected interfaces such as `lo`, `eth0`, and `docker0`.
- [x] 1.4 Handle topology read and parse failures as non-destructive warnings.

## 2. Stale Interface Cleanup

- [x] 2.1 Add a cleanup operation that checks each candidate with `ip link show`.
- [x] 2.2 Delete existing candidates with `ip link delete` and warn when deletion fails.
- [x] 2.3 Invoke cleanup immediately before `containerlab deploy`.
- [x] 2.4 Ensure cleanup failures do not suppress the containerlab deployment attempt.
- [x] 2.5 Separate command execution from cleanup policy so tests can inject deterministic command behavior.

## 3. Tests

- [x] 3.1 Test extraction of ordinary host endpoints and ignoring non-host links.
- [x] 3.2 Test slash sanitization, duplicate candidates, protected interfaces, and topologies without links.
- [x] 3.3 Test stale-interface deletion, missing-interface handling, deletion failures, and protected-interface safety.
- [x] 3.4 Test that cleanup commands complete before the containerlab deploy command.
- [x] 3.5 Test extraction against topology output produced by the current materializer.

## 4. Validation

- [x] 4.1 Run `go test ./launcher ./launcher/connectivity ./util/containerlab`.
- [x] 4.2 Run relevant lint/format checks and inspect the resulting diff.
- [x] 4.3 Confirm no generated files, Kubernetes APIs, or unrelated launcher behavior changed.
