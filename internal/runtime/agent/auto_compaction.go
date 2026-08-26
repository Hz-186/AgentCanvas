package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/tokencounter"

	"gorm.io/gorm"
)

const (
	defaultAutoCompactRatio         = .90
	defaultCompactUserMessageTokens = 20_000
	defaultCompactTimeout           = 20 * time.Second
	compactionSummaryPrefix         = conversation.CompactionSummaryPrefix
)

func autoCompactLimit(req RunRequest) int {
	window := req.ContextWindowTokens
	if window <= 0 {
		window = req.MaxInputTokens
	}
	if window <= 0 {
		window = defaultMaxInputTokens
	}
	limit := int(float64(window) * defaultAutoCompactRatio)
	if limit <= 0 {
		limit = window
	}
	if req.ModelAutoCompactTokenLimit > 0 && req.ModelAutoCompactTokenLimit < limit {
		return req.ModelAutoCompactTokenLimit
	}
	return limit
}

func autoCompactScope(req RunRequest) string {
	if strings.TrimSpace(req.ModelAutoCompactTokenLimitScope) == "body_after_prefix" {
		return "body_after_prefix"
	}
	return "total"
}

type runtimeTokenStatusResult struct {
	Measured          int
	Limit             int
	HardLimit         int
	TokenLimitReached bool
}

func runtimeTokenStatus(req RunRequest, baseMessages, transcript []llm.ChatMessage, tools []llm.ToolDefinition) runtimeTokenStatusResult {
	history := append([]llm.ChatMessage(nil), transcript...)
	if req.Task != "" && hasUserContent(baseMessages, req.Task) && !hasUserContent(history, req.Task) {
		history = append([]llm.ChatMessage{{Role: conversation.RoleUser, Content: req.Task}}, history...)
	}
	body := modelMessagesTokens(req, history)
	total := modelMessagesTokens(req, assembleRoundMessages(baseMessages, nil, transcript)) + modelToolSchemaTokens(req, tools)
	measured := total
	if autoCompactScope(req) == "body_after_prefix" {
		measured = body
	}
	limit := autoCompactLimit(req)
	hard := hardPromptTokenLimit(req)
	return runtimeTokenStatusResult{Measured: measured, Limit: limit, HardLimit: hard, TokenLimitReached: measured >= limit || total >= hard}
}

// compactRuntimeTranscript follows Codex's mid-turn compaction shape: the
// complete history is sent back to the model and the result contains only user
// messages plus a final user-role summary.
func (r *Runner) compactRuntimeTranscript(ctx context.Context, req RunRequest, transcript []llm.ChatMessage) ([]llm.ChatMessage, llm.Usage, *CompactionTrace) {
	history := runtimeCompactionHistory(req, transcript)
	beforeTokens := modelMessagesTokens(req, history)
	trace := &CompactionTrace{Trigger: "runtime", Scope: autoCompactScope(req), Status: "completed"}
	if req.TokenBudgetCompaction {
		var retained []llm.ChatMessage
		if req.RetainClientDeveloperMessages {
			retained = retainMessagesByRole(req, history, conversation.RoleDeveloper, defaultCompactUserMessageTokens)
		}
		trace.AfterTokens = modelMessagesTokens(req, retained)
		trace.SavedTokens = maxInt(0, beforeTokens-trace.AfterTokens)
		return retained, llm.Usage{}, trace
	}
	summary, usage, err := r.summarizeContext(ctx, req, history)
	trace.ModelCalled = true
	trace.Summary = summary
	if err != nil {
		trace.Status = "failed"
		trace.Error = err.Error()
		return transcript, usage, trace
	}
	if strings.TrimSpace(summary) == "" {
		summary = "(no summary available)"
		trace.Summary = summary
	}
	kept := retainUserMessages(req, history, defaultCompactUserMessageTokens)
	kept = append(kept, llm.ChatMessage{Role: conversation.RoleUser, Content: compactionSummaryPrefix + summary})
	trace.AfterTokens = modelMessagesTokens(req, kept)
	trace.SavedTokens = maxInt(0, beforeTokens-trace.AfterTokens)
	return kept, usage, trace
}

