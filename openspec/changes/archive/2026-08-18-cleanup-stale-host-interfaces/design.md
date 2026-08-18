## Context

The launcher materializes links terminating on the containerlab `host` node as veths whose
host-side endpoint lives in the launcher pod network namespace. That namespace belongs to the
pod sandbox, while the launcher container and nested Docker state can be restarted independently.

If deployment fails after a host-side veth is created, the interface remains in the pod namespace.
On restart, containerlab starts with no knowledge of the failed nested lab and cannot remove the
orphan before checking the new topology. The next deployment therefore fails on the existing
interface.

The cleanup must run after `topo.clab.yaml` is rendered and before `containerlab deploy`. It must
operate only on names derived from the current topology and must not affect normal pod plumbing.

## Goals / Non-Goals

**Goals:**

- Recover from stale host-side veths left by an interrupted deployment.
- Derive cleanup targets from the exact rendered topology used by containerlab.
- Match containerlab's interface-name sanitization.
- Make cleanup best-effort and observable through warnings.
- Test extraction, protection, cleanup behavior, and command ordering without requiring Kubernetes.

**Non-Goals:**

- Discovering or deleting arbitrary interfaces in the pod namespace.
- Replacing containerlab's lifecycle management or adding a separate lab state store.
- Cleaning interfaces inside nested node namespaces.
- Guaranteeing recovery from unrelated pod-network corruption.
- Adding Kubernetes API, CRD, or controller changes.

## Decisions

### Derive targets from the rendered topology

The launcher will parse `topo.clab.yaml` using the repository's containerlab configuration loader
and inspect link endpoints whose node is `host`. This ensures cleanup follows the topology actually
passed to containerlab, including generated cross-launcher links.

An alternative would be to reconstruct targets from Link resources or launcher API objects. That
would duplicate materialization logic and could diverge from the rendered file, so it is rejected.

### Sanitize interface names at the shared boundary

Host endpoint interface names will be normalized with the same `/` to `-` transformation already
required by the launcher connectivity code. The normalization helper should be shared or moved to
a common package rather than independently reimplemented.

### Use targeted `ip link` operations

For each derived target, the launcher will check whether the device exists and delete only that
device when present. `lo`, `eth0`, and `docker0` are protected explicitly even if a malformed or
unexpected topology names them.

An unrestricted interface scan is rejected because it could delete legitimate pod networking.
Containerlab destroy is insufficient because the failure case has no surviving nested lab metadata.

### Separate command execution from cleanup policy

The cleanup policy will be testable independently from process execution. Production code may use
`ip link show` and `ip link delete`, but tests will inject or replace the command boundary so they
can assert existence checks, deletion targets, failures, and ordering without requiring network
privileges or relying on `PATH`-installed shell scripts.

Cleanup failures will be logged and will not prevent the normal deploy command from being attempted;
the cleanup is a recovery aid, not a reason to suppress deployment.

### Run cleanup immediately before deployment

Cleanup belongs at the start of `runContainerlab`, after topology files have been written by
`fetchNodeResources` and before any containerlab process is started. This minimizes the interval in
which another failure can recreate stale state and ensures the inspected file is the deploy input.

## Risks / Trade-offs

- [A topology intentionally names a pod interface] → Protect essential names and restrict deletion
  to topology-derived host endpoints; malformed topology parsing remains non-destructive.
- [Interface sanitization changes in containerlab] → Keep the transformation centralized and add
  regression tests for vendor-style slash-containing names; document the dependency on containerlab
  naming behavior.
- [Cleanup command fails due to missing privileges or a race] → Emit a warning, continue to deploy,
  and preserve the original containerlab error for diagnosis.
- [A duplicate endpoint is listed] → Deduplicate cleanup targets so each interface is considered
  once.
- [The topology file cannot be read or parsed] → Warn and continue without deleting anything.
