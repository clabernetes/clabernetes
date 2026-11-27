//go:build linux

//nolint:testpackage // dense fixture-driven checks exercise one boundary end to end.
package directruntime

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

// legacySyntheticAcceptEntry builds one entry blob by hand per the x_tables ABI, without going
// through the sweep's encoder: an ipt_entry head carrying the protocol, one tcp or udp match,
// an optional comment match, and a bare standard-target verdict. Round-tripping the sweep's
// encoding against this blob asserts the ABI shape independently.
func legacySyntheticAcceptEntry(accept transportAccept, comment string) []byte {
	protocolMatchLength := legacyMatchHeadLength + legacyUDPMatchDataLength
	if accept.protocol == unix.IPPROTO_TCP {
		protocolMatchLength = legacyMatchHeadLength + legacyTCPMatchDataLength
	}

	paddedProtocolMatchLength := int(legacyAligned(uintptr(protocolMatchLength)))

	commentMatchLength := 0
	if comment != "" {
		commentMatchLength = int(
			legacyAligned(uintptr(legacyMatchHeadLength + legacyCommentMatchDataLength)),
		)
	}

	targetLength := int(
		legacyAligned(uintptr(legacyTargetHeadLength + legacyStandardTargetDataLength)),
	)

	entry := make(
		[]byte,
		legacyEntryHeadLength+paddedProtocolMatchLength+commentMatchLength+targetLength,
	)

	// ip.proto is the u16 at ip+80.
	binary.NativeEndian.PutUint16(entry[80:82], uint16(accept.protocol))

	// target_offset and next_offset are the u16s at entry+88 and entry+90.
	binary.NativeEndian.PutUint16(
		entry[88:90],
		uint16(legacyEntryHeadLength+protocolMatchLength+commentMatchLength),
	)
	binary.NativeEndian.PutUint16(entry[90:92], uint16(len(entry))) //nolint:gosec // A synthetic entry size, far below u16.

	// xt_entry_match header: match_size, then the NUL-padded extension name.
	binary.NativeEndian.PutUint16(
		entry[legacyEntryHeadLength:legacyEntryHeadLength+2],
		uint16(protocolMatchLength),
	)

	protocolName := protocolMatchName(accept.protocol)

	copy(entry[legacyEntryHeadLength+2:legacyEntryHeadLength+31], protocolName)

	// xt_tcp/xt_udp: sport min+max, then dport min+max.
	binary.NativeEndian.PutUint16(entry[legacyEntryHeadLength+legacyMatchHeadLength:], 0)
	binary.NativeEndian.PutUint16(entry[legacyEntryHeadLength+legacyMatchHeadLength+2:], 65535)
	binary.NativeEndian.PutUint16(
		entry[legacyEntryHeadLength+legacyMatchHeadLength+4:],
		accept.port,
	)
	binary.NativeEndian.PutUint16(
		entry[legacyEntryHeadLength+legacyMatchHeadLength+6:],
		accept.port,
	)

	if comment != "" {
		commentStart := legacyEntryHeadLength + protocolMatchLength

		binary.NativeEndian.PutUint16(
			entry[commentStart:commentStart+2],
			uint16(commentMatchLength),
		)
		copy(entry[commentStart+2:commentStart+31], "comment")
		copy(
			entry[commentStart+legacyMatchHeadLength:commentStart+legacyMatchHeadLength+len(comment)],
			comment,
		)
	}

	targetOffset := int(binary.NativeEndian.Uint16(entry[88:90]))

	// xt_entry_target header for the standard target: target_size, empty name.
	binary.NativeEndian.PutUint16(entry[targetOffset:targetOffset+2], uint16(targetLength)) //nolint:gosec // A synthetic target size, far below u16.

	// The standard target's verdict: NF_ACCEPT encoded -NF_VERDICT-1.
	binary.NativeEndian.PutUint32(entry[targetOffset+legacyTargetHeadLength:], legacyVerdictAccept)

	return entry
}

