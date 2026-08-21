## Why

Launcher deployments can leave a host-side veth interface in the pod network namespace if
containerlab fails after creating the veth but before moving both ends into nested node
namespaces. The launcher container is restarted in the existing pod sandbox, so the stale
interface survives while the new nested Docker/containerlab state is empty:

1. A launcher renders a link such as `router1:eth2 <-> host:router1-eth2`.
2. Containerlab creates `router1-eth2` in the pod namespace, then deployment fails.
3. Kubernetes restarts only the launcher container; the pod network namespace remains.
4. The next deploy tries to create the same host endpoint and fails because it already exists.
5. The launcher repeats this cycle until the pod is deleted.

The launcher must remove only topology-defined stale host interfaces before retrying deployment,
so failed deployments recover without manual pod deletion while protected pod interfaces remain
untouched.

## What Changes

- Detect host-side interfaces from the rendered containerlab topology before deployment.
- Apply containerlab's interface-name sanitization when identifying those interfaces.
- Remove matching stale interfaces from the pod network namespace before invoking containerlab.
- Protect essential pod interfaces such as `lo`, `eth0`, and `docker0`.
- Log cleanup and cleanup-failure warnings without masking the subsequent deployment attempt.
- Add unit coverage for endpoint extraction, sanitization, protected interfaces, and cleanup
  command ordering.

## Capabilities

### New Capabilities

- `stale-host-interface-cleanup`: Recover launcher deployments by removing stale host-side veth
  interfaces left in the pod network namespace after a failed containerlab deployment.

### Modified Capabilities

## Impact

- Affects the launcher deployment path, rendered containerlab topology handling, and launcher
  interface cleanup.
- Adds no Kubernetes API or CRD changes.
- Requires no new external dependency.
- Tests will need deterministic coverage for `ip link` discovery/deletion and deployment ordering.
