## Context

PR #296 adds generic support for Containerlab component nodes, but the review found boundaries that
are not yet enforced. A launcher can discover component containers by labels, while a grouped
Deployment mounts all member payloads into one filesystem. Both paths need explicit consistency
checks because the current behavior can otherwise select an arbitrary namespace or silently keep
the first payload at a shared destination.

The topology compatibility parser also canonicalizes structured `veth` endpoints into the existing
c9s Link representation. That is the right API boundary, but malformed scalar and structured
values need to fail consistently. The current branch additionally needs to merge the component
documentation with the containerd registry-hosts documentation added on `main`.

## Goals / Non-Goals

**Goals:**

- Preserve one logical Node and one launcher workload for expanded component nodes.
- Validate the discovered component namespace graph before readiness or application probes begin.
- Make duplicate shared payload destinations deterministic and reject conflicting sources.
- Cover the original SR-SIM `components: []` failure shape and the endpoint/mount edge cases.
- Keep brief and structured explicit `veth` syntax equivalent at the c9s Link boundary.
- Merge and document the supported behavior without changing CRDs or adding dependencies.

**Non-Goals:**

- Reimplement Containerlab component expansion, SR-SIM fabric creation, or card provisioning.
- Add component state to the c9s API or expose component details in Node status.
- Support non-`veth` explicit link types.
- Change ordinary single-container or existing explicit `network-mode: container:<primary>`
  grouping semantics.
- Run browser/Wrangler documentation tests unless production routing behavior is explicitly in
  scope.

## Decisions

### Validate the component namespace graph

Keep the existing exact `clab-node-name` lookup for ordinary and explicitly grouped Nodes, and
use `clab-root-node-name` only when no exact container exists. For component results, inspect each
container's node label, Docker network mode, name/ID, and shared namespace metadata.

Resolution will:

1. require a non-empty unique component name and container ID;
2. identify exactly one namespace owner;
3. parse `container:<target>` modes and require every target to resolve to a discovered component;
4. reject cycles, external targets, or components that do not resolve to the owner's namespace; and
5. retain every resolved component ID for generic readiness while using the owner ID for probes.

The resolver remains a pure function so malformed metadata and graph cases can be unit-tested
without invoking Docker. The Docker command boundary will also receive tests for exact-name
lookup, root-name fallback, and inspect failures.

### Validate shared payloads before Deployment creation

Normalize each payload destination using the same absolute/relative mapping used by the renderer,
then clean the path before comparing it. Track the source identity as ConfigMap name, key, and file
mode.

Identical destination/source entries produce one Kubernetes volume and one mount. A destination
with a different source or mode is a configuration error returned from reconciliation before the
Deployment is created or updated. The renderer will retain deterministic first-entry behavior as
a defensive property for direct unit callers, but the controller boundary is authoritative.

This is preferred over silently choosing the first attachment because all grouped containers see
the same filesystem and cannot safely consume different content at one path.

### Keep endpoint canonicalization at YAML decode time

Retain `LinkEndpoints` as the compatibility type and canonical brief strings before compilation.
Scalar entries must be non-empty strings containing a node and interface; structured entries must
contain non-empty string `node` and `interface` fields. Unsupported YAML shapes fail with a parse
error. The compiler continues to accept explicit `veth` and reject other unsupported explicit link
types.

Add positive tests for brief, structured, and mixed endpoint forms, plus negative tests for empty,
wrong-shaped, and malformed endpoints. Remove the now-unreachable `veth` branch from the
unsupported-link diagnostic helper.

### Test through the reported topology shape

Add a focused integration fixture matching issue #269: a Nokia SR-SIM node with `components: []`,
a relative license destination, and a generated launcher. Add a component fixture with multiple
cards to verify all-container readiness and owner-based probes. Keep the broader E2E suite
unchanged unless the fixture can run in the existing local cluster harness.

### Resolve current-main documentation changes by union

Merge the component/veth/readiness requirements from the feature branch with the containerd
registry-hosts requirement from `main`. Preserve both scenario sets, keep generated artifacts from
`main`, and do not hand-edit the archived PR artifacts.

## Risks / Trade-offs

- [Containerlab changes labels or network-mode conventions] → Fail launcher discovery visibly and
  keep the resolver's validation errors explicit rather than guessing a component.
- [Existing users repeat a destination with different ConfigMaps] → Reconciliation stops before
  changing the Deployment and reports the conflicting path and sources.
- [Docker versions expose different namespace metadata] → Prefer stable network-mode/name/ID
  relationships and make optional metadata checks conditional only when the required invariant can
  still be proven.
- [The issue-shaped SR-SIM image is unavailable in CI] → Keep parser/controller regression tests
  image-independent and gate only the live fixture on the existing E2E environment.
- [The branch's archived OpenSpec change is not an active delta] → Keep this follow-up as a new
  active change and archive it only after implementation and validation.

## Migration Plan

No CRD or persisted-resource migration is required. Merge current `main` first, deploy manager and
launcher images from the same revision, and roll out the controller before relying on component
topologies. Invalid conflicting payload groups will remain on their last valid Deployment until
their attachments are made consistent. Rollback restores the previous behavior for ordinary Nodes,
but component and validation fixes should be rolled back together.

## Open Questions

- Which Node condition and event wording should represent a conflicting grouped payload before a
  Deployment exists?
- Does the supported Containerlab version guarantee a stable namespace metadata field, or should
  the resolver rely only on `NetworkMode` target resolution?
