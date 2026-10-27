## Purpose

Define the plan-scoped packet capture command of the connectivity sidecar as a stable client
contract: how a caller selects a Node and its interfaces, the output formats, the interface
listing, the bounds, concurrency, and the audit records.

## ADDED Requirements

### Requirement: Capture runs in the connectivity sidecar with mounted defaults

The connectivity sidecar of every direct device Pod SHALL provide
`/clabernetes/manager node-runtime packet-capture`, reachable through Kubernetes Pod exec of the
`clabwire` container. The plan, input, and connectivity revision paths SHALL default to the
files mounted in that container; a caller MAY pass `--plan`, `--input`, and
`--connectivityRevision` explicitly. When no connectivity revision is mounted and the caller
passes none, the accepted plan SHALL be the effective plan, exactly as for the sidecar itself.

#### Scenario: Capture without file paths

- **WHEN** a caller runs `packet-capture --nodeName lin1 --interface eth1 --duration 10s` in the `clabwire` container
- **THEN** the command reads the mounted plan, input, and connectivity revision and captures `eth1` of `lin1`

#### Scenario: Explicit paths keep working

- **WHEN** a caller passes `--plan`, `--input`, `--connectivityRevision`, `--nodeID`, and one `--interface`
- **THEN** the command behaves as before and writes the same pcap stream

### Requirement: Callers select one Node by UID or by name

A capture or listing SHALL select at most one Node with either `--nodeID <Node UID>` or
`--nodeName <Node name>`; a capture MUST select exactly one. Passing both SHALL be rejected. The
name SHALL be the Node object name as it appears in the Pod's effective plan, so a Node that
shares its Pod with other Nodes is selected by its own name. A Node that is absent from the
Pod's effective plan SHALL be denied.

#### Scenario: Select a grouped Node by name

- **WHEN** two Nodes share one Pod and a caller passes `--nodeName` of the second Node
- **THEN** only interfaces planned for the second Node are capturable in that call

#### Scenario: Both selectors are rejected

- **WHEN** a caller passes both `--nodeID` and `--nodeName`
- **THEN** the command fails before reading any interface and captures nothing

#### Scenario: Unknown Node name

- **WHEN** a caller passes a `--nodeName` that is not in the Pod's effective plan
- **THEN** the command writes a Denied audit record and captures nothing

### Requirement: Every requested interface is authorized before capture starts

`--interface` MAY be repeated. Each value SHALL name exactly one lab-link interface planned for
the selected Node in the effective plan, by its interface name or by the alias the Link used.
If any requested interface is not planned for the selected Node, or is requested twice, the
whole call SHALL fail with a Denied audit record before any interface is opened and before any
capture bytes are written. The management interface and sidecar-owned interfaces are never
capturable. Each capture socket MUST NOT deliver packets of any other interface.

#### Scenario: One foreign interface denies the call

- **WHEN** a caller requests `eth1` and `eth9` and only `eth1` is planned for the Node
- **THEN** the command writes one Denied audit record naming both interfaces, opens no interface, and writes nothing to stdout

#### Scenario: Interface alias

- **WHEN** the Link wrote the SR Linux endpoint as `ethernet-1/1` and a caller requests `ethernet-1/1` or `e1-1`
- **THEN** the command captures the planned interface `e1-1`

### Requirement: Capture output formats

`--format` SHALL accept `pcap` and `pcapng`, defaulting to `pcap`. `pcap` SHALL write a classic
little-endian libpcap stream with Ethernet link type and SHALL accept exactly one interface.
`pcapng` SHALL write one Section Header Block, then one Interface Description Block per
requested interface in request order with Ethernet link type, the capture snap length, an
`if_name` option set to the planned interface name, and nanosecond timestamp resolution, then
one Enhanced Packet Block per packet carrying the index of its interface's Description Block,
its timestamp, captured length, and original length. Packets from several interfaces SHALL be
captured concurrently and written to the one stream without interleaving block bytes. Packet
payloads SHALL NOT appear anywhere but stdout.

#### Scenario: Two interfaces in one pcapng stream

- **WHEN** a caller requests `--format pcapng --interface eth1 --interface eth2` while both carry traffic
- **THEN** stdout is a valid pcapng stream with two Interface Description Blocks named `eth1` and `eth2`, and every Enhanced Packet Block references the block of the interface that saw the packet

#### Scenario: pcap with several interfaces is rejected

- **WHEN** a caller requests two interfaces without `--format pcapng`
- **THEN** the command fails before capturing and names `pcapng` as the required format

### Requirement: Capturable interfaces can be listed

`--list-interfaces` SHALL print to stdout a JSON array with one object per capturable interface
of the selected Node, or of every Node in the Pod's effective plan when no Node is selected,
then exit without capturing. Each object SHALL contain `node` (Node name), `interface` (the name
`--interface` accepts and pcapng `if_name` carries), `peerNode`, and `peerInterface`, and
SHALL contain `alias` when the Link used an interface alias. `peerNode` SHALL be the peer Node
name, `host` for a host endpoint, or empty when the peer cannot be named from the plan.
`peerInterface` SHALL be the peer endpoint interface as the Link wrote it. The list SHALL be
sorted by Node and interface. Capture bounds SHALL NOT apply to a listing.

#### Scenario: List every interface of a Pod

- **WHEN** a caller runs `packet-capture --list-interfaces` without a Node selector
- **THEN** stdout holds one JSON object per planned lab-link interface of every Node in the Pod, and the command exits successfully

### Requirement: Captures are bounded

A capture SHALL require `--duration` or `--packetLimit`; the first bound reached SHALL end it.
`--duration` SHALL be at most one hour, `--packetLimit` at most 1,000,000 packets counted
across all requested interfaces, and `--snapLength` between 64 bytes and 1 MiB with a default of
256 KiB. Values outside these ranges SHALL be rejected before capture.

#### Scenario: Packet limit across interfaces

- **WHEN** a caller captures two interfaces with `--packetLimit 4`
- **THEN** the stream holds exactly four packets in total and the command succeeds

### Requirement: Captures may overlap and chain

Each capture SHALL be an independent process that holds no Pod-wide lock or exclusive
resource. Several captures SHALL be allowed at the same time on one Pod, including on the same
interface, and a capture started right before or right after another ends SHALL be accepted.
Clients chain calls to exceed the one-hour bound; each call writes its own complete stream, so
a pcapng client strips the repeated Section Header and Interface Description Blocks.

#### Scenario: Two concurrent captures

- **WHEN** two callers capture interfaces of the same Pod at the same time
- **THEN** both succeed and each stream contains the packets of its own interfaces

#### Scenario: Chained captures

- **WHEN** a caller starts a new capture of the same interfaces while the previous capture is ending
- **THEN** the new capture is accepted and starts its own stream

### Requirement: Captures write secret-free audit records

Each capture SHALL write JSON audit records with schema version
`c9s.direct-packet-capture-audit/v1alpha1` to stderr: `Started` after authorization, then
`Succeeded` or `Failed`, or a single `Denied` record when authorization fails. Records SHALL
carry the plan digest, the Node UID and name, the format, the bounds, and every requested
interface with its planned interface ID when it is authorized; the final record SHALL carry
packet and byte counts. Records MUST NOT contain packet payload bytes.

#### Scenario: Audit lists all interfaces

- **WHEN** a two-interface capture succeeds
- **THEN** stderr holds a Started and a Succeeded record, each listing both interfaces, and the Succeeded record carries the packet counts
