package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/tokencounter"
)

// Single source of truth for compaction constants (values are fixed by the
// unify-context-compaction change; do not tune them per call site).
const (
	UserMessageBudgetTokens = 20_000
	ThresholdRatio          = 0.90
	SummarizeTimeout        = 20 * time.Second
	SummaryPrefix           = "SUMMARY:\n"
	FallbackSummary         = "(no summary available)"
	PerEntryLimitTokens     = 8_000
)

// ErrCompactionFailed wraps summarization failures that retrying cannot fix
// (single-entry overflow, provider errors after retries).
var ErrCompactionFailed = errors.New("compaction failed")

// Request configures one Compact call. Zero UserBudgetTokens/PerEntryLimitTokens
// fall back to the package constants.
type Request struct {
	SystemPrompt   string
	CompactPrompt  string
	Provider       llm.ChatProviderConfig
	Model          string
	ParentSummary  string // previous rolling summary; empty starts fresh
	UserBudget     int
	PerEntryLimit  int
	Timeout        time.Duration
	ProviderType   string // token counter hints; Provider.ProviderType preferred
	TokenModelHint string
}

// Result carries the compacted window plus usage accounting.
type Result struct {
	Summary    string
	Retained   []Entry // user-text entries kept verbatim (tail-first within budget)
	Usage      llm.Usage
	ModelCalls int
}

func (r Request) userBudget() int {
	if r.UserBudget > 0 {
		return r.UserBudget
	}
	return UserMessageBudgetTokens
}

func (r Request) perEntryLimit() int {
	if r.PerEntryLimit > 0 {
		return r.PerEntryLimit
	}
	return PerEntryLimitTokens
}

func (r Request) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return SummarizeTimeout
}

func (r Request) tokenCount(text string) int {
	providerType := r.Provider.ProviderType
	if providerType == "" {
		providerType = r.ProviderType
	}
	return tokencounter.Count(providerType, r.TokenModelHintOrModel(), text).Tokens
}

func (r Request) TokenModelHintOrModel() string {
	if r.TokenModelHint != "" {
		return r.TokenModelHint
	}
	return r.Model
}

// Compact summarizes entries into a rolling summary and selects the retained
// user messages. It is the single algorithm invoked from every trigger point.
func Compact(ctx context.Context, client llm.ChatClient, req Request, entries []Entry) (Result, error) {
	summary, usage, calls, err := summarize(ctx, client, req, entries)
	if err != nil {
		return Result{Usage: usage, ModelCalls: calls}, err
	}
	return Result{
		Summary:    summary,
		Retained:   RetainUserEntries(req, entries),
		Usage:      usage,
		ModelCalls: calls,
	}, nil
}

// renderEntry formats one entry for the summarizer input.
func renderEntry(entry Entry) string {
	switch entry.ContentType {
	case "function_call":
		args := strings.TrimSpace(string(entry.Arguments))
		if args == "" {
			args = "{}"
		}
		return fmt.Sprintf("[tool call: %s] %s", entry.ToolName, args)
	case "function_call_output":
		return fmt.Sprintf("[tool result: %s] %s", entry.ToolName, entry.Content)
	default:
		return entry.Content
	}
}

// summarizableEntries filters entries the summarizer sees: system_echo and
// reasoning never contribute.
func summarizableEntries(entries []Entry) []Entry {
	kept := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		switch entry.ContentType {
		case "system_echo", "reasoning":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func summarize(ctx context.Context, client llm.ChatClient, req Request, entries []Entry) (string, llm.Usage, int, error) {
	entries = summarizableEntries(entries)
	if len(entries) == 0 {
		return FallbackSummary, llm.Usage{}, 0, nil
	}
	prompt := "Summarize the conversation for continuation. Include: progress and decisions; context constraints and preferences; remaining work; key data and references. Return summary text only."
	if custom := strings.TrimSpace(req.CompactPrompt); custom != "" {
		prompt += "\nAdditional guidance:\n" + custom
	}
	system := strings.TrimSpace(req.SystemPrompt)
	if system == "" {
		system = "You are a context compaction engine."
	}
	zero := 0.0
	usage := llm.Usage{}
	calls := 0
	trimmed := append([]Entry(nil), entries...)
	retries := 0
	for {
		messages := make([]llm.ChatMessage, 0, len(trimmed)+3)
		messages = append(messages, llm.ChatMessage{Role: "system", Content: system})
		if parent := strings.TrimSpace(req.ParentSummary); parent != "" {
			messages = append(messages, llm.ChatMessage{Role: "user", Content: SummaryPrefix + parent})
		}
		for _, entry := range trimmed {
			content := renderEntry(entry)
			if limit := req.perEntryLimit(); limit > 0 && req.tokenCount(content) > limit {
				content = TruncateToTokens(req, content, limit)
			}
			messages = append(messages, llm.ChatMessage{Role: entry.Role, Content: content})
		}
		messages = append(messages, llm.ChatMessage{Role: "user", Content: prompt})

		callCtx, cancel := context.WithTimeout(ctx, req.timeout())
		response, err := client.Chat(callCtx, req.Provider, llm.ChatRequest{Model: req.Model, Messages: messages, Temperature: &zero})
		cancel()
		calls++
		if response != nil {
			usage = addUsage(usage, response.Usage)
		}
		if err == nil {
			if strings.TrimSpace(response.Content) == "" {
				return FallbackSummary, usage, calls, nil
			}
			return strings.TrimSpace(response.Content), usage, calls, nil
		}
		if errors.Is(err, llm.ErrContextWindowExceeded) {
			if len(trimmed) <= 1 {
				return "", usage, calls, fmt.Errorf("%w: summarizer context window exceeded with a single entry: %w", ErrCompactionFailed, err)
			}
			trimmed = trimmed[1:]
			retries = 0
			continue
		}
		if retries < 2 {
			select {
			case <-ctx.Done():
				return "", usage, calls, ctx.Err()
			case <-time.After(time.Duration(1<<retries) * 10 * time.Millisecond):
			}
			retries++
			continue
		}
		return "", usage, calls, fmt.Errorf("%w: %w", ErrCompactionFailed, err)
	}
}

func addUsage(total, delta llm.Usage) llm.Usage {
	total.PromptTokens += delta.PromptTokens
	total.CompletionTokens += delta.CompletionTokens
	total.TotalTokens += delta.TotalTokens
	total.CachedInputTokens += delta.CachedInputTokens
	total.ReasoningTokens += delta.ReasoningTokens
	return total
}
