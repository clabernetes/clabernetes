//go:build linux

//nolint:err113 // x_tables ABI work is one dense, well-factored boundary.
package directruntime

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The x_tables control ABI, fixed by the uapi headers since 2.6: the raw IPPROTO_RAW socket is
// the control channel, IPT_SO_GET_INFO/IPT_SO_GET_ENTRIES read a table snapshot, and
// IPT_SO_SET_REPLACE splices an edited snapshot back. Every layout below is byte-exact for the
// kernel's on-the-wire encoding (native-endian, naturally aligned); the offsets the entry
// encoding depends on are asserted by TestLegacyAcceptEntryEncodesIptablesShape without a kernel.
const (
	// legacyTableNameLength is xt_table's name field, XT_TABLE_NAME_LEN.
	legacyTableNameLength = 32

	// legacyFilterTableName is the table a device's iptables-legacy input policy lives in.
	legacyFilterTableName = "filter"

	// legacyGetInfoOption is IPT_SO_GET_INFO, on sockopt level IPPROTO_IP.
	legacyGetInfoOption = 64

	// legacyGetEntriesOption is IPT_SO_GET_ENTRIES, on sockopt level IPPROTO_IP.
	legacyGetEntriesOption = 65

	// legacySetReplaceOption is IPT_SO_SET_REPLACE, on sockopt level IPPROTO_IP.
	legacySetReplaceOption = 64

	// legacyFilterInputHook is NF_INET_LOCAL_IN's number in the five-hook array; the OUTPUT
	// hook is LOCAL_OUT, number 3 -- not the historic 4.
	legacyFilterInputHook = 1

	// legacyEntryHeadLength is sizeof(struct ipt_entry), 112: ipt_ip(84) + nfcache(4) +
	// target_offset(u16 at 88) + next_offset(u16 at 90) + comefrom(4) + counters(16).
	legacyEntryHeadLength = 112

	// legacyMatchHeadLength and legacyTargetHeadLength are sizeof(struct xt_entry_match) and
	// sizeof(struct xt_entry_target): u16 size + char name[29] + u8 revision.
	legacyMatchHeadLength  = 32
	legacyTargetHeadLength = 32

	// legacyTCPMatchDataLength and legacyUDPMatchDataLength are the xt_tcp/xt_udp payloads
	// past their xt_entry_match headers: two port ranges, invflags, and (tcp) the option
	// byte.
	legacyTCPMatchDataLength = 12
	legacyUDPMatchDataLength = 9

	// legacyCommentMatchDataLength is the comment payload: XT_COMMENT_LEN bytes plus the
	// terminator.
	legacyCommentMatchDataLength = 256

	// legacyStandardTargetDataLength is the verdict int32 past the xt_entry_target header.
	legacyStandardTargetDataLength = 4

	// legacyVerdictAccept is the standard target's verdict encoding for NF_ACCEPT(0): the
	// kernel stores -NF_VERDICT-1, so an accept round-trips as 0xFFFFFFFE. Kept as the
	// round-trip-verified constant rather than derived, so the ABI stays explicit.
	legacyVerdictAccept = uint32(0xFFFFFFFE)

	// legacyVerdictDrop is the standard target's verdict encoding for NF_DROP: the kernel
	// stores -NF_VERDICT-1, so a drop round-trips as 0xFFFFFFFF.
	legacyVerdictDrop = uint32(0xFFFFFFFF)

	// legacyReplaceHeadLength is sizeof(struct ipt_replace) on 64-bit: name[32] +
	// valid_hooks + num_entries + size + hook_entry[5] + underflow[5] + num_counters + the
	// counters pointer.
	legacyReplaceHeadLength = 96

	// legacyHookArrayLength is the byte size of one five-hook offset array on the wire.
	legacyHookArrayLength = 20

	// legacyEntriesSizeFieldWidth is the u32 entries-size field an entries header carries
	// past its name field.
	legacyEntriesSizeFieldWidth = 4

	// legacyPortMax is the largest port number an xt_tcp/xt_udp range field encodes.
	legacyPortMax = 65535

	// legacyTCPMatchName and legacyUDPMatchName are the match extension names the kernel
	// resolves for the swept transports.
	legacyTCPMatchName = "tcp"
	legacyUDPMatchName = "udp"

	// legacyGetinfoLength is sizeof(struct ipt_getinfo), 84 on every target: name[32] +
	// valid_hooks(u32) + hook_entry[5](u32 each) + underflow[5](u32 each) + num_entries(u32)
	// + size(u32). The kernel's get_info() rejects any other optlen with EINVAL, so the
	// constant is the exact size, not an exchange-slack number.
	legacyGetinfoLength = 84

	// legacyExtensionAlignment is the alignment the kernel applies to every entry and
	// extension on 64-bit (XT_ALIGN(sizeof(long))).
	legacyExtensionAlignment = 8
)