// assertLegacyAcceptEntryShape asserts one encoded entry's raw ABI fields: the padded size
// math, the target_offset/next_offset consistency, and the ip protocol placement.
func assertLegacyAcceptEntryShape(
	t *testing.T,
	accept transportAccept,
	comment string,
	protocolLength int,
) {
	t.Helper()

	entry := newLegacyAcceptEntry(accept, comment)

	// The head sizes: entry = 112 + padded protocol match + optional padded comment
	// match + padded standard target (32+4 aligns to 40). For the commented UDP rule:
	// 112 + 48 + 288 + 40 = 488.
	expectedCommentLength := 0
	if comment != "" {
		expectedCommentLength = int(
			legacyAligned(uintptr(legacyMatchHeadLength + legacyCommentMatchDataLength)),
		)
	}

	expectedTargetLength := int(
		legacyAligned(uintptr(legacyTargetHeadLength + legacyStandardTargetDataLength)),
	)
	expectedTotal := legacyEntryHeadLength + int(
		legacyAligned(uintptr(protocolLength)),
	) + expectedCommentLength + expectedTargetLength

	if len(entry) != expectedTotal {
		t.Fatalf(
			"entry total size: got %d, want %d (head %d + protocol %d + comment %d + target %d)",
			len(entry),
			expectedTotal,
			legacyEntryHeadLength,
			int(legacyAligned(uintptr(protocolLength))),
			expectedCommentLength,
			expectedTargetLength,
		)
	}

	expectedTargetOffset := legacyEntryHeadLength + int(
		legacyAligned(uintptr(protocolLength)),
	) + expectedCommentLength

	targetOffset := int(binary.NativeEndian.Uint16(entry[88:90]))
	nextOffset := int(binary.NativeEndian.Uint16(entry[90:92]))

	if targetOffset != expectedTargetOffset {
		t.Fatalf("target_offset: got %d, want %d", targetOffset, expectedTargetOffset)
	}

	if nextOffset != len(entry) {
		t.Fatalf("next_offset: got %d, want the entry length %d", nextOffset, len(entry))
	}

	if targetOffset >= nextOffset {
		t.Fatalf(
			"target_offset %d must sit before next_offset %d",
			targetOffset,
			nextOffset,
		)
	}

	// The protocol is the u16 at ip+80.
	if got := binary.NativeEndian.Uint16(entry[80:82]); got != uint16(accept.protocol) {
		t.Fatalf("ip proto: got %d, want %d", got, accept.protocol)
	}
}

// assertLegacyAcceptEntryRoundTrip decodes the sweep's encoded entry back into a rule and
// asserts the round-trip against the accept, plus the verdict's exact NF_ACCEPT encoding.
func assertLegacyAcceptEntryRoundTrip(t *testing.T, accept transportAccept, comment string) {
	t.Helper()

	entry := newLegacyAcceptEntry(accept, comment)

	targetOffset := int(binary.NativeEndian.Uint16(entry[88:90]))

	// The rule round-trips: the sweep's decoder reads back the same accept.
	rule, err := decodeLegacyRule(entry, targetOffset)
	if err != nil {
		t.Fatalf("decoding the encoded entry: %v", err)
	}

	if rule.Protocol != accept.protocol {
		t.Fatalf("decoded protocol: got %d, want %d", rule.Protocol, accept.protocol)
	}

	if !rule.Accept {
		t.Fatalf("decoded rule does not terminate with an ACCEPT verdict: %+v", rule)
	}

	if rule.Port != accept.port {
		t.Fatalf("decoded dport: got %d, want %d", rule.Port, accept.port)
	}

	if rule.Comment != comment {
		t.Fatalf("decoded comment: got %q, want %q", rule.Comment, comment)
	}

	// The verdict stays 0xFFFFFFFE for NF_ACCEPT, encoding -NF_ACCEPT-1.
	verdictOffset := targetOffset + legacyTargetHeadLength
	if got := binary.NativeEndian.Uint32(
		entry[verdictOffset : verdictOffset+4],
	); got != legacyVerdictAccept {
		t.Fatalf("verdict: got 0x%08X, want 0x%08X", got, legacyVerdictAccept)
	}
}

