## Context

See `proposal.md` for motivation. A c9s Node normally maps to one same-named nested Docker
container. Containerlab component expansion instead creates several containers labeled with their
individual node name and a common root-node name, then places all but one into the network namespace
owned by another component. The launcher must preserve the outer one-Node lifecycle while observing
the complete inner realization.

Grouped c9s Nodes also share one launcher filesystem. Kubernetes rejects a Pod with duplicate
`VolumeMount.mountPath` values even when each member independently declares the same SR-SIM license.

## Goals / Non-Goals

**Goals:**

- Treat containerlab-expanded components as the nested realization of one logical Node.
- Preserve group-atomic readiness and application probes across the shared network namespace.
- Keep shared grouped payload mounts valid for Kubernetes.
- Preserve representable explicit `veth` topology syntax during direct and in-cluster compilation.

**Non-Goals:**

- Reimplement containerlab's component expansion, SR-SIM fabric construction, or card provisioning.
- Allow components of one logical node to span Pods or Kubernetes workers.
- Support explicit `host`, bridge, macvlan, or other non-veth link types.
- Add component state to the c9s API or Node status.

## Decisions

### Resolve exact nodes before component groups

The launcher first looks for the established exact node-name label. Only when that lookup returns no
container does it query the root-node label and inspect all returned components. This preserves the
existing behavior for ordinary and explicitly grouped c9s Nodes while using containerlab's stable
component identity rather than inferring container names.

The alternative was to assume SR-SIM suffixes such as `-a` and `-1`. That would couple generic
launcher behavior to one device kind and would not verify the complete component set.

### Derive the shared network-namespace owner from Docker state

Every expanded component contributes to generic readiness. The component whose Docker network mode
is not `container:<id>` is the namespace owner and supplies the management address used by TCP/SSH
probes. Discovery rejects zero or multiple owners, missing component labels, and duplicate component
names rather than probing an arbitrary container.

Selecting CPM-A by name was rejected because containerlab may choose another component as the
namespace owner; all components still share the same logical network namespace.

### Deduplicate grouped mounts by destination path

Grouped Nodes share one launcher filesystem, so a destination path can exist only once in the Pod.
Deployment rendering keeps the first attachment for a destination and omits later duplicate mounts.
This matches shared-license usage and prevents Kubernetes validation from rejecting the workload.

Creating separate mounts per component was rejected because the component containers are nested
inside the same launcher and consume the same host path.

### Canonicalize structured veth endpoints at YAML decode time

The topology compatibility type accepts either a scalar `node:interface` endpoint or a mapping with
`node` and `interface`, then stores the brief form already understood by the c9s compiler. The
compiler permits explicit `veth` because both endpoint forms map without loss to an ordinary c9s
Link; other explicit link types remain errors.

Preserving a second structured endpoint representation through the controller was rejected because
the c9s Link API already has one complete node/interface representation.

## Risks / Trade-offs

- [Containerlab changes component labels or network modes] → Use its documented labels, validate the
  inspected set strictly, and fail visibly when the invariant changes.
- [Two grouped attachments target one path with different content] → One shared filesystem cannot
  realize both simultaneously; document the path as shared and keep deterministic group ordering.
- [A component fails after initial discovery] → Retain every discovered component ID in generic
  readiness so the logical Node becomes unready.
- [New structured endpoint shapes appear] → Reject shapes that cannot map to a c9s node/interface
  pair instead of silently discarding fields.

## Migration Plan

No CRD or persisted-resource migration is required. Deploy the manager and launcher built from the
same revision so the compiler, Deployment renderer, and launcher discovery behavior advance
together. Rollback restores the former behavior; ordinary single-container and explicitly grouped
Nodes remain compatible in either direction.