// legacyReplaceMaxAttempts bounds the read-modify-write against a device concurrently
// rewriting its ruleset: every EAGAIN restarts from a fresh snapshot.
const legacyReplaceMaxAttempts = 5

// legacyXTablesLockfileEnv is the environment variable iptables-legacy reads its lockfile
// path from; the sweep honors it so both sides take the same lock.
const legacyXTablesLockfileEnv = "XTABLES_LOCKFILE"

// legacyXTablesLockfilePath is the lockfile upstream iptables-legacy takes when the
// environment does not name one.
const legacyXTablesLockfilePath = "/run/xtables.lock"

// legacyLockfileMode is the xtables lockfile's creation mode, matching iptables-legacy's own.
const legacyLockfileMode = 0o600

// legacyRule is one rule of one legacy iptables chain, decoded far enough for the sweep to
// recognize its own accepts and a device's drops.
type legacyRule struct {
	// Protocol is the IP protocol number the rule matches, or 0 for any protocol.
	Protocol byte
	// Port is the destination port the rule matches, or 0 for any port.
	Port uint16
	// Accept reports whether the rule terminates with an ACCEPT verdict.
	Accept bool
	// Comment is the rule's --comment payload, when it carries one.
	Comment string
}

// AcceptsTCP reports whether the rule keeps TCP traffic to the port deliverable.
func (r legacyRule) AcceptsTCP(port uint16) bool {
	return r.Accept && r.Protocol == unix.IPPROTO_TCP && r.Port == port
}

// AcceptsUDP reports whether the rule keeps UDP traffic to the port deliverable.
func (r legacyRule) AcceptsUDP(port uint16) bool {
	return r.Accept && r.Protocol == unix.IPPROTO_UDP && r.Port == port
}

// legacyChainSpan is one base chain's position inside the entries blob.
type legacyChainSpan struct {
	// Name is the hook's chain name.
	Name string
	// Entry is the byte offset the chain starts at.
	Entry uint32
	// End is the byte offset just past the chain's entries, before the next base chain.
	End uint32
}

// legacyChainSpans derives the base chains from the snapshot's hook arrays, in entries-blob
// order: the filter table's five hooks carry the base chains, and the hook_entry offsets place
// them in the blob.
func legacyChainSpans(info *legacyGetinfo) []legacyChainSpan {
	// The filter table's hook numbering is NF_INET (LOCAL_IN is 1, LOCAL_OUT is 3); NAT
	// hooks never appear in the filter table's valid_hooks mask.
	names := map[uint32]string{
		0: "PREROUTING",
		1: "INPUT",
		2: "FORWARD",
		3: "OUTPUT",
		4: "POSTROUTING",
	}

	var spans []legacyChainSpan

	for hook, name := range names {
		if info.ValidHooks&(1<<hook) == 0 {
			continue
		}

		spans = append(spans, legacyChainSpan{
			Name:  name,
			Entry: info.HookEntry[hook],
		})
	}

	sort.Slice(spans, func(index, other int) bool {
		return spans[index].Entry < spans[other].Entry
	})

	// A base chain's span ends where the next base chain begins; entries past the last base
	// span are user-chain rules the device jumps into.
	for index := range spans {
		if index+1 < len(spans) {
			spans[index].End = spans[index+1].Entry

			continue
		}

		spans[index].End = math.MaxUint32
	}

	return spans
}

// ensureLegacyTransportAccepts sweeps the legacy iptables filter table and inserts the missing
// per-port accepts at the head of its input base chain -- the x_tables counterpart of the
// netlink sweep, without which an EOS-style `-P INPUT DROP` programmed through iptables-legacy
// stays invisible to netlink and severs the transports.
//
// The whole read-modify-write runs under the xtables lock, as upstream iptables-restore
// holds it: without the lock a device replacing its ruleset between our get_entries and our
// set-replace could make the kernel write past the counters copy-back array. EAGAIN retries
// restart from a fresh snapshot inside the locked section.
//
// A lock that cannot be taken skips the tick with a diagnostic: the next revision tick
// re-asserts, and an unservable lockfile path must never crash-loop the sidecar. Ceiling:
// the lock serializes only processes resolving the same lockfile inode; a device container
// in a separate mount namespace resolves XTABLES_LOCKFILE to a different inode, leaving a
// residual race window bounded by the next-tick re-assertion.
func ensureLegacyTransportAccepts(accepts []transportAccept) error {
	if len(accepts) == 0 {
		return nil
	}

	lock, ok := legacyLockXTables()
	if !ok {
		return nil
	}

	defer unix.Close(lock) //nolint:errcheck // closing also drops the advisory lock.

	var lastError error

	for attempt := range legacyReplaceMaxAttempts {
		retry, err := ensureLegacyTransportAcceptsOnce(accepts)
		if err == nil {
			return nil
		}

		if retry && attempt != legacyReplaceMaxAttempts-1 {
			continue
		}

		lastError = err

		break
	}

	return lastError
}

