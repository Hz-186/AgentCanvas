package compaction

import (
	"strings"

	"agentcanvas/internal/domain/conversation"
)

// RetainUserEntries keeps user-text entries within budget tokens; see
// RetainEntriesByRole.
func RetainUserEntries(req Request, entries []Entry) []Entry {
	return RetainEntriesByRole(req, entries, conversation.RoleUser)
}

// RetainEntriesByRole keeps, from the tail backwards, text entries of the
// given role whose content does not carry the summary prefix, within budget
// tokens. The first overflowing entry is truncated to the remaining budget
// and kept; earlier entries are dropped. Order is preserved.
func RetainEntriesByRole(req Request, entries []Entry, role string) []Entry {
	remaining := req.userBudget()
	kept := make([]Entry, 0)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Role != role || entry.ContentType != conversation.ContentTypeText {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(entry.Content), SummaryPrefix) {
			continue
		}
		tokens := req.tokenCount(entry.Content)
		if tokens <= remaining {
			kept = append(kept, entry)
			remaining -= tokens
			continue
		}
		if remaining > 0 {
			entry.Content = TruncateToTokens(req, entry.Content, remaining)
			if strings.TrimSpace(entry.Content) != "" {
				kept = append(kept, entry)
			}
		}
		break
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept
}

// TruncateToTokens binary-searches the longest rune prefix within limit tokens.
func TruncateToTokens(req Request, text string, limit int) string {
	runes := []rune(text)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if req.tokenCount(string(runes[:mid])) <= limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:lo])
}
