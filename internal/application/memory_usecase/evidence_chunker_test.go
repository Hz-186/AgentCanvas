package memory_usecase

import (
	"strings"
	"testing"

	"agentcanvas/internal/domain/conversation"
)

// These tests pin the durable evidence chunking contract (design Decision 6):
// whole-unit splits under the 120000-byte cap, part_index/part_count slicing
// for a single oversized tool output, and a two-unit overlap between adjacent
// chunks. Chunking operates only on renderer units, never raw message rows.

// sizedTextUnit builds a text unit whose rendered size (the chunker's own
// byte metric) is exactly `size` bytes, so packing boundaries are precise.
func sizedTextUnit(id int64, size int) EvidenceUnit {
	unit := EvidenceUnit{Kind: EvidenceUnitText, MessageID: id, Role: conversation.RoleUser}
	base := evidenceUnitBytes(unit)
	if size < base {
		size = base
	}
	unit.Content = strings.Repeat(string(rune('a'+int(id%26))), size-base)
	return unit
}

func chunkedUnitIDs(chunk EvidenceChunk) []int64 {
	ids := make([]int64, 0, len(chunk.Units))
	for _, unit := range chunk.Units {
		ids = append(ids, unit.Unit.MessageID)
	}
	return ids
}

// firstOccurrenceOrder reduces a chunk sequence to the first-seen message-id
// order, skipping overlap repetitions.
func firstOccurrenceOrder(chunks []EvidenceChunk) []int64 {
	seen := map[int64]bool{}
	order := []int64{}
	for _, chunk := range chunks {
		for _, unit := range chunk.Units {
			if seen[unit.Unit.MessageID] {
				continue
			}
			seen[unit.Unit.MessageID] = true
			order = append(order, unit.Unit.MessageID)
		}
	}
	return order
}

func sameChunkedUnit(a, b ChunkedEvidenceUnit) bool {
	return a.Unit == b.Unit && a.PartIndex == b.PartIndex && a.PartCount == b.PartCount
}