// legacyLockXTables takes the xtables lock one sweep tick holds across its whole
// read-modify-write: XTABLES_LOCKFILE names the file, defaulting to the /run/xtables.lock
// upstream iptables-legacy takes. The lock is taken non-blocking so a device holding it
// cannot block the sidecar's controller loop; skipping the tick converges on the next one.
//
// It reports false when the lock cannot be held: the lockfile cannot be created or opened, or
// another process holds it (EWOULDBLOCK under LOCK_NB). Skipping the tick is success for this
// tick.
func legacyLockXTables() (int, bool) {
	path := os.Getenv(legacyXTablesLockfileEnv)
	if path == "" {
		path = legacyXTablesLockfilePath
	}

	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR, legacyLockfileMode)
	if err != nil {
		_, _ = fmt.Fprintf(
			os.Stderr,
			"legacy transport sweep: the xtables lockfile %q is unavailable, skipping: %v\n",
			path,
			err,
		)

		return -1, false
	}

	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)

		_, _ = fmt.Fprintf(
			os.Stderr,
			"legacy transport sweep: the xtables lock %q is held, skipping the tick: %v\n",
			path,
			err,
		)

		return -1, false
	}

	return fd, true
}

// ensureLegacyTransportAcceptsOnce runs one read-modify-write of the legacy filter table under
// the xtables lock the caller holds, reporting whether the failure is a concurrent rewrite
// worth retrying from a fresh snapshot.
func ensureLegacyTransportAcceptsOnce(accepts []transportAccept) (retry bool, err error) {
	socket, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_RAW)
	if err != nil {
		return false, fmt.Errorf("opening the x_tables control socket: %w", err)
	}
	defer unix.Close(socket) //nolint:errcheck // nothing actionable on a closing control socket.

	info, entries, err := legacyReadTable(socket, legacyFilterTableName)
	if err != nil {
		if isToleratedLegacySweepError(err) {
			return false, nil
		}

		eagain := errors.Is(err, unix.EAGAIN)

		return eagain, fmt.Errorf("reading the legacy %q table: %w", legacyFilterTableName, err)
	}

	// A kernel without the INPUT hook hosts no device input policy either.
	if info.ValidHooks&(1<<legacyFilterInputHook) == 0 {
		return false, nil
	}

	insertion := int(info.HookEntry[legacyFilterInputHook])

	decoded, err := decodeLegacyEntries(entries)
	if err != nil {
		return false, fmt.Errorf("decoding the legacy %q entries: %w", legacyFilterTableName, err)
	}

	// Accept detection is scoped to the INPUT span: the kubelet probe traverses only INPUT,
	// so an accept in OUTPUT or a user chain must not suppress the insertion.
	inputStart := int(info.HookEntry[legacyFilterInputHook])
	inputEnd := int(info.Underflow[legacyFilterInputHook])

	rules := make([]legacyRule, 0, len(decoded))

	for _, entry := range decoded {
		if entry.Offset < inputStart || entry.Offset >= inputEnd {
			continue
		}

		rules = append(rules, entry.Rule)
	}

	var missing []transportAccept

	for _, accept := range accepts {
		if !legacyRulesAccept(rules, accept) {
			missing = append(missing, accept)
		}
	}

	if len(missing) == 0 {
		return false, nil
	}

	newEntries, newInfo, err := legacySpliceAccepts(info, entries, insertion, missing)
	if err != nil {
		return false, err
	}

	if err := legacySetReplace(socket, newInfo, newEntries); err != nil {
		eagain := errors.Is(err, unix.EAGAIN)

		return eagain, fmt.Errorf("replacing the legacy %q table: %w", legacyFilterTableName, err)
	}

	return false, nil
}

// isToleratedLegacySweepError reports whether an x_tables outcome means there is no legacy
// table to sweep here: a kernel or namespace without x_tables (EPROTONOSUPPORT, ENOPROTOOPT,
// EOPNOTSUPP, EAFNOSUPPORT), a table the device removed (ENOENT), or a transient
// kernel-helper read window (EFAULT). Like the netlink sweep's missing-family tolerance,
// they are success.
func isToleratedLegacySweepError(err error) bool {
	return errors.Is(err, unix.ENOENT) ||
		errors.Is(err, unix.EPROTONOSUPPORT) ||
		errors.Is(err, unix.EAFNOSUPPORT) ||
		errors.Is(err, unix.ENOPROTOOPT) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EFAULT)
}

// legacyReadTable snapshots one legacy table through IPT_SO_GET_INFO and IPT_SO_GET_ENTRIES,
// returning the info and the raw entries blob the ABI exchanges (without the entries header).
func legacyReadTable(socket int, name string) (*legacyGetinfo, []byte, error) {
	info, err := legacyGetTableInfo(socket, name)
	if err != nil {
		return nil, nil, err
	}

	entries, err := legacyGetTableEntries(socket, name, info.Size)
	if err != nil {
		return nil, nil, err
	}

	return info, entries, nil
}