// assertLegacySyntheticRoundTrip asserts the sweep's encoder against the hand-built blob for
// the same rule: the encoder and its decoder agree with the ABI as written independently.
func assertLegacySyntheticRoundTrip(t *testing.T, accept transportAccept, comment string) {
	t.Helper()

	entry := newLegacyAcceptEntry(accept, comment)
	synthetic := legacySyntheticAcceptEntry(accept, comment)

	if len(synthetic) != len(entry) {
		t.Fatalf(
			"hand-built entry length %d disagrees with the encoder's %d",
			len(synthetic), len(entry),
		)
	}

	decodedSynthetic, err := decodeLegacyEntries(synthetic)
	if err != nil {
		t.Fatalf("decoding the hand-built blob: %v", err)
	}

	if len(decodedSynthetic) != 1 {
		t.Fatalf("hand-built blob decoded into %d entries, want one", len(decodedSynthetic))
	}

	decodedRule := decodedSynthetic[0].Rule
	if decodedRule.Protocol != accept.protocol ||
		decodedRule.Port != accept.port ||
		!decodedRule.Accept ||
		decodedRule.Comment != comment {
		t.Fatalf(
			"hand-built blob decoded %+v, want protocol %d port %d accept comment %q",
			decodedRule, accept.protocol, accept.port, comment,
		)
	}
}

// TestLegacyAcceptEntryEncodesIptablesShape asserts the sweep's entry encoding against the ABI
// shapes, without a kernel: the padded size math, the target_offset/next_offset consistency,
// and a full decode round-trip against a hand-built blob for the same rule.
func TestLegacyAcceptEntryEncodesIptablesShape(t *testing.T) {
	tests := []struct {
		name           string
		accept         transportAccept
		comment        string
		protocolLength int
	}{
		{
			name:           "tcp accept with comment",
			accept:         transportAccept{protocol: unix.IPPROTO_TCP, port: 14791},
			comment:        "c9s-transport-tcp-14791",
			protocolLength: legacyMatchHeadLength + legacyTCPMatchDataLength,
		},
		{
			name:           "udp accept with comment",
			accept:         transportAccept{protocol: unix.IPPROTO_UDP, port: 14790},
			comment:        "c9s-transport-14790",
			protocolLength: legacyMatchHeadLength + legacyUDPMatchDataLength,
		},
		{
			name:           "tcp accept without comment",
			accept:         transportAccept{protocol: unix.IPPROTO_TCP, port: 14791},
			comment:        "",
			protocolLength: legacyMatchHeadLength + legacyTCPMatchDataLength,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertLegacyAcceptEntryShape(t, test.accept, test.comment, test.protocolLength)
			assertLegacyAcceptEntryRoundTrip(t, test.accept, test.comment)
			assertLegacySyntheticRoundTrip(t, test.accept, test.comment)
		})
	}
}

// TestLegacyCommentRoundTrips asserts the comment match specifically: the payload is a fixed
// 256-byte NUL-terminated field, and the sweep's own comment survives one encode/decode cycle.
func TestLegacyCommentRoundTrips(t *testing.T) {
	accept := transportAccept{protocol: unix.IPPROTO_UDP, port: 14790}
	comment := "c9s-transport-14790"

	entry := newLegacyAcceptEntry(accept, comment)

	decoded, err := decodeLegacyEntries(entry)
	if err != nil {
		t.Fatalf("decoding the encoded entry: %v", err)
	}

	if len(decoded) != 1 {
		t.Fatalf("decoded %d entries, want one", len(decoded))
	}

	rule := decoded[0].Rule
	if rule.Comment != comment {
		t.Fatalf("decoded comment %q, want %q", rule.Comment, comment)
	}

	if !rule.AcceptsUDP(14790) {
		t.Fatalf("decoded rule does not accept udp/14790: %+v", rule)
	}
}

// TestLegacyAcceptsDetectRules asserts the sweep's accept detection over decoded rule shapes a
// device's ruleset carries: a bare accept, an accept with a comment, and a drop the sweep must
// not mistake for an accept.
func TestLegacyAcceptsDetectRules(t *testing.T) {
	rules := []legacyRule{
		{Protocol: unix.IPPROTO_TCP, Port: 14791, Accept: true},
		{Protocol: unix.IPPROTO_UDP, Port: 14790, Accept: true, Comment: "c9s-transport-14790"},
		{Protocol: unix.IPPROTO_UDP, Port: 14790, Accept: false},
	}

	if !legacyRulesAccept(rules, transportAccept{protocol: unix.IPPROTO_TCP, port: 14791}) {
		t.Fatal("a present tcp accept was not detected")
	}

	if !legacyRulesAccept(rules, transportAccept{protocol: unix.IPPROTO_UDP, port: 14790}) {
		t.Fatal("a present udp accept was not detected")
	}

	if legacyRulesAccept(rules, transportAccept{protocol: unix.IPPROTO_TCP, port: 80}) {
		t.Fatal("a foreign port read as an accept")
	}

	if legacyRulesAccept(rules, transportAccept{protocol: unix.IPPROTO_UDP, port: 14791}) {
		t.Fatal("a dropped rule read as an accept")
	}
}

