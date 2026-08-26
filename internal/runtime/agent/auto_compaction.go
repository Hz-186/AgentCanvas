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
	"agentcanvas/internal/runtime/compaction"

	"gorm.io/gorm"
)

func autoCompactLimit(req RunRequest) int {
	window := req.ContextWindowTokens
	if window <= 0 {
		window = req.MaxInputTokens
	}
	if window <= 0 {
		window = defaultMaxInputTokens
	}
	limit := int(float64(window) * compaction.ThresholdRatio)
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

// compactRuntimeTranscript delegates to the shared compaction core: the
// complete history (tool entries included) goes to the summarizer and the
// result contains only retained user messages plus a final user-role summary.
func (r *Runner) compactRuntimeTranscript(ctx context.Context, req RunRequest, transcript []llm.ChatMessage) ([]llm.ChatMessage, llm.Usage, *CompactionTrace) {
	history := runtimeCompactionHistory(req, transcript)
	beforeTokens := modelMessagesTokens(req, history)
	trace := &CompactionTrace{Trigger: "runtime", Scope: autoCompactScope(req), Status: "completed"}
	if req.TokenBudgetCompaction {
		var retained []llm.ChatMessage
		if req.RetainClientDeveloperMessages {
			retained = retainMessagesByRole(req, history, conversation.RoleDeveloper, compaction.UserMessageBudgetTokens)
		}
		trace.AfterTokens = modelMessagesTokens(req, retained)
		trace.SavedTokens = maxInt(0, beforeTokens-trace.AfterTokens)
		return retained, llm.Usage{}, trace
	}
	provider, model := req.CompactionProvider, strings.TrimSpace(req.CompactionModel)
	if strings.TrimSpace(provider.ProviderType) == "" || model == "" {
		provider, model = req.Provider, req.Model
	}
	coreReq := compaction.Request{
		SystemPrompt:  req.SystemPrompt,
		CompactPrompt: req.CompactPrompt,
		Provider:      provider,
		Model:         model,
	}
	result, err := compaction.Compact(ctx, chatClientAdapter{r.LLM}, coreReq, compaction.FromChat(history))
	trace.ModelCalled = true
	trace.Summary = result.Summary
	if err != nil {
		trace.Status = "failed"
		trace.Error = err.Error()
		return transcript, result.Usage, trace
	}
	kept := make([]llm.ChatMessage, 0, len(result.Retained)+1)
	for _, entry := range result.Retained {
		kept = append(kept, llm.ChatMessage{Role: entry.Role, Content: entry.Content})
	}
	kept = append(kept, llm.ChatMessage{Role: conversation.RoleUser, Content: compaction.SummaryPrefix + result.Summary})
	trace.AfterTokens = modelMessagesTokens(req, kept)
	trace.SavedTokens = maxInt(0, beforeTokens-trace.AfterTokens)
	return kept, result.Usage, trace
}

// chatClientAdapter lets the core package drive a ToolCallingClient through
// the plain Chat interface.
type chatClientAdapter struct{ client llm.ToolCallingClient }

func (a chatClientAdapter) Chat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	response, err := a.client.ChatWithTools(ctx, cfg, llm.ToolChatRequest{Model: req.Model, Messages: req.Messages, Temperature: req.Temperature})
	if err != nil || response == nil {
		return nil, err
	}
	return &llm.ChatResponse{Content: response.Message.Content, Usage: response.Usage}, nil
}

func (a chatClientAdapter) StreamChat(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ChatRequest, onEvent func(llm.StreamEvent) error) error {
	return errors.New("streaming unsupported for compaction")
}

func runtimeCompactionHistory(req RunRequest, transcript []llm.ChatMessage) []llm.ChatMessage {
	history := append([]llm.ChatMessage(nil), transcript...)
	if req.Task != "" && !hasUserContent(history, req.Task) {
		history = append([]llm.ChatMessage{{Role: conversation.RoleUser, Content: req.Task}}, history...)
	}
	return history
}

// retainMessagesByRole delegates selection and truncation to the shared
// compaction core and converts the retained entries back to chat messages.
func retainMessagesByRole(req RunRequest, history []llm.ChatMessage, role string, budget int) []llm.ChatMessage {
	entries := compaction.RetainEntriesByRole(compaction.Request{
		Provider: req.Provider, Model: req.Model, UserBudget: budget,
	}, compaction.FromChat(history), role)
	kept := make([]llm.ChatMessage, 0, len(entries))
	for _, entry := range entries {
		kept = append(kept, llm.ChatMessage{Role: entry.Role, Content: entry.Content})
	}
	return kept
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
		kept = append(kept, retainMessagesByRole(req, messages, conversation.RoleDeveloper, compaction.UserMessageBudgetTokens)...)
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
	claimed, err := r.Snapshots.ClaimSnapshot(ctx, req.OwnerID, *req.ConversationID, parentID, parentVersion, token, time.Now().Add(compaction.SummarizeTimeout+5*time.Second))
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
	if !req.TokenBudgetCompaction && len(kept) > 0 && !strings.HasPrefix(kept[0].Content, compaction.SummaryPrefix) {
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