// legacyGetTableInfo reads IPT_SO_GET_INFO for one table.
func legacyGetTableInfo(socket int, name string) (*legacyGetinfo, error) {
	if err := legacyCheckName(name); err != nil {
		return nil, err
	}

	request := &legacyGetinfo{}
	copy(request.Name[:], name)

	length := uint32(legacyGetinfoLength)

	if err := legacyGetsockopt(
		socket,
		unix.IPPROTO_IP,
		legacyGetInfoOption,
		unsafe.Pointer(request), //nolint:gosec // The x_tables ABI exchanges C structs through sockopt pointers.
		&length,
	); err != nil {
		return nil, fmt.Errorf("reading x_tables info for the %q table: %w", name, err)
	}

	// Captured pre-splice: SET_REPLACE's num_counters must be this count, not the edited
	// snapshot's.
	request.ReplaceCounters = request.NumEntries

	return request, nil
}

// legacyGetTableEntries reads IPT_SO_GET_ENTRIES for the snapshot info describes: the entries
// header names the table and carries the full entries size, and the kernel overwrites
// everything past the 8-byte-aligned head with the rules.
func legacyGetTableEntries(socket int, name string, size uint32) ([]byte, error) {
	if err := legacyCheckName(name); err != nil {
		return nil, err
	}

	if size == 0 {
		return nil, fmt.Errorf("the legacy %q table holds no entries", name)
	}

	head := legacyAligned(uintptr(legacyTableNameLength + legacyEntriesSizeFieldWidth))

	buffer := make([]byte, head+uintptr(size))

	copy(buffer, name)
	binary.NativeEndian.PutUint32(
		buffer[legacyTableNameLength:legacyTableNameLength+4],
		size,
	)

	length := uint32(len(buffer)) //nolint:gosec // The buffer sizes from the kernel's own table size field.

	if err := legacyGetsockopt(
		socket,
		unix.IPPROTO_IP,
		legacyGetEntriesOption,
		unsafe.Pointer(unsafe.SliceData(buffer)), //nolint:gosec // The x_tables ABI exchanges C structs through sockopt pointers.
		&length,
	); err != nil {
		return nil, fmt.Errorf("reading x_tables entries of the %q table: %w", name, err)
	}

	return buffer[head:], nil
}

// legacySpliceAccepts builds the edited snapshot: the new accept entries spliced at the head
// of the INPUT chain, with every hook_entry and underflow offset at or past the insertion
// point shifted and every jump target re-pointed.
func legacySpliceAccepts(
	info *legacyGetinfo,
	entries []byte,
	insertion int,
	missing []transportAccept,
) ([]byte, *legacyGetinfo, error) {
	// The insertion must land on an entry boundary: every x_tables entry is 8-byte aligned,
	// but entries are variable-length, so the boundary is the alignment, not a head multiple.
	if insertion > len(entries) || insertion%legacyExtensionAlignment != 0 {
		return nil, nil, fmt.Errorf(
			"the legacy INPUT chain's entry offset %d does not land on an entry boundary",
			insertion,
		)
	}

	var inserted [][]byte

	var shift uint32

	for _, accept := range missing {
		entry := newLegacyAcceptEntry(accept, accept.comment())
		inserted = append(inserted, entry)
		shift += uint32(len(entry)) //nolint:gosec // One accept entry's length, far below u32.
	}

	newEntries := make([]byte, 0, len(entries)+int(shift))

	newEntries = append(newEntries, entries[:insertion]...)

	for _, entry := range inserted {
		newEntries = append(newEntries, entry...)
	}

	newEntries = append(newEntries, entries[insertion:]...)

	newInfo := &legacyGetinfo{
		Name:            info.Name,
		ValidHooks:      info.ValidHooks,
		NumEntries:      info.NumEntries + uint32(len(missing)), //nolint:gosec // The snapshot's entry count plus the sweep's own handful of accepts.
		Size:            uint32(len(newEntries)),                //nolint:gosec // The spliced blob length, far below the kernel's u32 size field.
		HookEntry:       info.HookEntry,
		Underflow:       info.Underflow,
		ReplaceCounters: info.ReplaceCounters,
	}

	for hook := range newInfo.HookEntry {
		// The spliced hook's own chain head stays at the insertion offset: ipt_do_table
		// starts traversal at hook_entry[hook]; every underflow moves past the accepts.
		if hook != legacyFilterInputHook && newInfo.HookEntry[hook] >= uint32(insertion) { //nolint:gosec // A validated offset inside the snapshot blob.
			newInfo.HookEntry[hook] += shift
		}

		if newInfo.Underflow[hook] >= uint32(insertion) { //nolint:gosec // A validated offset inside the snapshot blob.
			newInfo.Underflow[hook] += shift
		}
	}

	legacyRepointJumps(newEntries, insertion, shift)

	return newEntries, newInfo, nil
}

