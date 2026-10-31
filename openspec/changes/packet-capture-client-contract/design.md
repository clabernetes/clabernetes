## Context

`node-runtime packet-capture` runs in the `clabwire` native sidecar, which shares the Pod
network namespace with the device containers. It reconstructs the effective plan from the
mounted input, plan, and connectivity revision, authorizes one interface, opens an
`AF_PACKET` socket bound to it, and streams classic pcap on stdout with JSON audit records on
stderr. Every invocation is its own exec process. See proposal.md for why clients need more.

Facts the design relies on:

- The plan's interface `name` is containerlab's in-namespace interface name after alias
  mapping (`endpoint.GetIfaceName()`), and `alias` is the Link's spelling when the kind maps
  it (SR Linux `ethernet-1/1` to `e1-1`, `cisco_xrv9k` `Gi0/0/0/0` to `eth1`). Linux kinds and
  canonical spellings have no alias.
- Plan nodes carry the Node object name and UID. A Pod's plan contains every Node grouped into
  that Pod.
- Interfaces carry the peer's Node UID and the peer endpoint as the Link wrote it. A cross-Pod
  peer's UID is not in the local plan, but its wire transport is the peer's wire Service name,
  `<peer Node name>-wire`. Host endpoints have no peer UID and connectivity `host`.

## Goals / Non-Goals

**Goals:**

- One exec per Pod and Node captures any number of that Node's interfaces into one stream.
- Callers need only the Node name and interface names.
- Existing callers keep identical behavior.

**Non-Goals:**

- Capturing interfaces of several Nodes in one call. Clients run one call per Node.
- An HTTP or streaming API next to Pod exec.
- Merging streams of several Pods; that is the client's job (containerlab).
- Capturing the management or sidecar interfaces.

## Decisions

- **Defaults from shared constants.** The mount paths move to exported constants in
  `internal/directruntime`, which the Pod renderer already imports for the peer directory
  mount. The CLI defaults and the renderer then cannot drift. A missing default revision file
  means no revision is mounted, which is the same effective plan the sidecar uses; an
  explicitly passed path that is missing still fails.
- **Name selection against the effective plan.** `--nodeName` matches `NodePlan.Name` in the
  effective plan, the same plan that authorizes interfaces, so a name never resolves to a UID
  from another source. Alternative: also accept the topology's original node name. Rejected:
  containerlab already maps topology names to Node objects, and a second name space invites
  ambiguity.
- **Authorize all, then open all, then write.** Every interface is resolved first; any
  failure is one Denied record. Then all sockets are opened; an open failure is a Failed
  record before any byte reaches stdout. Only then is the header written.
- **pcapng writer in the package.** The format needs four block types with fixed layouts. The
  writer builds each block in memory and writes it with one `Write` call under a mutex, so
  concurrent interface readers never interleave bytes. `gopacket` is only an indirect
  dependency and would pull libpcap concerns into the sidecar; the standard library is enough.
  Timestamps use nanosecond resolution (`if_tsresol` = 9). Byte order is little-endian, which
  readers detect from the byte-order magic.
- **One reader goroutine per interface, shared counter.** Each reader takes the writer lock to
  write a packet and count it, so the packet limit is exact across interfaces. Reaching the
  limit, the duration, a read error, or caller cancellation cancels every reader. The first
  error wins and is reported in the Failed record.
- **pcap stays single-interface.** pcap has no interface field, so several interfaces in pcap
  would lose the information clients need. The command rejects that combination instead of
  silently picking one.
- **Peer Node name.** In-plan peers are named from the plan; host peers are `host`; wire peers
  are named by stripping the wire Service suffix, which the controller and the runtime now
  take from one function. A peer that matches none of these gets an empty `peerNode`.
- **Socket bound before it receives.** The socket is created with protocol 0 and bound with
  `ETH_P_ALL` to the interface index, the libpcap pattern. A socket created with `ETH_P_ALL`
  queues packets from every interface until `bind` runs; that window could leak frames of the
  management interface into a capture.
- **Audit shape.** The v1alpha1 record gains `nodeName`, `format`, and `interfaces` (name,
  planned ID, packet count). The singular `interfaceName` and `interfaceID` stay for
  single-interface calls, so the change is additive for existing parsers.

- **Client departure through stdin.** Kubernetes keeps an exec'd process running after the
  client's stream closes, but it closes the process's stdin: client-go's websocket executor
  sends the v5 `CLOSE` signal for stdin (`v5.channel.k8s.io`, served since Kubernetes 1.29 and
  translated by the API server for the kubelet), and the SPDY executor half-closes the stdin
  stream. `--stop-on-stdin-close` reads stdin and ends the capture on EOF or a read error,
  through the same path as a reached bound, so the record is `Succeeded` with a reason. It is
  opt-in because an exec without `-i` gets an empty stdin that would end the capture at once.
  Alternatives: a heartbeat on stdin (needs a client protocol for no gain over EOF), or
  probing stdout with zero-length writes (pipes do not report a closed reader that way).
- **SIGPIPE ignored.** Go kills a process with SIGPIPE when a write to stdout fails with
  EPIPE. The command ignores SIGPIPE so the write returns EPIPE; EPIPE, EBADF, and a closed
  descriptor end the capture as a departed client with a final record. Other write errors
  still fail it.

## Risks / Trade-offs

- [A slow stdout consumer stalls every reader behind the writer lock] → The kernel socket
  buffers absorb bursts, and drops then happen in the kernel as with any capture tool. The
  call stays bounded by duration.
- [Wire peer naming depends on the Service name convention] → Both sides use one function,
  and a unit test pins the round trip.
- [A client that cannot close stdin without closing the whole stream] → Abrupt disconnects
  also end the exec's stdin, so the capture still stops; the flag only needs stdin attached.
- [Clients may treat a pcap and pcapng stream alike] → `pcap` stays the default; pcapng is
  opt-in and documented with its layout.

## Migration Plan

Additive. Old invocations keep working. Clients that need several interfaces or the shorter
form require this release; they check the clabernetes version before relying on it.
