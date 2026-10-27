## Why

Direct packet capture works, but a client has to reverse-engineer how to call it: it must know
three file paths inside the `clabwire` sidecar, read the Node UID from the Node object first,
start one exec per interface, merge the single-interface pcap streams itself, and learn which
interfaces are capturable only from Denied audit lines. containerlab is about to build its c9s
capture support on this command, so the command needs a contract that clients can rely on.

## What Changes

- `--plan`, `--input`, and `--connectivityRevision` default to the paths mounted in the
  `clabwire` container. Callers may still pass them.
- `--nodeName <name>` selects the Node by its Node object name, as an alternative to
  `--nodeID`. Exactly one of the two is required. The name selects the Node within the Pod's
  plan, so grouped Nodes that share a Pod work.
- `--interface` may be repeated. Every requested interface is authorized before any capture
  starts; one interface that does not belong to the selected Node denies the whole call.
- `--format pcap|pcapng`, default `pcap`. pcapng carries one Interface Description Block per
  interface (`if_name` set to the interface name), so several interfaces share one stream.
  `pcap` stays the default and still accepts exactly one interface.
- `--list-interfaces` prints the capturable interfaces of the selected Node, or of every Node
  in the Pod's plan, as JSON with the peer Node and peer interface of each link, then exits.
- `--interface` also accepts the interface alias the Link used (for example `ethernet-1/1`
  for SR Linux `e1-1`).
- The audit record lists every requested interface and the selected Node's name.
- Concurrent captures on one Pod, and a follow-up call started right before or after another
  call ends, are specified as supported, so clients can chain calls under the one-hour bound.
- The capture socket no longer receives packets from other interfaces in the window between
  socket creation and binding to the planned interface.

No breaking change: existing single-interface pcap invocations with explicit paths and
`--nodeID` keep working and produce the same bytes.

## Capabilities

### New Capabilities

- `direct-packet-capture`: the plan-scoped packet capture command of the connectivity sidecar,
  its target selection, output formats, interface listing, bounds, concurrency, and audit
  records.

### Modified Capabilities

None. `direct-device-runtime` keeps its general requirement that packet capture targets
planned interfaces; the new capability specifies the command in detail.

## Impact

- `cmd/clabernetes/cli/entrypoint.go`: `node-runtime packet-capture` flags.
- `internal/directruntime/capture.go`, `capture_linux.go`, and a new pcapng writer in the same
  package. No new dependency.
- `internal/directpod/renderer.go`: mount paths and the fixed capture command are shared with
  the runtime defaults.
- `controllers/node/service.go`: the wire Service name comes from the runtime package, which
  also uses it to name the peer Node of a cross-Pod link.
- `docs/guides/lab-operations.md` and `e2e/topology/direct/operations_test.go`.
- No API, CRD, chart, or RBAC change. Clients still need `pods/exec` on the lab Pod.