// legacyRepointJumps re-points every standard target whose verdict encodes a jump crossing
// the insertion point. The walk starts at offset 0 so entries laid out before the INPUT chain
// are covered too: iptables never generates one, but a stale target landing on an entry
// boundary would pass mark_source_chains silently instead of being rejected.
func legacyRepointJumps(entries []byte, insertion int, shift uint32) {
	for offset := 0; offset+legacyEntryHeadLength <= len(entries); {
		// next_offset (u16 at entry+90) counts from the entry's own start, so the walk
		// advances by it.
		nextOffset := int(binary.NativeEndian.Uint16(entries[offset+90 : offset+92]))

		if nextOffset <= legacyEntryHeadLength || offset+nextOffset > len(entries) {
			break
		}

		legacyRepointOneJump(entries[offset:offset+nextOffset], insertion, shift)

		offset += nextOffset
	}
}

// legacyRepointOneJump adjusts one entry's jump target for the splice, if it carries one.
// The standard verdict is positive exactly when it encodes a jump: the plain blob offset the
// kernel chases in mark_source_chains; negative verdicts are the true outcomes and stay put.
func legacyRepointOneJump(entry []byte, insertion int, shift uint32) {
	targetOffset := int(binary.NativeEndian.Uint16(entry[88:90]))
	nextOffset := int(binary.NativeEndian.Uint16(entry[90:92]))

	if targetOffset+legacyTargetHeadLength+4 > nextOffset {
		return
	}

	targetName := nullTerminated(entry[targetOffset+2 : targetOffset+31])
	if targetName != "" {
		return
	}

	verdict := int64(int32(binary.NativeEndian.Uint32( //nolint:gosec // The standard verdict is int32 on the wire.
		entry[targetOffset+legacyTargetHeadLength : targetOffset+legacyTargetHeadLength+4],
	)))

	if verdict < 0 || verdict < int64(insertion) {
		return
	}

	binary.NativeEndian.PutUint32(
		entry[targetOffset+legacyTargetHeadLength:targetOffset+legacyTargetHeadLength+4],
		uint32(verdict+int64(shift)), //nolint:gosec // The repointed jump lands inside the spliced blob.
	)
}

// legacySetReplace swaps the spliced snapshot back through IPT_SO_SET_REPLACE: the
// ipt_replace head, the entry blob, and the counters copy-back region. The kernel validates
// num_counters against the replaced table's live entry count -- hence the pre-splice
// ReplaceCounters -- and copies num_counters*16 bytes into the pointer, leaving the spare slot
// unread slack.
func legacySetReplace(socket int, info *legacyGetinfo, blob []byte) error {
	head := legacyAligned(unsafe.Sizeof(legacyReplace{}))

	// One zeroed xt_counters (pcnt, bcnt) per REPLACED entry, plus one spare slot of slack
	// past the region the kernel actually copies; zeroed is the honest value -- the sweep
	// rewrites other devices' rules and keeps no counter history for them.
	countersLength := (uintptr(info.ReplaceCounters) + 1) * unsafe.Sizeof(xtCounters{})

	buffer := make([]byte, head+uintptr(len(blob))+countersLength)

	var tableName legacyInfoName
	copy(tableName[:], legacyFilterTableName)

	replace := legacyReplace{
		Name:        tableName,
		ValidHooks:  info.ValidHooks,
		NumEntries:  info.NumEntries,
		Size:        uint32(len(blob)), //nolint:gosec // The spliced blob length, far below the kernel's u32 size field.
		HookEntry:   info.HookEntry,
		Underflow:   info.Underflow,
		NumCounters: info.ReplaceCounters,
		Counters:    uintptr(unsafe.Pointer(&buffer[head+uintptr(len(blob))])), //nolint:gosec // Lifetime-bound: the uintptr is consumed by the syscall in this frame, where buffer is still referenced.
	}

	copy(buffer, legacyReplaceHeadBytes(&replace))
	copy(buffer[head:], blob)

	return legacySetsockopt(
		socket,
		unix.IPPROTO_IP,
		legacySetReplaceOption,
		unsafe.SliceData(buffer), //nolint:gosec // The x_tables ABI exchanges C structs through sockopt pointers.
		uint32(len(buffer)),      //nolint:gosec // The exchange buffer is the ABI-sized replace request.
	)
}

// legacyReplaceHeadBytes encodes the 96-byte ipt_replace head: the native-endian scalars in
// declaration order, ending with the counters copy-back pointer.
func legacyReplaceHeadBytes(replace *legacyReplace) []byte {
	head := make([]byte, legacyReplaceHeadLength)

	copy(head, replace.Name[:])
	binary.NativeEndian.PutUint32(head[32:36], replace.ValidHooks)
	binary.NativeEndian.PutUint32(head[36:40], replace.NumEntries)
	binary.NativeEndian.PutUint32(head[40:44], replace.Size)
	copy(head[44:64], legacyHookBytes(replace.HookEntry))
	copy(head[64:84], legacyHookBytes(replace.Underflow))
	binary.NativeEndian.PutUint32(head[84:88], replace.NumCounters)
	binary.NativeEndian.PutUint64(head[88:96], uint64(replace.Counters))

	return head
}