// TestLegacySpliceAcceptsShiftsHooks asserts the splice's offset bookkeeping on a synthetic
// snapshot: a PREROUTING rule before INPUT, the INPUT drop policy rule, and the accept spliced
// between them.
func TestLegacySpliceAcceptsShiftsHooks(t *testing.T) {
	// The blob holds two entries, both kernel-shaped head + bare standard target: the
	// PREROUTING policy accept at 0 and the INPUT drop policy at 152 (entries are 8-byte
	// aligned and variable-length, so 152 is a legal INPUT chain start). Hook and underflow
	// offsets name the blob offsets the kernel walks.
	info := &legacyGetinfo{
		ValidHooks: 0x3, // PREROUTING(0), INPUT(1) bits the filter table carries.
		HookEntry:  [5]uint32{0, 152, 0, 0, 0},
		Underflow:  [5]uint32{0, 152, 0, 0, 0},
		NumEntries: 2,
		Size:       304,
	}

	entries := make([]byte, 304)

	// A bare policy rule is head + padded standard target: IPT_ALIGN pads the verdict
	// region to 40 bytes on 64-bit, so the rule spans 152 and 152 is a legal chain start.
	policyRuleLength := legacyEntryHeadLength +
		int(legacyAligned(uintptr(legacyTargetHeadLength+legacyStandardTargetDataLength)))

	// The PREROUTING policy accept at 0: head, then the standard verdict.
	binary.NativeEndian.PutUint16(entries[88:90], uint16(legacyEntryHeadLength))
	binary.NativeEndian.PutUint16(entries[90:92], uint16(policyRuleLength)) //nolint:gosec // A synthetic entry length, far below u16.
	binary.NativeEndian.PutUint32(
		entries[legacyEntryHeadLength+legacyTargetHeadLength:],
		legacyVerdictAccept,
	)

	// The INPUT drop policy at 152: head, then the standard verdict.
	binary.NativeEndian.PutUint16(entries[152+88:152+90], uint16(legacyEntryHeadLength))
	binary.NativeEndian.PutUint16(entries[152+90:152+92], uint16(policyRuleLength)) //nolint:gosec // A synthetic entry length, far below u16.
	binary.NativeEndian.PutUint32(
		entries[152+legacyEntryHeadLength+legacyTargetHeadLength:],
		0xFFFFFFFF, // The NF_DROP policy verdict.
	)

	accepts := []transportAccept{{protocol: unix.IPPROTO_UDP, port: 14790}}

	newEntries, newInfo, err := legacySpliceAccepts(info, entries, 152, accepts)
	if err != nil {
		t.Fatalf("splicing the accepts: %v", err)
	}

	// The udp accept entry lands first, at the INPUT head.
	acceptEntry := newLegacyAcceptEntry(accepts[0], accepts[0].comment())
	if !bytes.Contains(newEntries, acceptEntry) {
		t.Fatal("the spliced blob does not carry the udp accept entry")
	}

	// The hook and underflow offsets moved by the new entry's length. The spliced INPUT
	// hook's own hook_entry stays at the insertion offset -- the accepts become INPUT's
	// first rules and ipt_do_table starts traversal at hook_entry[INPUT] -- while its
	// underflow (the drop policy rule) moves past them.
	shift := uint32(len(acceptEntry)) //nolint:gosec // One accept entry's length, far below u32.
	if newInfo.HookEntry[1] != 152 {
		t.Fatalf(
			"INPUT hook_entry: got %d, want 152 (the spliced chain head stays put)",
			newInfo.HookEntry[1],
		)
	}

	if newInfo.Underflow[1] != 152+shift {
		t.Fatalf("INPUT underflow: got %d, want %d", newInfo.Underflow[1], 152+shift)
	}

	if newInfo.NumEntries != 3 {
		t.Fatalf("num_entries: got %d, want 3", newInfo.NumEntries)
	}

	if newInfo.Size != uint32(len(newEntries)) { //nolint:gosec // The spliced blob length, far below u32.
		t.Fatalf("entries size: got %d, want %d", newInfo.Size, len(newEntries))
	}

	// The spliced blob still decodes cleanly into the policy rule and the accept.
	decodedEntries, err := decodeLegacyEntries(newEntries)
	if err != nil {
		t.Fatalf("decoding the spliced blob: %v", err)
	}

	if len(decodedEntries) != 3 {
		t.Fatalf("the spliced blob holds %d entries, want 3", len(decodedEntries))
	}
}

