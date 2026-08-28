package memory_usecase

import (
	"fmt"
	"strings"
)

// durableChunkOverlapUnits is the number of evidence units adjacent chunks
// share (design Decision 6): the overlap lets the extraction model see
// boundary context twice without a full re-send.
const durableChunkOverlapUnits = 2

// ChunkedEvidenceUnit is one evidence unit placed into a chunk. Units whose
// rendered size exceeds the chunk cap are sliced into consecutive fragments
// carrying PartIndex/PartCount (part_index ascending from 0); every unsliced
// unit carries PartIndex 0 / PartCount 1.
type ChunkedEvidenceUnit struct {
	Unit      EvidenceUnit
	PartIndex int
	PartCount int
}

// EvidenceChunk is one extraction payload: an ordered sequence of chunked
// units whose new-unit bytes stay under the cap. Chunks after the first begin
// with copies of the previous chunk's last units (the overlap).
type EvidenceChunk struct {
	Index int
	Units []ChunkedEvidenceUnit
}

// EvidenceRenderer is the producer; EvidenceChunker is the pure splitter. It
// operates only on rendered units — raw message rows are never chunked.
type EvidenceChunker struct {
	maxBytes     int
	overlapUnits int
}

func NewEvidenceChunker() *EvidenceChunker {
	return &EvidenceChunker{maxBytes: durableMaxRolloutLen, overlapUnits: durableChunkOverlapUnits}
}

// evidenceUnitText renders one unit for the extraction prompt. The same text
// is the chunker's byte metric, so packing and prompts can never disagree.
func evidenceUnitText(unit EvidenceUnit) string {
	var builder strings.Builder
	switch unit.Kind {
	case EvidenceUnitExchange:
		fmt.Fprintf(&builder, "[msg %d] tool_call %s %s state=%s code=%s\narguments: %s\noutput: %s",
			unit.MessageID, unit.ToolCallID, unit.ToolName, unit.ErrorState, unit.ErrorCode, unit.Arguments, unit.Output)
	case EvidenceUnitOrphanOutput:
		fmt.Fprintf(&builder, "[msg %d] orphan tool_output %s %s state=%s code=%s\noutput: %s",
			unit.MessageID, unit.ToolCallID, unit.ToolName, unit.ErrorState, unit.ErrorCode, unit.Output)
	default:
		fmt.Fprintf(&builder, "[msg %d] %s: %s", unit.MessageID, unit.Role, unit.Content)
	}
	return builder.String()
}

// evidenceUnitBytes is the rendered size of an unsliced unit including the
// per-unit separator the prompt builder emits.
func evidenceUnitBytes(unit EvidenceUnit) int {
	return len(evidenceUnitText(unit)) + 1
}

// partMarker annotates sliced fragments for the model; part_index stays
// 0-based to match the ChunkedEvidenceUnit field.
func partMarker(partIndex, partCount int) string {
	return fmt.Sprintf(" [part %d/%d]", partIndex, partCount)
}

func evidenceChunkedText(cu ChunkedEvidenceUnit) string {
	text := evidenceUnitText(cu.Unit)
	if cu.PartCount > 1 {
		text += partMarker(cu.PartIndex, cu.PartCount)
	}
	return text
}

// evidenceChunkedBytes is the chunker's packing metric for one placed unit.
func evidenceChunkedBytes(cu ChunkedEvidenceUnit) int {
	return len(evidenceChunkedText(cu)) + 1
}

