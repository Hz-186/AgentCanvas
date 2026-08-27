package memory

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Citation line grammar (design §5; Codex-compatible, line-oriented):
//
//	<oai-mem-citation memory_id="<positive int>" [attrs...]>content</oai-mem-citation>
//
// One citation per line, optional extra provenance attributes and inner
// content, surrounding whitespace tolerated, closing tag must end the line.
// Any line containing the protocol tag name that does not fully match this
// shape is a malformed citation line: it is removed from visible text and
// dropped from accounting with an observable warning. The block is stripped
// line by line wherever those lines appear.
var citationLinePattern = regexp.MustCompile(`^\s*<oai-mem-citation\s+[^>]*\bmemory_id\s*=\s*"([0-9]+)"[^>]*>.*</oai-mem-citation>\s*$`)

// citationTagMark identifies citation block content even when the line is
// malformed, so no citation markup ever reaches the user-visible answer.
const citationTagMark = "oai-mem-citation"

// CitationUsageRepository is the narrow repository surface citation usage
// accounting depends on. The persistent Repository already satisfies it; the
// surface stays minimal so finalization can be tested with a fake and does
// not drag unrelated persistence behavior in.
type CitationUsageRepository interface {
	FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]Memory, error)
	MarkUsed(ctx context.Context, ownerID int64, ids []int64) error
}

// CitationOutcome is the result of stripping and accounting the citation
// block in a final answer.
type CitationOutcome struct {
	// VisibleText is the final answer with every citation line removed
	// (trailing whitespace trimmed when any line was removed).
	VisibleText string
	// UpdatedIDs lists the validated, deduplicated memory IDs that received
	// one usage update each (in first-citation order).
	UpdatedIDs []int64
	// Warnings holds one entry per dropped malformed line, per missing or
	// foreign-owner ID, and per repository failure. It never fails the run.
	Warnings []string
}

// AccountCitations strips the <oai-mem-citation> block from finalText and
// records owner-validated usage for the cited memories. Validation precedes
// update: an ID is counted only when it exists for the current owner. Cited
// IDs are deduplicated per run, so repeated citations produce exactly one
// usage_count increment and one last_used_at update. Returned-but-uncited
// memories are never counted. Missing, foreign-owner and malformed citations
// are dropped individually with warnings; repository failures only produce
// warnings. RecallLog stays the returned-candidate audit and is untouched.
func AccountCitations(ctx context.Context, ownerID int64, finalText string, repo CitationUsageRepository) CitationOutcome {
	visible, citedIDs, warnings := parseCitationBlock(finalText)
	outcome := CitationOutcome{VisibleText: visible, Warnings: warnings}
	if repo == nil || len(citedIDs) == 0 {
		return outcome
	}
	found, err := repo.FindByIDs(ctx, ownerID, citedIDs)
	if err != nil {
		outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("citation usage accounting skipped: %v", err))
		return outcome
	}
	owned := make(map[int64]bool, len(found))
	for _, item := range found {
		owned[item.ID] = true
	}
	for _, id := range citedIDs {
		if owned[id] {
			outcome.UpdatedIDs = append(outcome.UpdatedIDs, id)
			continue
		}
		outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("citation memory %d does not exist or belongs to another owner; usage not recorded", id))
	}
	if len(outcome.UpdatedIDs) > 0 {
		if err := repo.MarkUsed(ctx, ownerID, outcome.UpdatedIDs); err != nil {
			outcome.UpdatedIDs = nil
			outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("record citation usage: %v", err))
		}
	}
	return outcome
}

// StripCitations removes every citation block line from text, leaving all
// other content intact (trailing whitespace trimmed when any line was
// removed). It performs no usage accounting and touches no repository, so
// callers that persist user-visible content can sanitize model output at the
// persistence boundary while the raw text keeps flowing to AccountCitations.
func StripCitations(text string) string {
	visible, _, _ := parseCitationBlock(text)
	return visible
}

// parseCitationBlock strips citation lines from finalText and extracts the
// deduplicated, positively parsed memory IDs in first-citation order.
func parseCitationBlock(finalText string) (visible string, citedIDs []int64, warnings []string) {
	lines := strings.Split(finalText, "\n")
	removed := false
	kept := make([]string, 0, len(lines))
	seen := make(map[int64]bool)
	for _, line := range lines {
		if !strings.Contains(line, citationTagMark) {
			kept = append(kept, line)
			continue
		}
		removed = true
		match := citationLinePattern.FindStringSubmatch(line)
		if match == nil {
			warnings = append(warnings, fmt.Sprintf("dropped malformed citation line: %q", strings.TrimSpace(line)))
			continue
		}
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || id <= 0 {
			warnings = append(warnings, fmt.Sprintf("dropped malformed citation line: %q", strings.TrimSpace(line)))
			continue
		}
		if !seen[id] {
			seen[id] = true
			citedIDs = append(citedIDs, id)
		}
	}
	visible = strings.Join(kept, "\n")
	if removed {
		visible = strings.TrimRight(visible, " \t\r\n")
	}
	return visible, citedIDs, warnings
}