// legacyWriteBareVerdictEntry writes one bare kernel-shaped entry (head + padded standard
// target) carrying one verdict at the blob offset: the shape every policy and jump rule in
// the synthetic fixtures takes.
func legacyWriteBareVerdictEntry(blob []byte, offset int, verdict uint32) {
	entryLength := legacyEntryHeadLength +
		int(legacyAligned(uintptr(legacyTargetHeadLength+legacyStandardTargetDataLength)))

	// The head fields: target_offset at 112 (a bare rule carries no matches) and
	// next_offset, then the verdict past the padded target header.
	binary.NativeEndian.PutUint16(blob[offset+88:offset+90], uint16(legacyEntryHeadLength))
	binary.NativeEndian.PutUint16(blob[offset+90:offset+92], uint16(entryLength)) //nolint:gosec // A synthetic entry length, far below u16.
	binary.NativeEndian.PutUint32(
		blob[offset+legacyEntryHeadLength+legacyTargetHeadLength:],
		verdict,
	)
}

// TestLegacyRepointJumpsShiftsTargets asserts the splice's jump repointing on a synthetic
// multi-hook snapshot: a jump from the INPUT chain into a user chain above the insertion
// point gains the shift, while negative verdicts and a jump targeting below the insertion
// point stay put.
func TestLegacyRepointJumpsShiftsTargets(t *testing.T) {
	// The blob holds six kernel-shaped entries, all head + bare standard target (152 bytes
	// each): the PREROUTING policy accept at 0, a PREROUTING jump into a user chain at 152
	// (a below-insertion target), the user chain's rule at 304, the INPUT jump across the
	// insertion point at 456, the INPUT drop policy at 608, and the user chain's rule
	// carrying a back-jump below the insertion at 760.
	entries := make([]byte, 912)

	legacyWriteBareVerdictEntry(entries, 0, legacyVerdictAccept)
	legacyWriteBareVerdictEntry(entries, 152, 304)
	legacyWriteBareVerdictEntry(entries, 304, legacyVerdictAccept)
	legacyWriteBareVerdictEntry(entries, 456, 760)
	legacyWriteBareVerdictEntry(entries, 608, legacyVerdictDrop)
	legacyWriteBareVerdictEntry(entries, 760, 304)

	info := &legacyGetinfo{
		ValidHooks: 0x3, // PREROUTING(0), INPUT(1) bits the filter table carries.
		HookEntry:  [5]uint32{0, 456, 0, 0, 0},
		Underflow:  [5]uint32{0, 608, 0, 0, 0},
		NumEntries: 6,
		Size:       912,
	}

	accepts := []transportAccept{{protocol: unix.IPPROTO_UDP, port: 14790}}

	newEntries, newInfo, err := legacySpliceAccepts(info, entries, 456, accepts)
	if err != nil {
		t.Fatalf("splicing the accepts: %v", err)
	}

	acceptEntry := newLegacyAcceptEntry(accepts[0], accepts[0].comment())
	if !bytes.Contains(newEntries, acceptEntry) {
		t.Fatal("the spliced blob does not carry the udp accept entry")
	}

	// The repointed blob still decodes cleanly, and the hook bookkeeping matches the
	// splice: the INPUT head stays put while its underflow moves past the accepts.
	decodedEntries, err := decodeLegacyEntries(newEntries)
	if err != nil {
		t.Fatalf("decoding the spliced blob: %v", err)
	}

	if len(decodedEntries) != 7 {
		t.Fatalf("the spliced blob holds %d entries, want 7", len(decodedEntries))
	}

	if newInfo.HookEntry[1] != 456 {
		t.Fatalf("INPUT hook_entry: got %d, want 456 (the spliced chain head stays put)", newInfo.HookEntry[1])
	}

	if newInfo.Underflow[1] != 608+uint32(len(acceptEntry)) { //nolint:gosec // Synthetic offsets stay far below u32.
		t.Fatalf(
			"INPUT underflow: got %d, want %d",
			newInfo.Underflow[1],
			608+uint32(len(acceptEntry)), //nolint:gosec // Synthetic offsets stay far below u32.
		)
	}

	// Every bare entry carries its verdict at its own offset+112+32.
	verdictAt := func(offset int) uint32 {
		return binary.NativeEndian.Uint32(newEntries[offset+legacyEntryHeadLength+legacyTargetHeadLength:])
	}

	// The crossing jump (INPUT into the user chain above the insertion point) gains the
	// shift: it sat at 456 in the old blob, now at 944, and its 760 target moved past the
	// accept entries.
	if got := verdictAt(944); got != 760+uint32(len(acceptEntry)) { //nolint:gosec // Synthetic offsets stay far below u32.
		t.Fatalf("crossing jump verdict: got %d, want %d", got, 760+uint32(len(acceptEntry))) //nolint:gosec // Synthetic offsets stay far below u32.
	}

	// Negative verdicts are the true outcomes and stay put, whatever their position.
	if got := verdictAt(0); got != legacyVerdictAccept {
		t.Fatalf("PREROUTING policy verdict moved: got %d, want %d", got, legacyVerdictAccept)
	}

	if got := verdictAt(304); got != legacyVerdictAccept {
		t.Fatalf("user chain rule verdict moved: got %d, want %d", got, legacyVerdictAccept)
	}

	if got := verdictAt(1096); got != legacyVerdictDrop {
		t.Fatalf("INPUT policy verdict moved: got %d, want %d", got, legacyVerdictDrop)
	}

	// A jump below the insertion point stays put: the PREROUTING jump targeting the user
	// chain at 304, and the user chain's own back-jump to it, keep their plain offsets.
	if got := verdictAt(152); got != 304 {
		t.Fatalf("below-insertion jump verdict moved: got %d, want 304", got)
	}

	if got := verdictAt(1248); got != 304 {
		t.Fatalf("below-insertion back-jump verdict moved: got %d, want 304", got)
	}
}