func runtimeCompactionHistory(req RunRequest, transcript []llm.ChatMessage) []llm.ChatMessage {
	history := append([]llm.ChatMessage(nil), transcript...)
	if req.Task != "" && !hasUserContent(history, req.Task) {
		history = append([]llm.ChatMessage{{Role: conversation.RoleUser, Content: req.Task}}, history...)
	}
	return history
}

func (r *Runner) summarizeContext(ctx context.Context, req RunRequest, history []llm.ChatMessage) (string, llm.Usage, error) {
	if len(history) == 0 {
		return "(no summary available)", llm.Usage{}, nil
	}
	provider, model := req.CompactionProvider, strings.TrimSpace(req.CompactionModel)
	if strings.TrimSpace(provider.ProviderType) == "" || model == "" {
		provider, model = req.Provider, req.Model
	}
	custom := strings.TrimSpace(req.CompactPrompt)
	prompt := "Summarize the conversation for continuation. Include: progress and decisions; context constraints and preferences; remaining work; key data and references. Return summary text only."
	if custom != "" {
		prompt += "\nAdditional guidance:\n" + custom
	}
	zero := 0.0
	usage := llm.Usage{}
	trimmed := append([]llm.ChatMessage(nil), history...)
	retries := 0
	for {
		messages := make([]llm.ChatMessage, 0, len(trimmed)+2)
		system := strings.TrimSpace(req.SystemPrompt)
		if system == "" {
			system = "You are a context compaction engine."
		}
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleSystem, Content: system})
		messages = append(messages, trimmed...)
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleUser, Content: prompt})
		callCtx, cancel := context.WithTimeout(ctx, defaultCompactTimeout)
		response, err := r.LLM.ChatWithTools(callCtx, provider, llm.ToolChatRequest{Model: model, Messages: messages, Temperature: &zero})
		cancel()
		if response != nil {
			usage = addUsage(usage, response.Usage)
		}
		if err == nil {
			if response == nil || strings.TrimSpace(response.Message.Content) == "" {
				return "(no summary available)", usage, nil
			}
			return strings.TrimSpace(response.Message.Content), usage, nil
		}
		if errors.Is(err, llm.ErrContextWindowExceeded) {
			if len(trimmed) <= 1 {
				return "", usage, err
			}
			trimmed, retries = trimmed[1:], 0
			continue
		}
		if retries < 2 {
			select {
			case <-ctx.Done():
				return "", usage, ctx.Err()
			case <-time.After(time.Duration(1<<retries) * 10 * time.Millisecond):
			}
			retries++
			continue
		}
		return "", usage, err
	}
}

func retainUserMessages(req RunRequest, history []llm.ChatMessage, budget int) []llm.ChatMessage {
	return retainMessagesByRole(req, history, conversation.RoleUser, budget)
}

func retainMessagesByRole(req RunRequest, history []llm.ChatMessage, role string, budget int) []llm.ChatMessage {
	kept := make([]llm.ChatMessage, 0, len(history))
	remaining := budget
	for i := len(history) - 1; i >= 0; i-- {
		message := history[i]
		if message.Role != role || strings.HasPrefix(strings.TrimSpace(message.Content), compactionSummaryPrefix) {
			continue
		}
		tokens := modelTextTokens(req, message.Content)
		if tokens <= remaining {
			kept = append(kept, message)
			remaining -= tokens
			continue
		}
		if remaining > 0 {
			message.Content = truncateMessageTokens(req, message.Content, remaining)
			if strings.TrimSpace(message.Content) != "" {
				kept = append(kept, message)
			}
		}
		break
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept
}

func truncateMessageTokens(req RunRequest, text string, limit int) string {
	runes := []rune(text)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if modelTextTokens(req, string(runes[:mid])) <= limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:lo])
}

func hasUserContent(messages []llm.ChatMessage, content string) bool {
	for _, message := range messages {
		if message.Role == conversation.RoleUser && message.Content == content {
			return true
		}
	}
	return false
}

func baseMessagesAfterTokenBudget(req RunRequest, messages []llm.ChatMessage, retainDeveloper bool) []llm.ChatMessage {
	kept := make([]llm.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role == conversation.RoleSystem {
			kept = append(kept, message)
		}
	}
	if retainDeveloper {
		kept = append(kept, retainMessagesByRole(req, messages, conversation.RoleDeveloper, defaultCompactUserMessageTokens)...)
	}
	return kept
}