// legacyGetsockopt issues one getsockopt through the syscall layer. The optlen argument is
// both input and output on the ABI, so the length travels through its own pointer slot.
func legacyGetsockopt(socket, level, option int, destination unsafe.Pointer, length *uint32) error {
	if _, _, errno := unix.Syscall6(
		unix.SYS_GETSOCKOPT,
		uintptr(socket),
		uintptr(level),
		uintptr(option),
		uintptr(destination),
		uintptr(unsafe.Pointer(length)), //nolint:gosec // The sockopt length is an in/out kernel parameter.
		0,
	); errno != 0 {
		return errno
	}

	return nil
}

// legacySetsockopt issues one setsockopt through the syscall layer.
func legacySetsockopt(socket, level, option int, source *byte, length uint32) error {
	if _, _, errno := unix.Syscall6(
		unix.SYS_SETSOCKOPT,
		uintptr(socket),
		uintptr(level),
		uintptr(option),
		uintptr(unsafe.Pointer(source)), //nolint:gosec // The x_tables ABI exchanges C structs through sockopt pointers.
		uintptr(length),
		0,
	); errno != 0 {
		return errno
	}

	return nil
}

// legacyGetinfo mirrors struct ipt_getinfo as the kernel exchanges it on level IPPROTO_IP:
// name[32] + valid_hooks + hook_entry[5] + underflow[5] + num_entries + size.
type legacyGetinfo struct {
	Name       legacyInfoName
	ValidHooks uint32
	HookEntry  [5]uint32
	Underflow  [5]uint32
	NumEntries uint32
	Size       uint32
	// ReplaceCounters is the pre-splice entry count SET_REPLACE must submit as its
	// num_counters (kernel xt_replace_table validates it against the replaced table); it
	// rides the snapshot struct, not the wire ipt_getinfo.
	ReplaceCounters uint32
}

// legacyInfoName is the 32-byte name field shared by ipt_getinfo, ipt_get_entries, and
// ipt_replace.
type legacyInfoName [legacyTableNameLength]byte

// legacyReplace mirrors struct ipt_replace's head: name[32] + valid_hooks + num_entries +
// size + hook_entry[5] + underflow[5] + num_counters + the counters pointer.
type legacyReplace struct {
	Name        legacyInfoName
	ValidHooks  uint32
	NumEntries  uint32
	Size        uint32
	HookEntry   [5]uint32
	Underflow   [5]uint32
	NumCounters uint32
	Counters    uintptr
}

// xtCounters mirrors struct xt_counters: one per-entry pair of packet and byte counts.
type xtCounters struct {
	// Pcnt is the packet counter, Bcnt the byte counter.
	Pcnt uint64
	Bcnt uint64
}

// legacyHookBytes flattens one five-hook offset array.
func legacyHookBytes(hooks [5]uint32) []byte {
	flat := make([]byte, legacyHookArrayLength)

	for index, value := range hooks {
		binary.NativeEndian.PutUint32(flat[index*4:(index+1)*4], value)
	}

	return flat
}

// legacyAligned rounds one x_tables buffer offset to the 8-byte alignment the kernel applies
// to every entry and extension on 64-bit.
func legacyAligned(offset uintptr) uintptr {
	if offset%legacyExtensionAlignment == 0 {
		return offset
	}

	return offset + legacyExtensionAlignment - offset%legacyExtensionAlignment
}

// legacyCheckName validates one table name against the kernel's 32-byte field.
func legacyCheckName(name string) error {
	if name == "" || len(name) >= legacyTableNameLength {
		return fmt.Errorf("name %q does not fit the x_tables name field", name)
	}

	return nil
}