// TestLegacyRepointPreInsertionForwardJump asserts the repointing walk covers entries laid
// out before the INPUT chain: a user chain there with a forward jump past the insertion
// point would keep a stale target that mark_source_chains accepts silently.
func TestLegacyRepointPreInsertionForwardJump(t *testing.T) {
	// Four entries (head + bare standard target, 152 bytes each): a user chain rule at 0
	// jumping to 456 (above the insertion point), an ACCEPT at 152, the INPUT jump at 456
	// targeting the drop policy at 608.
	entries := make([]byte, 760)

	legacyWriteBareVerdictEntry(entries, 0, 456)
	legacyWriteBareVerdictEntry(entries, 152, legacyVerdictAccept)
	legacyWriteBareVerdictEntry(entries, 456, 608)
	legacyWriteBareVerdictEntry(entries, 608, legacyVerdictDrop)

	info := &legacyGetinfo{
		ValidHooks: 0x2, // INPUT(1) only: the table shape the hardening targets.
		HookEntry:  [5]uint32{0, 456, 0, 0, 0},
		Underflow:  [5]uint32{0, 608, 0, 0, 0},
		NumEntries: 4,
		Size:       760,
	}

	accepts := []transportAccept{{protocol: unix.IPPROTO_TCP, port: 14791}}

	newEntries, _, err := legacySpliceAccepts(info, entries, 456, accepts)
	if err != nil {
		t.Fatalf("splicing the accepts: %v", err)
	}

	acceptEntry := newLegacyAcceptEntry(accepts[0], accepts[0].comment())

	verdictAt := func(blob []byte, offset int) uint32 {
		return binary.NativeEndian.Uint32(blob[offset+legacyEntryHeadLength+legacyTargetHeadLength:])
	}

	// The pre-insertion user-chain entry at 0 sat before the old walk's start; its jump to
	// 456 (the old insertion point, now covered by the inserted accepts) must gain the
	// shift so it still lands on the crossing INPUT jump's entry.
	if got := verdictAt(newEntries, 0); got != 456+uint32(len(acceptEntry)) { //nolint:gosec // Synthetic offsets stay far below u32.
		t.Fatalf("pre-insertion forward jump verdict: got %d, want %d", got, 456+uint32(len(acceptEntry))) //nolint:gosec // Synthetic offsets stay far below u32.
	}

	// The negative verdict below the insertion point stays put.
	if got := verdictAt(newEntries, 152); got != legacyVerdictAccept {
		t.Fatalf("negative verdict moved: got %d, want %d", got, legacyVerdictAccept)
	}
}