// Chunk splits the unit sequence into extraction chunks (design Decision 6):
// whole-unit greedy packing under the cap, oversized units pre-sliced into
// consecutive part_index/part_count fragments, and a two-unit overlap copied
// to the head of every chunk after the first. Every fragment fits the cap
// alone EXCEPT when the unit's shell alone already reaches or exceeds the
// cap: that pathological unit is still sliced into cap-sized payload slices
// (shell overflow accepted) so the fragment count stays O(payload/cap). The
// overlap rides outside the new-unit budget; every chunk places at least one
// new unit, so each chunk always advances the frontier.
func (c *EvidenceChunker) Chunk(units []EvidenceUnit) []EvidenceChunk {
	placed := c.expandOversized(units)
	chunks := []EvidenceChunk{}
	next := 0
	for next < len(placed) {
		chunk := EvidenceChunk{Index: len(chunks)}
		if len(chunks) > 0 {
			previous := chunks[len(chunks)-1].Units
			from := len(previous) - c.overlapUnits
			if from < 0 {
				from = 0
			}
			chunk.Units = append(chunk.Units, previous[from:]...)
		}
		used := 0
		for next < len(placed) {
			size := evidenceChunkedBytes(placed[next])
			if used > 0 && used+size > c.maxBytes {
				break
			}
			chunk.Units = append(chunk.Units, placed[next])
			used += size
			next++
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

// expandOversized slices every unit that alone exceeds the cap into
// consecutive fragments; all other units pass through unsliced.
func (c *EvidenceChunker) expandOversized(units []EvidenceUnit) []ChunkedEvidenceUnit {
	placed := make([]ChunkedEvidenceUnit, 0, len(units))
	for _, unit := range units {
		if evidenceChunkedBytes(ChunkedEvidenceUnit{Unit: unit, PartCount: 1}) <= c.maxBytes {
			placed = append(placed, ChunkedEvidenceUnit{Unit: unit, PartCount: 1})
			continue
		}
		placed = append(placed, c.sliceOversized(unit)...)
	}
	return placed
}

// sliceOversized cuts the unit's dominant payload (tool Output for exchange
// and orphan units, Content for text units) into consecutive fragments. Each
// fragment renders <= the cap unless the unit's shell (everything except the
// sliceable payload) alone reaches or exceeds the cap: then the payload is
// still cut into cap-sized slices — accepting the shell overflow — so the
// fragment count stays O(payload/cap) instead of one fragment per byte.
// Fragment payloads concatenate byte-for-byte back to the original; nothing
// from the middle is dropped.
func (c *EvidenceChunker) sliceOversized(unit EvidenceUnit) []ChunkedEvidenceUnit {
	payload := unit.Output
	if unit.Kind == EvidenceUnitText {
		payload = unit.Content
	}
	if payload == "" {
		// No sliceable payload (a degenerate oversized shell): keep the unit
		// whole rather than inventing empty fragments.
		return []ChunkedEvidenceUnit{{Unit: unit, PartCount: 1}}
	}
	shell := unit
	if unit.Kind == EvidenceUnitText {
		shell.Content = ""
	} else {
		shell.Output = ""
	}
	// Reserve the widest possible marker up front so the real marker (whose
	// digit count depends on the final part count) can never overflow the cap.
	maxParts := len(payload) + 1
	markerMax := len(partMarker(maxParts, maxParts))
	budget := c.maxBytes - evidenceUnitBytes(shell) - markerMax
	if budget < 1 {
		// The shell alone fills (or overflows) the cap. Slice the payload by
		// the full cap instead of clamping to a byte — accepting the shell
		// overflow for this pathological unit — so the fragment count stays
		// O(payload/cap) instead of exploding to one fragment per byte.
		budget = c.maxBytes
	}
	partCount := (len(payload) + budget - 1) / budget
	parts := make([]ChunkedEvidenceUnit, 0, partCount)
	for index := 0; index < partCount; index++ {
		start := index * budget
		end := start + budget
		if end > len(payload) {
			end = len(payload)
		}
		fragment := unit
		if unit.Kind == EvidenceUnitText {
			fragment.Content = payload[start:end]
		} else {
			fragment.Output = payload[start:end]
		}
		parts = append(parts, ChunkedEvidenceUnit{Unit: fragment, PartIndex: index, PartCount: partCount})
	}
	return parts
}
