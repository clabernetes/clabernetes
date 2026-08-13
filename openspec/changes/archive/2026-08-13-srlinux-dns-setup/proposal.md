## Why

SR Linux keeps its management stack in the nested `srbase-mgmt` network namespace. In a clabernetes launcher, that namespace can lack the forwarding routes and IPv4 forwarding required to reach DNS servers through the nested Docker network, leaving SR Linux management traffic unable to resolve names. The repair belongs in clabernetes' nested runtime so standalone containerlab behavior and user topologies remain unchanged.

## What Changes

- Add launcher lifecycle handling that discovers SR Linux containers and their runtime networking data from structured Docker inspection output.
- Configure and verify the routes and IPv4 forwarding required for SR Linux management-network traffic.
- Wait for the `srbase-mgmt`, `mgmt0`, and `mgmt0-0` runtime interfaces before applying the repair.
- Keep the repair idempotent, skip non-SR Linux and shared-network-namespace containers, and keep launcher readiness false when the repair cannot complete.
- Add unit coverage for discovery, selection, skips, retries, idempotency, failures, and command ordering.
- Add an end-to-end DNS verification from the SR Linux management namespace.
- Leave generic containerlab behavior, standalone deployments, and user topology definitions unchanged.

## Capabilities

### New Capabilities

- `runtime-dns-forwarding`: Repair and verify DNS-path forwarding for SR Linux nodes running inside the clabernetes nested Docker runtime.

### Modified Capabilities

<!-- No existing requirement contract changes are expected. -->

## Impact

- Affected code: `launcher/` runtime discovery, lifecycle, readiness, and Docker command helpers.
- Affected tests: launcher unit tests and the topology e2e suite.
- No Kubernetes API, CRD, or external Go dependency changes are expected.
- Runtime behavior changes only for eligible SR Linux containers launched by clabernetes.
