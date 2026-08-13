## Context

The launcher runs containerlab inside a nested Docker daemon. SR Linux places its management stack in the `srbase-mgmt` network namespace, while the Docker-managed management interface and peer remain in the container root namespace. The current launcher deploys containerlab, discovers node container IDs, starts connectivity handling, and then reports readiness through `/clabernetes/.nodestatus`; it has no runtime repair step for this namespace boundary.

The launcher already receives management-network settings as structured `LAUNCHER_MGMT_NETWORK` JSON and already discovers nested container IDs through Docker labels. Docker inspection also exposes the containerlab labels, network metadata, and network mode needed by this change.

## Goals / Non-Goals

**Goals:**

- Discover eligible SR Linux containers without re-parsing `topo.clab.yaml`.
- Use one structured Docker inspection response for node kind, network mode, gateway, and management IPv4 address.
- Apply idempotent management routes and IPv4 forwarding after the required namespaces and interfaces appear.
- Keep readiness false until forwarding is successfully verified.
- Provide deterministic unit and e2e coverage for selection, retries, failures, idempotency, and DNS resolution.

**Non-Goals:**

- Modify generic containerlab or its standalone runtime.
- Add a product-specific `RUNTIME=C9S` topology switch.
- Add new Kubernetes API fields or change the topology schema.
- Repair containers that share another node's network namespace or use host/none networking.

## Decisions

### Runtime discovery uses Docker inspection JSON

After containerlab deploys, the launcher will inspect all known node container IDs in one Docker command and decode the JSON response. It will use:

- `Config.Labels["clab-node-kind"]` and `Config.Labels["clab-node-name"]` for eligibility and diagnostics;
- `HostConfig.NetworkMode` to skip `container:*`, `host`, and `none` modes;
- `NetworkSettings.Networks[managementNetwork]` for `Gateway` and `IPAddress`.

The eligible kind values are `srl` and `nokia_srlinux`, matching the containerlab aliases used by the repository and upstream documentation. Docker metadata is preferred over retaining or re-reading the rendered topology.

The management network name comes from the parsed `LAUNCHER_MGMT_NETWORK` settings. When it is unset, the helper uses containerlab's default `clab` network. A missing network entry, gateway, or address is an actionable failure for an otherwise eligible container.

### Hook placement and readiness

The repair runs in `launch()` after `runContainerlab` succeeds and after `nodeContainerIDs` has been populated, but before connectivity startup and status-probe goroutines. The launcher stores a forwarding-readiness result on the runtime object.

The status probe path incorporates that result into `getNodeReadiness`. A failed or timed-out repair also cancels launcher startup before connectivity begins. This is required because status probes are optional; without this startup failure, Kubernetes can mark a probe-less Deployment ready even though the runtime repair failed.

### Interface readiness uses command exit status

The helper waits with the launcher context and a bounded retry interval. It checks runtime existence without parsing human-readable output:

```text
docker exec <id> ip netns exec srbase-mgmt true
docker exec <id> ip netns exec srbase-mgmt ip link show dev mgmt0.0
docker exec <id> ip link show dev mgmt0
docker exec <id> ip link show dev mgmt0-0
```

Transient non-zero exits are retried. A timeout includes the node and the last missing namespace or interface in the failure message.

### Forwarding commands are namespace-aware and idempotent

Once runtime checks pass, the launcher runs:

```text
docker exec <id> ip route replace <gateway> dev mgmt0 scope link
docker exec <id> ip route replace default via <gateway> dev mgmt0
docker exec <id> ip route replace <mgmt-ip> dev mgmt0-0 scope link
docker exec <id> sysctl -w net.ipv4.ip_forward=1
```

The SR Linux peer is named `mgmt0.0` inside `srbase-mgmt`; `mgmt0` and `mgmt0-0` are in the container root namespace. `replace` makes repeated launches safe. Each command must exit successfully. Route verification uses exit-status route lookups for an external destination and the node management address; the forwarding check uses an in-container status command so the Go code does not parse human-readable command output.

### Command execution is injectable for tests

Runtime command execution will be behind a small launcher-local runner abstraction. Production uses `exec.CommandContext`; unit tests provide a fake runner that returns structured inspection JSON, command exit errors, and route-verification responses. This enables exact command-order and retry tests without requiring Docker or Linux namespaces.

### End-to-end verification uses Kubernetes DNS

The topology e2e test will obtain the launcher pod and execute through its nested Docker daemon. It will locate the SR Linux container using the existing `clab-node-name` label and run a DNS lookup from `srbase-mgmt` against the stable Kubernetes service hostname `kubernetes.default.svc.cluster.local`. The test proves the namespace can resolve through the launcher runtime path rather than merely checking root-namespace DNS.

## Risks / Trade-offs

- **[Containerlab label contract changes]** → Keep label names centralized in the launcher helper and fail with a clear discovery error rather than selecting by image name alone.
- **[Custom management network is absent or has incomplete Docker metadata]** → Use the configured network name, report the missing field, and keep readiness failing.
- **[SR Linux boot timing varies by image]** → Bound the interface retry window using the launcher context and expose the missing namespace/interface in logs.
- **[The launcher has insufficient privilege for route/sysctl operations]** → Preserve the current privileged launcher requirement and report the exact failed command.
- **[Nested Docker container commands differ across SR Linux images]** → Keep the runtime command sequence isolated and cover supported image behavior with the e2e test.

## Migration Plan

No data migration or API rollout is required. Deploying a new launcher image enables the repair for newly created launcher pods. Existing pods receive the behavior when their launcher deployment rolls.

Rollback consists of deploying the previous launcher image; no topology or Kubernetes resource changes remain after rollback.

## Open Questions

No blocking design questions remain. The implementation should confirm the `sysctl` execution namespace and the availability of `ip -j` in the supported SR Linux image while adding the command-runner tests; both checks are contained within the launcher and e2e validation.