func TestEvidenceChunker(t *testing.T) {
	t.Run("shouldKeepSmallWindowSingleChunk", func(t *testing.T) {
		units := []EvidenceUnit{
			sizedTextUnit(1, 400),
			sizedTextUnit(2, 500),
			sizedTextUnit(3, 600),
		}

		chunks := NewEvidenceChunker().Chunk(units)

		if len(chunks) != 1 {
			t.Fatalf("chunks = %d, want one chunk for a window below the cap", len(chunks))
		}
		if got := chunkedUnitIDs(chunks[0]); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
			t.Fatalf("chunk unit order = %v, want [1 2 3] unchanged", got)
		}
		for _, unit := range chunks[0].Units {
			if unit.PartIndex != 0 || unit.PartCount != 1 {
				t.Fatalf("small-window unit %d carries part %d/%d, want unsliced", unit.Unit.MessageID, unit.PartIndex, unit.PartCount)
			}
		}
		// Single chunk: no overlap copies may duplicate units.
		if len(chunks[0].Units) != 3 {
			t.Fatalf("single chunk holds %d units, want exactly the 3 input units", len(chunks[0].Units))
		}
	})

	t.Run("shouldSplitOnlyAtUnitBoundaries", func(t *testing.T) {
		// Five units of exactly cap/3 bytes: three fill a chunk, the fourth
		// crosses the boundary and must move to the next chunk whole.
		size := durableMaxRolloutLen / 3
		units := []EvidenceUnit{
			sizedTextUnit(1, size),
			sizedTextUnit(2, size),
			sizedTextUnit(3, size),
			sizedTextUnit(4, size),
			sizedTextUnit(5, size),
		}

		chunks := NewEvidenceChunker().Chunk(units)

		if len(chunks) < 2 {
			t.Fatalf("chunks = %d, want at least two for %d cap/3-sized units", len(chunks), len(units))
		}
		if got := chunkedUnitIDs(chunks[0]); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
			t.Fatalf("first chunk holds %v, want units 1,2,3 exactly at the cap", got)
		}
		// The boundary-crossing unit 4 lands whole in the next chunk.
		var carried *ChunkedEvidenceUnit
		for i := range chunks[1].Units {
			if chunks[1].Units[i].Unit.MessageID == 4 {
				carried = &chunks[1].Units[i]
				break
			}
		}
		if carried == nil {
			t.Fatalf("unit 4 missing from the second chunk: %v", chunkedUnitIDs(chunks[1]))
		}
		if carried.Unit.Content != units[3].Content {
			t.Fatalf("unit 4 was cut at the boundary: carried %d bytes, want all %d", len(carried.Unit.Content), len(units[3].Content))
		}
		// No unit anywhere was sliced (nothing here exceeds the cap alone).
		for _, chunk := range chunks {
			for _, unit := range chunk.Units {
				if unit.PartCount != 1 {
					t.Fatalf("unit %d sliced into %d parts although it fits the cap", unit.Unit.MessageID, unit.PartCount)
				}
			}
		}
		if got := firstOccurrenceOrder(chunks); len(got) != 5 || got[0] != 1 || got[1] != 2 || got[2] != 3 || got[3] != 4 || got[4] != 5 {
			t.Fatalf("first-occurrence order = %v, want [1 2 3 4 5]", got)
		}
	})

	t.Run("shouldSliceOversizedOutputWithPartIndex", func(t *testing.T) {
		oversized := strings.Repeat("x", 300000)
		units := []EvidenceUnit{
			sizedTextUnit(1, 500),
			{
				Kind: EvidenceUnitExchange, MessageID: 5, Role: conversation.RoleAssistant,
				ToolCallID: "call_big", ToolName: "loader", Arguments: `{"path":"/data"}`,
				Output: oversized, ErrorState: EvidenceErrorStateSuccess,
			},
			sizedTextUnit(9, 500),
		}

		chunks := NewEvidenceChunker().Chunk(units)

		// Collect distinct parts by part_index: the two-unit overlap repeats
		// fragments across adjacent chunks, which must not count as re-slicing.
		var parts []ChunkedEvidenceUnit
		seenParts := map[int]bool{}
		smallIDs := map[int64]bool{}
		for _, chunk := range chunks {
			for _, unit := range chunk.Units {
				if unit.Unit.ToolCallID == "call_big" {
					if !seenParts[unit.PartIndex] {
						seenParts[unit.PartIndex] = true
						parts = append(parts, unit)
					}
					continue
				}
				smallIDs[unit.Unit.MessageID] = true
			}
		}
		if len(parts) < 2 {
			t.Fatalf("oversized output produced %d part(s), want consecutive fragments", len(parts))
		}
		for i, part := range parts {
			if part.PartIndex != i {
				t.Fatalf("part %d carries part_index %d, want ascending from 0", i, part.PartIndex)
			}
			if part.PartCount != len(parts) {
				t.Fatalf("part %d carries part_count %d, want consistent %d", i, part.PartCount, len(parts))
			}
			if got := evidenceChunkedBytes(part); got > durableMaxRolloutLen {
				t.Fatalf("part %d renders %d bytes, above the %d-byte cap", i, got, durableMaxRolloutLen)
			}
			if part.Unit.Output == "" {
				t.Fatalf("part %d carries an empty output fragment", i)
			}
		}
		var rebuilt strings.Builder
		for _, part := range parts {
			rebuilt.WriteString(part.Unit.Output)
		}
		if rebuilt.String() != oversized {
			t.Fatalf("concatenated parts differ from the original output: got %d bytes, want %d (middle dropped?)", rebuilt.Len(), len(oversized))
		}
		if !smallIDs[1] || !smallIDs[9] {
			t.Fatalf("surrounding units lost during slicing: %v", smallIDs)
		}
	})

	t.Run("shouldOverlapTwoUnitsBetweenAdjacentChunks", func(t *testing.T) {
		// Ten units of cap/4 bytes: four fill a chunk, so the walk produces at
		// least three chunks with two-unit overlaps.
		size := durableMaxRolloutLen / 4
		units := make([]EvidenceUnit, 0, 10)
		for id := int64(1); id <= 10; id++ {
			units = append(units, sizedTextUnit(id, size))
		}

		chunks := NewEvidenceChunker().Chunk(units)

		if len(chunks) < 3 {
			t.Fatalf("chunks = %d, want at least three to verify adjacent overlaps", len(chunks))
		}
		for i := 0; i+1 < len(chunks); i++ {
			current, next := chunks[i], chunks[i+1]
			if len(current.Units) < 2 || len(next.Units) < 2 {
				t.Fatalf("chunks %d/%d hold %d/%d units, too few to share a two-unit overlap", i, i+1, len(current.Units), len(next.Units))
			}
			tail := current.Units[len(current.Units)-2:]
			if !sameChunkedUnit(next.Units[0], tail[0]) || !sameChunkedUnit(next.Units[1], tail[1]) {
				t.Fatalf("chunks %d and %d share %v vs %v, want exactly the last two units in order", i, i+1, chunkedUnitIDs(next)[:2], chunkedUnitIDs(current)[len(current.Units)-2:])
			}
			// Exactly two: the unit after the overlap must be new, not a third
			// shared one.
			if len(next.Units) > 2 && len(current.Units) > 2 {
				thirdFromTail := current.Units[len(current.Units)-3]
				if sameChunkedUnit(next.Units[2], thirdFromTail) {
					t.Fatalf("chunks %d and %d share more than two units", i, i+1)
				}
			}
		}
		if got := firstOccurrenceOrder(chunks); len(got) != 10 {
			t.Fatalf("first-occurrence order = %v, want all ten units exactly once", got)
		}
	})

	t.Run("shouldNotExplodeWhenUnitShellExceedsCap", func(t *testing.T) {
		// Pathological unit: the shell (everything except the sliceable
		// output) alone exceeds the 120000-byte cap. Slicing must stay
		// O(payload/cap) — never one fragment per payload byte.
		oversized := strings.Repeat("x", 300000)
		unit := EvidenceUnit{
			Kind: EvidenceUnitExchange, MessageID: 5, Role: conversation.RoleAssistant,
			ToolCallID: "call_huge", ToolName: "loader",
			Arguments:  strings.Repeat("g", 120001),
			Output:     oversized,
			ErrorState: EvidenceErrorStateSuccess,
		}
		shell := unit
		shell.Output = ""
		if shellBytes := evidenceUnitBytes(shell); shellBytes <= durableMaxRolloutLen {
			t.Fatalf("test setup: shell renders %d bytes, must exceed the %d-byte cap", shellBytes, durableMaxRolloutLen)
		}

		chunker := NewEvidenceChunker()
		parts := chunker.expandOversized([]EvidenceUnit{unit})

		if len(parts) > 5 {
			t.Fatalf("shell-exceeds-cap unit exploded into %d fragments, want O(payload/cap) <= 5", len(parts))
		}
		if len(parts) < 2 {
			t.Fatalf("unit with %d-byte payload produced %d fragment(s), want sliced fragments, not a silent drop", len(oversized), len(parts))
		}
		for i, part := range parts {
			if part.PartIndex != i {
				t.Fatalf("part %d carries part_index %d, want contiguous from 0", i, part.PartIndex)
			}
			if part.PartCount != len(parts) {
				t.Fatalf("part %d carries part_count %d, want consistent %d", i, part.PartCount, len(parts))
			}
			if part.Unit.MessageID != 5 || part.Unit.ToolCallID != "call_huge" {
				t.Fatalf("part %d lost the unit identity: %+v", i, part.Unit)
			}
			if part.Unit.Output == "" {
				t.Fatalf("part %d carries an empty output fragment", i)
			}
		}
		var rebuilt strings.Builder
		for _, part := range parts {
			rebuilt.WriteString(part.Unit.Output)
		}
		if rebuilt.String() != oversized {
			t.Fatalf("concatenated parts differ from the original output: got %d bytes, want %d (middle dropped?)", rebuilt.Len(), len(oversized))
		}

		// Full chunk path: every fragment reaches a chunk — the unit is not
		// silently dropped by the packer.
		chunks := chunker.Chunk([]EvidenceUnit{unit})
		seen := map[int]bool{}
		for _, chunk := range chunks {
			for _, placed := range chunk.Units {
				if placed.Unit.ToolCallID != "call_huge" {
					t.Fatalf("unexpected unit in chunks: %+v", placed.Unit)
				}
				seen[placed.PartIndex] = true
			}
		}
		if len(seen) != len(parts) {
			t.Fatalf("chunks carry %d distinct part indices, want all %d fragments placed", len(seen), len(parts))
		}
	})
}
