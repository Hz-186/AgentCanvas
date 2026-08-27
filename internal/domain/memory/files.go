package memory

import (
	"context"
	"strings"
)

// FileSearchResult is the minimal detail payload exposed to an Agent. The
// filesystem is the source of truth; callers do not need the legacy SQL
// memory taxonomy or retention fields.
type FileSearchResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CodexReader provides the single progressive-read path for durable memory.
// Summary is used once during run assembly; Search is used only on demand.
type CodexReader interface {
	ReadSummary(ctx context.Context, ownerID int64, tokenBudget int) (string, error)
	Search(ctx context.Context, ownerID int64, query string, limit int) ([]FileSearchResult, error)
}

// AdHocWriter is intentionally separate from ordinary memory writes. A
// caller must pass an explicit user-intent marker; implementations append a
// new note and never edit MEMORY.md directly.
type AdHocWriter interface {
	AppendAdHocNote(ctx context.Context, ownerID, conversationID, runID int64, request, answer string) (string, error)
}

// HasExplicitMemoryIntent recognizes only direct user requests. Ordinary
// statements are deliberately excluded so a ReAct answer cannot create a
// durable note accidentally.
func HasExplicitMemoryIntent(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false
	}
	// A quoted example such as “解释‘请记住’这句话” is not a memory command.
	// Remove quoted spans before checking the imperative markers.
	value = stripQuotedMemoryText(value)
	for _, marker := range []string{
		"请记住", "记住这个", "记住：", "记住:", "帮我记住", "保存为记忆", "记下来", "忘记这个", "忘记该", "删除记忆", "忘掉这个", "更新记忆", "修改记忆",
		"remember this", "please remember", "save this to memory", "forget this", "delete this memory", "update memory",
	} {
		if index := strings.Index(value, marker); index >= 0 && memoryIntentPrefix(value[:index]) {
			return true
		}
	}
	return false
}

func memoryIntentPrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return true
	}
	// Permit short politeness/context prefixes while avoiding arbitrary prose
	// that merely mentions the words “remember” or “update memory”.
	for _, allowed := range []string{"请", "请帮我", "帮我", "以后", "从现在起", "please", "can you", "could you", "ช่วย"} {
		if strings.TrimSpace(strings.TrimSuffix(prefix, allowed)) == "" {
			return true
		}
	}
	return false
}

func stripQuotedMemoryText(value string) string {
	var builder strings.Builder
	quote := rune(0)
	for _, r := range value {
		if quote != 0 {
			if r == quote || (quote == '\u201c' && r == '\u201d') || (quote == '\u300c' && r == '\u300d') {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '\u2018', '\u201c', '\u300c', '`':
			quote = r
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
