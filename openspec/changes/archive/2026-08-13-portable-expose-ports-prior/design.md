## Context

The compiler already flattens native containerlab defaults, kinds, and nodes, normalizes native
port bindings into `Node.spec.ports`, and filters reserved labels before rendering Node metadata.
The Node controller, launcher materializer, and Service reconciler already implement all behavior
after a destination port reaches `Node.spec.ports`. Clabverter and containerlab's c9s runtime both
reuse this compiler.

## Goals / Non-Goals

**Goals:**

- Represent internal c9s Service reachability without changing local Docker host publication.
- Keep one conversion path for Topology, clabverter primitive output, and the containerlab runtime.
- Preserve existing Node exposure allocation and Service semantics.

**Non-Goals:**

- Inferring application ports from node names, kinds, images, commands, or configuration files.
- Changing `Node.spec.ports`, LauncherProfile exposure policy, or the default auto-exposed set.
- Making arbitrary reserved labels user-controlled Kubernetes metadata.

## Decisions

### Consume a reserved source label during topology compilation

Use `c9s.run/exposePorts` as a definition-only directive and consume it after topology inheritance
and native port normalization, but before reserved-label filtering. This makes the directive inherit
like other containerlab labels while ensuring it never reaches Node metadata.

Alternatives considered:

- A normal `ports` entry changes local Docker behavior by publishing the port on the host.
- A new containerlab schema field would require a broader containerlab API change for a c9s-only
  concern.
- Reading Node metadata in the Node controller would create a second user-intent API beside
  `Node.spec.ports` and would not help direct Node resources.

### Use a comma-separated destination-port grammar

The label value accepts one or more comma-separated entries. Each trimmed entry uses the existing
destination-port parser (`port` or `port/protocol`), is canonicalized to lower-case protocol, and is
deduplicated with both ordinary ports and other directive entries.

This is compact enough for a container label and reuses established Node semantics. Invalid entries
are fatal because silently omitting one would deploy a topology whose internal dependency remains
unreachable.

### Keep all downstream exposure behavior unchanged

The compiler appends canonical entries to the flattened node's ports. Existing allocation,
launcher publication, and Service rendering then handle them identically to direct Node intent.
This avoids CRD generation and keeps exposure type and enable/disable policy in LauncherProfile.

## Risks / Trade-offs

- **[Comma separation cannot express future syntax containing commas]** → The current destination
  port grammar contains no comma; introduce a versioned/new directive if that ever changes.
- **[A reserved label is inert under local containerlab but visible as a Docker label]** → Document
  that this is intentional and that it does not publish host ports.
- **[Consumers built against an older clabernetes module omit the directive]** → Require downstream
  dependency bumps when the clabernetes change is released; validate both clabverter and
  containerlab runtime paths.

## Migration Plan

1. Release clabernetes with compiler, tests, and documentation.
2. Update downstream containerlab to the released clabernetes module.
3. Add `c9s.run/exposePorts` to topologies that require non-default internal Service ports.
4. Remove any temporary application-specific port inference.

Rollback is safe: remove the directive from affected topologies and restore the previous manager
and consumer binaries. No CRDs or persisted API shapes change.