// newLegacyAcceptEntry encodes `iptables -I <chain> 1 -p <proto> --dport <port> -j ACCEPT`
// with the sweep's attribution comment, byte-exact per the x_tables ABI: an ipt_entry head, an
// xt_tcp or xt_udp match, the optional comment match, and a standard-target accept verdict.
func newLegacyAcceptEntry(accept transportAccept, comment string) []byte {
	protocolMatchLength := int(
		legacyAligned(uintptr(legacyMatchHeadLength + legacyUDPMatchDataLength)),
	)
	if accept.protocol == unix.IPPROTO_TCP {
		protocolMatchLength = int(
			legacyAligned(uintptr(legacyMatchHeadLength + legacyTCPMatchDataLength)),
		)
	}

	commentMatchLength := 0
	if comment != "" {
		commentMatchLength = int(
			legacyAligned(uintptr(legacyMatchHeadLength + legacyCommentMatchDataLength)),
		)
	}

	targetOffset := legacyEntryHeadLength + protocolMatchLength + commentMatchLength

	entry := make(
		[]byte,
		targetOffset+int(
			legacyAligned(uintptr(legacyTargetHeadLength+legacyStandardTargetDataLength)),
		),
	)

	// ipt_entry.ip: the sweep's accepts carry no addresses, inversions, or fragments, so the
	// ip head beyond the protocol stays zero; ip.proto is the u16 at ip+80.
	binary.NativeEndian.PutUint16(entry[80:82], uint16(accept.protocol))

	cursor := legacyEntryHeadLength

	// Each extension's payload is addressed from its own start, captured before the header
	// write consumes the extension's length off the cursor: xt headers carry their data
	// inline, so the data begins headerStart + headerSize, not at the post-header cursor.
	protocolStart := cursor

	cursor += writeLegacyExtensionHeader(
		entry[cursor:],
		protocolMatchName(accept.protocol),
		protocolMatchLength,
	)
	writeLegacyPortMatchData(
		entry[protocolStart+legacyMatchHeadLength:protocolStart+protocolMatchLength],
		accept,
	)

	if comment != "" {
		commentStart := cursor

		cursor += writeLegacyExtensionHeader(entry[cursor:], "comment", commentMatchLength)
		copy(
			entry[commentStart+legacyMatchHeadLength:commentStart+commentMatchLength],
			comment,
		)
	}

	// The standard target: an unnamed target header carrying a bare verdict is what the
	// kernel resolves as xt_standard_target.
	writeLegacyExtensionHeader(entry[cursor:], "", len(entry)-cursor)
	binary.NativeEndian.PutUint32(
		entry[cursor+legacyTargetHeadLength:cursor+legacyTargetHeadLength+4],
		legacyVerdictAccept,
	)

	// ipt_entry head: target_offset at 88, next_offset at 90.
	binary.NativeEndian.PutUint16(entry[88:90], uint16(targetOffset)) //nolint:gosec // x_tables offsets are u16 on the wire.
	binary.NativeEndian.PutUint16(entry[90:92], uint16(len(entry)))   //nolint:gosec // x_tables offsets are u16 on the wire.

	return entry
}

// protocolMatchName is the match extension name the kernel resolves for the protocol.
func protocolMatchName(protocol byte) string {
	if protocol == unix.IPPROTO_TCP {
		return legacyTCPMatchName
	}

	return legacyUDPMatchName
}

// writeLegacyExtensionHeader writes one xt_entry_match/xt_entry_target header: the padded
// extension size then the NUL-padded name. It returns the bytes the extension consumes.
func writeLegacyExtensionHeader(destination []byte, name string, size int) int {
	binary.NativeEndian.PutUint16(destination[0:2], uint16(size)) //nolint:gosec // x_tables extension sizes are u16 on the wire.

	copy(destination[2:31], name)

	return size
}

// writeLegacyPortMatchData fills the xt_tcp/xt_udp payload: the full source range, the exact
// destination port, no inversions.
func writeLegacyPortMatchData(destination []byte, accept transportAccept) {
	binary.NativeEndian.PutUint16(destination[0:2], 0)
	binary.NativeEndian.PutUint16(destination[2:4], legacyPortMax)
	binary.NativeEndian.PutUint16(destination[4:6], accept.port)
	binary.NativeEndian.PutUint16(destination[6:8], accept.port)
}

// legacyDecodedEntry is one decoded entry with the byte offset it sits at inside the entries
// blob, the position the chain spans bucket rules by.
type legacyDecodedEntry struct {
	// Offset is the entry's byte offset inside the entries blob.
	Offset int
	// Rule is the decoded rule shape.
	Rule legacyRule
}

// decodeLegacyEntries walks one kernel entries blob, returning one decoded entry per rule so
// the sweep and the tests can assert chain contents without the kernel.
func decodeLegacyEntries(entries []byte) ([]legacyDecodedEntry, error) {
	var rules []legacyDecodedEntry

	for offset := 0; offset < len(entries); {
		if len(entries)-offset < legacyEntryHeadLength {
			return nil, fmt.Errorf("entry at %d truncates the ipt_entry head", offset)
		}

		// target_offset and next_offset (u16s at entry+88 and entry+90) count from the
		// entry's own start per the x_tables ABI: the kernel walks entries with entry +
		// next_offset and reads the target at entry + target_offset, so the walk advances
		// by the raw fields, not to them.
		targetOffset := int(binary.NativeEndian.Uint16(entries[offset+88 : offset+90]))
		nextOffset := int(binary.NativeEndian.Uint16(entries[offset+90 : offset+92]))

		if nextOffset <= legacyEntryHeadLength ||
			offset+nextOffset > len(entries) ||
			targetOffset < legacyEntryHeadLength ||
			targetOffset > nextOffset {
			return nil, fmt.Errorf(
				"entry at %d has inconsistent offsets (target %d, next %d)",
				offset, targetOffset, nextOffset,
			)
		}

		rule, err := decodeLegacyRule(entries[offset:offset+nextOffset], targetOffset)
		if err != nil {
			return nil, fmt.Errorf("decoding the entry at %d: %w", offset, err)
		}

		rules = append(rules, legacyDecodedEntry{Offset: offset, Rule: rule})

		offset += nextOffset
	}

	return rules, nil
}

