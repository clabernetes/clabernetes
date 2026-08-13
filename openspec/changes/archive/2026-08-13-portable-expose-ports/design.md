## Context

The topology compiler already flattens Containerlab defaults, kinds, and node definitions into
self-contained Node intent. It also normalizes Docker-style host bindings into destination-only
ports, filters reserved source labels, and feeds the same compiled representation to the in-cluster
Topology controller and `clabverter --emit-crs`.

Downstream Node reconciliation already allocates pod-side ports, renders Services, and materializes
the resulting mappings into the launcher topology. The change therefore belongs at the source
compiler boundary: it should turn a portable source declaration into the existing
`Node.spec.ports` intent without changing downstream APIs or local Containerlab behavior.

## Goals / Non-Goals

**Goals:**

- Express c9s Service reachability for ports that must remain unpublished on a local Docker host.
- Keep in-cluster compilation and direct manifest generation on one conversion path.
- Make effective directive labels inherit through Containerlab defaults and kinds like other labels.
- Validate malformed source input before any resource is rendered.
- Preserve existing port allocation, Service type, and auto-exposure policy.

**Non-Goals:**

- Change the Node CRD or add a second persisted port field.
- Infer ports from images, commands, kinds, or application configuration.
- Change native Containerlab `ports` semantics for local runs.
- Let arbitrary `c9s.run/` labels become user-controlled Kubernetes metadata.

## Decisions

### Consume the directive after flattening and port normalization

Run the directive consumer on each flattened node after ordinary Docker-style port bindings have
been reduced to destination-only entries. This gives a directive inherited from `defaults` or a
kind the same effective-node semantics as other inherited labels, while allowing it to deduplicate
against the final ordinary port list. Remove the directive before label validation/rendering so it
cannot become Node metadata.

Alternatives considered:

- Processing the raw YAML separately would duplicate inheritance rules and could diverge between
  the controller and clabverter.
- Processing it in the Node controller would make source labels part of a second user-intent API
  and would not help `--emit-crs`.
- Reusing native `ports` would publish an internal-only port on a local Docker host.

### Reuse the established destination-port parser

Split the label value on commas, trim each entry, and pass every entry through
`ProcessPortDefinition`. That parser enforces ports 1 through 65535 and TCP/UDP protocols,
including the default TCP protocol for a bare port. Store successful entries as
`<destination>/<lowercase protocol>` and use that same canonical form for semantic deduplication
against ordinary ports and prior directive entries.

An empty segment, unsupported protocol, range, host binding, or out-of-range port is fatal. The
compiler collects diagnostics for all invalid entries but emits no compiled topology while any
directive diagnostic remains.

### Keep the downstream exposure pipeline unchanged

The compiler appends only destination-port intent. Existing Node reconciliation resolves the
effective LauncherProfile, allocates unique pod-side ports, persists allocations in status, and
renders the Service. This preserves `disableExpose`, `disableAutoExpose`, and Service-type
behavior without a CRD or generated-artifact change.

### Preserve local-runtime inertness

The source directive remains in the raw Containerlab definition stored by a normal Topology and is
accepted as an ordinary Containerlab label by local Containerlab. Because it is not a native
`ports` entry, local Containerlab does not publish a host port. c9s consumers that use the shared
compiler consume it into Node intent; direct Node manifests do not gain any new directive.

## Risks / Trade-offs

- **Inherited directives expose the same ports on every effective node using that defaults/kind
  label** → Document that inheritance follows Containerlab label semantics; use a node-level label
  when only one node needs the port.
- **Comma separation reserves commas in the directive grammar** → The current destination-port
  grammar contains no comma; introduce a new versioned directive if that changes.
- **Older downstream compiler consumers ignore the directive** → Coordinate a dependency release and
  test both clabverter and the containerlab c9s runtime against the shared compiler.
- **A valid directive still has no effect when exposure is disabled** → Document that
  `LauncherProfile` exposure policy remains authoritative.

## Migration Plan

1. Release the compiler, tests, and documentation.
2. Update downstream consumers to the released compiler module.
3. Add `c9s.run/exposePorts` to portable topologies that require non-default internal Service ports.
4. Remove any temporary application-specific port inference.

Rollback is safe: remove the source directive and restore the previous compiler/consumer versions.
No persisted API or CRD migration is required.

## Open Questions

None for the initial grammar and compiler integration. Downstream release coordination is an
operational dependency, not a change to the behavior defined here.