func (r *Runner) persistRuntimeCompaction(ctx context.Context, req RunRequest, trace *CompactionTrace, history, kept []llm.ChatMessage) error {
	if r.Snapshots == nil || req.ConversationID == nil || *req.ConversationID <= 0 || trace == nil || trace.Status != "completed" {
		return nil
	}
	current, err := r.Snapshots.FindCurrentSnapshot(ctx, req.OwnerID, *req.ConversationID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var parentID *int64
	parentVersion := 0
	if current != nil {
		parentID, parentVersion = &current.ID, current.SnapshotVersion
	}
	token := fmt.Sprintf("runtime-%d-%d", req.RunID, time.Now().UnixNano())
	claimed, err := r.Snapshots.ClaimSnapshot(ctx, req.OwnerID, *req.ConversationID, parentID, parentVersion, token, time.Now().Add(defaultCompactTimeout+5*time.Second))
	if err != nil || !claimed {
		return err
	}
	now := time.Now().UTC()
	fingerprintBytes := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", req.RunID, parentVersion, trace.Summary, contentFingerprint(history))))
	firstID, lastID := int64(0), int64(0)
	if req.InitialUserMessageID > 0 {
		firstID, lastID = req.InitialUserMessageID, req.InitialUserMessageID
		if req.TokenBudgetCompaction {
			firstID = lastID + 1
		}
	}
	providerID, model := req.CompactionProviderID, strings.TrimSpace(req.CompactionModel)
	if providerID == 0 || model == "" {
		providerID, model = r.ProviderID, req.Model
	}
	item := &conversation.Compaction{
		ImmutableModel: domain.ImmutableModel{OwnerID: req.OwnerID}, ConversationID: *req.ConversationID,
		FirstMessageID: firstID, LastMessageID: lastID, ParentSnapshotID: parentID, SnapshotVersion: parentVersion + 1,
		SourceFingerprint: hex.EncodeToString(fingerprintBytes[:]), TriggerType: conversation.CompactionTriggerRuntime,
		Status: conversation.CompactionCompleted, Summary: trace.Summary, PromptVersion: "runtime-compaction-v1",
		ProviderID: providerID, Model: model, BeforeTokens: trace.BeforeTokens, AfterTokens: modelMessagesTokens(req, kept),
		SummaryTokens: modelTextTokens(req, trace.Summary), CompletedAt: &now,
		ContextWindowTokens: req.ContextWindowTokens,
	}
	if !req.TokenBudgetCompaction && len(kept) > 0 && !strings.HasPrefix(kept[0].Content, compactionSummaryPrefix) {
		item.FirstMessageContent = kept[0].Content
	}
	if err := r.Snapshots.CompleteSnapshot(ctx, item, parentID, parentVersion, token); err != nil {
		_ = r.Snapshots.ReleaseSnapshotClaim(context.Background(), req.OwnerID, *req.ConversationID, token, err.Error())
		return err
	}
	return nil
}

func contentFingerprint(messages []llm.ChatMessage) string {
	data, _ := json.Marshal(messages)
	return string(data)
}

func modelTokenCount(req RunRequest, text string) tokencounter.Result {
	return tokencounter.Count(req.Provider.ProviderType, req.Model, text)
}

func modelTextTokens(req RunRequest, text string) int { return modelTokenCount(req, text).Tokens }

func modelMessagesTokens(req RunRequest, messages []llm.ChatMessage) int {
	if len(messages) == 0 {
		return 0
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return estimateMessagesTokens(messages)
	}
	return modelTextTokens(req, string(data))
}

func modelToolSchemaTokens(req RunRequest, tools []llm.ToolDefinition) int {
	if len(tools) == 0 {
		return 0
	}
	data, err := json.Marshal(tools)
	if err != nil {
		return estimateToolSchemaTokens(tools)
	}
	return modelTextTokens(req, string(data))
}

func hardPromptTokenLimit(req RunRequest) int {
	window := req.ContextWindowTokens
	if window <= 0 {
		window = req.MaxInputTokens
	}
	if window <= 0 {
		window = defaultMaxInputTokens
	}
	limit := window - maxInt(0, req.ReservedOutputTokens) - maxInt(0, req.ContextSafetyMarginTokens)
	if req.MaxInputTokens > 0 && req.MaxInputTokens < limit {
		limit = req.MaxInputTokens
	}
	return maxInt(1, limit)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