// decodeLegacyRule decodes one entry into the sweep's rule shape, reading the protocol from
// the ip head's u16 at ip+80 and the dport, verdict, and comment from the extensions.
func decodeLegacyRule(entry []byte, targetOffset int) (legacyRule, error) {
	rule := legacyRule{Protocol: byte(binary.NativeEndian.Uint16(entry[80:82]))} //nolint:gosec // ip.proto is a u8 protocol number the kernel sends in a u16 field.

	cursor := legacyEntryHeadLength

	for cursor < targetOffset {
		if targetOffset-cursor < legacyMatchHeadLength {
			return rule, fmt.Errorf("extension at %d truncates the match header", cursor)
		}

		matchSize := int(binary.NativeEndian.Uint16(entry[cursor : cursor+2]))
		matchName := nullTerminated(entry[cursor+2 : cursor+31])

		if matchSize < legacyMatchHeadLength || cursor+matchSize > targetOffset {
			return rule, fmt.Errorf(
				"match %q at %d overruns the target boundary",
				matchName,
				cursor,
			)
		}

		if matchName == "comment" {
			rule.Comment = nullTerminated(entry[cursor+legacyMatchHeadLength : cursor+matchSize])
		}

		if matchName == legacyTCPMatchName || matchName == legacyUDPMatchName {
			if matchSize < legacyMatchHeadLength+8 {
				return rule, fmt.Errorf("match %q truncates the port range", matchName)
			}

			// xt_tcp/xt_udp lead with the source range; the destination range follows.
			rule.Port = binary.NativeEndian.Uint16(
				entry[cursor+legacyMatchHeadLength+4 : cursor+legacyMatchHeadLength+6],
			)
		}

		cursor += matchSize
	}

	if targetOffset+legacyTargetHeadLength+4 > len(entry) {
		return rule, errors.New("entry truncates the target")
	}

	targetName := nullTerminated(entry[targetOffset+2 : targetOffset+31])

	if targetName == "" {
		rule.Accept = binary.NativeEndian.Uint32(
			entry[targetOffset+legacyTargetHeadLength:targetOffset+legacyTargetHeadLength+4],
		) == legacyVerdictAccept
	}

	return rule, nil
}

// nullTerminated trims one fixed x_tables string field at its first NUL.
func nullTerminated(field []byte) string {
	end := 0

	for end < len(field) && field[end] != 0 {
		end++
	}

	return string(field[:end])
}

// listLegacyChainRules decodes one legacy chain's rules in order.
func listLegacyChainRules(tableName, chainName string) ([]legacyRule, error) {
	if tableName != legacyFilterTableName {
		return nil, fmt.Errorf("no legacy %q table: the sweep asserts the filter table", tableName)
	}

	if err := legacyCheckName(chainName); err != nil {
		return nil, err
	}

	socket, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_RAW)
	if err != nil {
		return nil, fmt.Errorf("opening the x_tables control socket: %w", err)
	}
	defer unix.Close(socket) //nolint:errcheck // nothing actionable on a closing control socket.

	info, entries, err := legacyReadTable(socket, tableName)
	if err != nil {
		return nil, fmt.Errorf("reading the legacy %q table: %w", tableName, err)
	}

	decoded, err := decodeLegacyEntries(entries)
	if err != nil {
		return nil, fmt.Errorf("decoding the legacy %q table: %w", legacyFilterTableName, err)
	}

	// Base chains are ordered by their entry offsets; only rules inside a base chain's span
	// are bucketed by name, and trailing entries past the last base span stay out.
	for _, span := range legacyChainSpans(info) {
		if span.Name != chainName {
			continue
		}

		var rules []legacyRule

		for _, entry := range decoded {
			if entry.Offset >= int(span.Entry) && entry.Offset < int(span.End) {
				rules = append(rules, entry.Rule)
			}
		}

		return rules, nil
	}

	return nil, fmt.Errorf("the legacy %q table carries no %q chain", tableName, chainName)
}

// legacyRulesAccept reports whether any decoded rule already accepts one accept's port and
// protocol -- an accept with any comment satisfies the sweep; it is the accept that counts.
func legacyRulesAccept(rules []legacyRule, accept transportAccept) bool {
	for _, rule := range rules {
		if accept.protocol == unix.IPPROTO_TCP && rule.AcceptsTCP(accept.port) {
			return true
		}

		if accept.protocol == unix.IPPROTO_UDP && rule.AcceptsUDP(accept.port) {
			return true
		}
	}

	return false
}