// TestLegacyDecodeRejectsMalformedEntries asserts the decoder fails closed on hostile blobs:
// every inconsistent x_tables shape returns an error rather than reading out of bounds, so a
// corrupted kernel answer can never steer the sweep.
func TestLegacyDecodeRejectsMalformedEntries(t *testing.T) {
	// A valid bare entry: head + padded standard target, verdict at offset+144.
	bareEntry := func(verdict uint32) []byte {
		entry := make(
			[]byte,
			legacyEntryHeadLength+
				int(legacyAligned(uintptr(legacyTargetHeadLength+legacyStandardTargetDataLength))),
		)

		legacyWriteBareVerdictEntry(entry, 0, verdict)

		return entry
	}

	tests := []struct {
		name string
		blob []byte
	}{
		{
			name: "truncated ipt_entry head",
			blob: bareEntry(legacyVerdictAccept)[:60],
		},
		{
			name: "next_offset at the head boundary",
			blob: func() []byte {
				blob := bareEntry(legacyVerdictAccept)
				binary.NativeEndian.PutUint16(blob[90:92], uint16(legacyEntryHeadLength))

				return blob
			}(),
		},
		{
			name: "next_offset past the blob end",
			blob: func() []byte {
				blob := bareEntry(legacyVerdictAccept)
				binary.NativeEndian.PutUint16(blob[90:92], 200)

				return blob
			}(),
		},
		{
			name: "target_offset before the entry head",
			blob: func() []byte {
				blob := bareEntry(legacyVerdictAccept)
				binary.NativeEndian.PutUint16(blob[88:90], 100)

				return blob
			}(),
		},
		{
			name: "target_offset past next_offset",
			blob: func() []byte {
				blob := bareEntry(legacyVerdictAccept)
				binary.NativeEndian.PutUint16(blob[88:90], 200)
				binary.NativeEndian.PutUint16(blob[90:92], 152)

				return blob
			}(),
		},
		{
			name: "match size overruns the target boundary",
			blob: func() []byte {
				blob := make([]byte, 200)
				binary.NativeEndian.PutUint16(blob[88:90], 152)
				binary.NativeEndian.PutUint16(blob[90:92], 200)

				// A tcp match header claiming 48 bytes, overrunning the target at 152.
				binary.NativeEndian.PutUint16(blob[112:114], 48)
				copy(blob[114:117], legacyTCPMatchName)

				return blob
			}(),
		},
		{
			name: "tcp match truncates the port range",
			blob: func() []byte {
				blob := make([]byte, 192)
				binary.NativeEndian.PutUint16(blob[88:90], 152)
				binary.NativeEndian.PutUint16(blob[90:92], 192)

				// A tcp match head sized without the port ranges.
				binary.NativeEndian.PutUint16(blob[112:114], legacyMatchHeadLength+4)
				copy(blob[114:117], legacyTCPMatchName)

				return blob
			}(),
		},
		{
			name: "target truncation",
			blob: func() []byte {
				blob := bareEntry(legacyVerdictAccept)[:144]
				binary.NativeEndian.PutUint16(blob[90:92], 144)

				return blob
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := decodeLegacyEntries(test.blob)
			if err == nil {
				t.Fatalf("a malformed blob decoded: %+v", decoded)
			}
		})
	}
}
