// Package conversationcontext owns durable conversation compaction windows.
package conversationcontext

import (
	"context"
	"crypto/rand"
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

const (
	PromptVersion = "conversation-snapshot-v3"
)

var (
	ErrOverflow         = errors.New("context_overflow")
	ErrCompactionFailed = errors.New("context_compaction_failed")
)

type HistoryReader interface {
	ListActiveByConversation(context.Context, int64, int64) ([]conversation.Message, error)
}

type historyWindowReader interface {
	ListActiveBetween(context.Context, int64, int64, int64, int64) ([]conversation.Message, error)
	ListActiveAfter(context.Context, int64, int64, int64) ([]conversation.Message, error)
}

type Window struct {
	Snapshot *conversation.Compaction
	Messages []conversation.Message
}

type Trace struct {
	SnapshotID        int64     `json:"snapshot_id,omitempty"`
	FirstMessageID    int64     `json:"first_message_id,omitempty"`
	LastMessageID     int64     `json:"last_message_id,omitempty"`
	SnapshotVersion   int       `json:"snapshot_version,omitempty"`
	Reused            bool      `json:"reused,omitempty"`
	Created           bool      `json:"created,omitempty"`
	BeforeTokens      int       `json:"before_tokens,omitempty"`
	MeasuredTokens    int       `json:"measured_tokens,omitempty"`
	AfterTokens       int       `json:"after_tokens,omitempty"`
	IncrementalTokens int       `json:"incremental_tokens,omitempty"`
	Threshold         int       `json:"threshold,omitempty"`
	HardLimit         int       `json:"hard_limit,omitempty"`
	Usage             llm.Usage `json:"usage,omitempty"`
	ModelCalls        int       `json:"model_calls,omitempty"`
	ProviderID        int64     `json:"provider_id,omitempty"`
	Model             string    `json:"model,omitempty"`
	FallbackReason    string    `json:"fallback_reason,omitempty"`
	Failure           string    `json:"failure,omitempty"`
	LatencyMS         int64     `json:"latency_ms,omitempty"`
}

type summaryResult struct {
	Summary    string
	Usage      llm.Usage
	ModelCalls int
	ProviderID int64
	Model      string
}

type Request struct {
	OwnerID                       int64
	ConversationID                int64
	ProviderID                    int64
	Provider                      llm.ChatProviderConfig
	Model                         string
	CompactionProviderID          int64
	CompactionProvider            llm.ChatProviderConfig
	CompactionModel               string
	SystemPrompt                  string
	WindowTokens                  int
	ReservedOutput                int
	SafetyMargin                  int
	AutoLimit                     int
	AutoLimitScope                string
	Trigger                       string
	CompactPrompt                 string
	Force                         bool
	TokenBudgetCompaction         bool
	RetainClientDeveloperMessages bool
	Render                        func(Window) ([]llm.ChatMessage, int, error)
}

type Result struct {
	Window   Window
	Messages []llm.ChatMessage
	Trace    Trace
}

type Coordinator struct {
	History   HistoryReader
	Snapshots conversation.SnapshotRepository
	Client    llm.ChatClient
}

func (c Coordinator) Load(ctx context.Context, ownerID, conversationID int64) (Window, error) {
	if c.History == nil || conversationID <= 0 {
		return Window{}, nil
	}
	if c.Snapshots == nil {
		all, err := c.History.ListActiveByConversation(ctx, ownerID, conversationID)
		return Window{Messages: all}, err
	}
	snapshot, err := c.Snapshots.FindCurrentSnapshot(ctx, ownerID, conversationID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			all, listErr := c.History.ListActiveByConversation(ctx, ownerID, conversationID)
			return Window{Messages: all}, listErr
		}
		return Window{}, err
	}
	if snapshot.FirstMessageID <= 0 || snapshot.LastMessageID <= 0 {
		// Runtime rows created without an initial message ID are summary-only.
		return Window{Snapshot: snapshot}, nil
	}
	if ranged, ok := c.History.(historyWindowReader); ok {
		frozen, rangeErr := ranged.ListActiveBetween(ctx, ownerID, conversationID, snapshot.FirstMessageID, snapshot.LastMessageID)
		if rangeErr != nil {
			return Window{}, rangeErr
		}
		tail, tailErr := ranged.ListActiveAfter(ctx, ownerID, conversationID, snapshot.LastMessageID)
		if tailErr != nil {
			return Window{}, tailErr
		}
		return Window{Snapshot: snapshot, Messages: append(filterFrozen(snapshot, frozen), tail...)}, nil
	}
	all, err := c.History.ListActiveByConversation(ctx, ownerID, conversationID)
	if err != nil {
		return Window{}, err
	}
	frozen := filterFrozen(snapshot, all)
	return Window{Snapshot: snapshot, Messages: append(frozen, messagesAfter(all, snapshot.LastMessageID)...)}, nil
}

func filterFrozen(snapshot *conversation.Compaction, messages []conversation.Message) []conversation.Message {
	frozen := make([]conversation.Message, 0)
	frozenRole := conversation.RoleUser
	if snapshot.Summary == "" {
		frozenRole = conversation.RoleDeveloper
	}
	for _, message := range messages {
		if message.ID >= snapshot.FirstMessageID && message.ID <= snapshot.LastMessageID && message.Role == frozenRole {
			if message.ID == snapshot.FirstMessageID && snapshot.FirstMessageContent != "" {
				message.Content = snapshot.FirstMessageContent
			}
			frozen = append(frozen, message)
		}
	}
	return frozen
}

func (c Coordinator) Prepare(ctx context.Context, req Request) (Result, error) {
	if req.ConversationID <= 0 || c.History == nil || c.Snapshots == nil {
		return c.render(req, Window{}, false)
	}
	window, err := c.Load(ctx, req.OwnerID, req.ConversationID)
	if err != nil {
		return Result{}, err
	}
	result, err := c.render(req, window, window.Snapshot != nil)
	if err != nil {
		return Result{}, err
	}
	modelDowngrade := false
	if window.Snapshot != nil && window.Snapshot.Model != "" && window.Snapshot.Model != req.Model && req.WindowTokens > 0 && window.Snapshot.ContextWindowTokens > req.WindowTokens {
		if strings.TrimSpace(req.AutoLimitScope) == "body_after_prefix" {
			modelDowngrade = result.Trace.MeasuredTokens >= result.Trace.HardLimit
		} else {
			modelDowngrade = result.Trace.MeasuredTokens > result.Trace.Threshold
		}
	}
	if !req.Force && !modelDowngrade && result.Trace.MeasuredTokens < result.Trace.Threshold && result.Trace.BeforeTokens <= result.Trace.HardLimit {
		return result, nil
	}
	if len(window.Messages) > 0 {
		latest := window.Messages[len(window.Messages)-1]
		if latest.Role == conversation.RoleUser && tokencounter.Count(req.Provider.ProviderType, req.Model, latest.Content).Tokens > result.Trace.HardLimit {
			return result, fmt.Errorf("%w: current user input alone exceeds allowed prompt tokens", ErrOverflow)
		}
	}
	if c.Client == nil && !req.TokenBudgetCompaction {
		result.Trace.Failure = "llm client is unavailable"
		return result, fmt.Errorf("%w: %s", ErrCompactionFailed, result.Trace.Failure)
	}
	return c.compact(ctx, req, window, result.Trace)
}

func (c Coordinator) render(req Request, window Window, reused bool) (Result, error) {
	if req.Render == nil {
		return Result{}, fmt.Errorf("conversation context renderer is required")
	}
	messages, extra, err := req.Render(window)
	if err != nil {
		return Result{}, err
	}
	windowTokens := req.WindowTokens
	if windowTokens <= 0 {
		windowTokens = 128000
	}
	reserved := req.ReservedOutput
	if reserved <= 0 {
		reserved = min(8000, max(1, windowTokens/8))
	}
	margin := req.SafetyMargin
	if margin <= 0 {
		margin = min(1024, max(1, windowTokens/100))
	}
	hard := max(1, windowTokens-reserved-margin)
	threshold := int(float64(windowTokens) * .90)
	if req.AutoLimit > 0 && req.AutoLimit < threshold {
		threshold = req.AutoLimit
	}
	if threshold > hard {
		threshold = hard
	}
	body := count(req.Provider.ProviderType, req.Model, messages)
	before := body + extra
	measured := before
	if strings.TrimSpace(req.AutoLimitScope) == "body_after_prefix" {
		measured = body
	}
	incremental := 0
	for _, message := range window.Messages {
		incremental += tokencounter.Count(req.Provider.ProviderType, req.Model, message.Content).Tokens
	}
	trace := Trace{Reused: reused, Threshold: threshold, HardLimit: hard, BeforeTokens: before, MeasuredTokens: measured, IncrementalTokens: incremental}
	if window.Snapshot != nil {
		trace.SnapshotID, trace.SnapshotVersion = window.Snapshot.ID, window.Snapshot.SnapshotVersion
		trace.FirstMessageID, trace.LastMessageID = window.Snapshot.FirstMessageID, window.Snapshot.LastMessageID
	}
	return Result{Window: window, Messages: messages, Trace: trace}, nil
}

func (c Coordinator) compact(ctx context.Context, req Request, current Window, before Trace) (Result, error) {
	parentID, parentVersion := (*int64)(nil), 0
	if current.Snapshot != nil {
		parentID, parentVersion = &current.Snapshot.ID, current.Snapshot.SnapshotVersion
	}
	token, err := randomToken()
	if err != nil {
		before.Failure = err.Error()
		return Result{Window: current, Trace: before}, err
	}
	claimed, err := c.Snapshots.ClaimSnapshot(ctx, req.OwnerID, req.ConversationID, parentID, parentVersion, token, time.Now().UTC().Add(compaction.SummarizeTimeout+5*time.Second))
	if err != nil {
		before.Failure = err.Error()
		return Result{Window: current, Trace: before}, err
	}
	if !claimed {
		window, loadErr := c.Load(ctx, req.OwnerID, req.ConversationID)
		if loadErr != nil {
			return Result{}, loadErr
		}
		result, renderErr := c.render(req, window, window.Snapshot != nil)
		if renderErr != nil {
			return Result{}, renderErr
		}
		return result, nil
	}
	fail := func(err error) (Result, error) {
		_ = c.Snapshots.ReleaseSnapshotClaim(context.Background(), req.OwnerID, req.ConversationID, token, err.Error())
		before.Failure = err.Error()
		return Result{Window: current, Trace: before}, err
	}
	all, err := c.History.ListActiveByConversation(ctx, req.OwnerID, req.ConversationID)
	if err != nil {
		return fail(err)
	}
	summaryResult := summaryResult{ProviderID: req.ProviderID, Model: req.Model}
	retained := []conversation.Message(nil)
	if req.TokenBudgetCompaction {
		if req.RetainClientDeveloperMessages {
			retained = retainRoleMessages(req, all, conversation.RoleDeveloper, compaction.UserMessageBudgetTokens)
		}
	} else {
		summaryResult, err = c.compactHistory(ctx, req, current.Snapshot, all)
		if err != nil {
			return fail(err)
		}
		if strings.TrimSpace(summaryResult.Summary) == "" {
			summaryResult.Summary = compaction.FallbackSummary
		}
		retained = retainRoleMessages(req, all, conversation.RoleUser, compaction.UserMessageBudgetTokens)
	}
	firstID, lastID := int64(0), int64(0)
	if len(retained) > 0 {
		firstID, lastID = retained[0].ID, retained[len(retained)-1].ID
	}
	if req.TokenBudgetCompaction && len(all) > 0 {
		lastID = all[len(all)-1].ID
		if len(retained) == 0 {
			firstID = lastID + 1
		}
	}
	now := time.Now().UTC()
	item := &conversation.Compaction{
		ImmutableModel: domain.ImmutableModel{OwnerID: req.OwnerID}, ConversationID: req.ConversationID,
		FirstMessageID: firstID, LastMessageID: lastID, ParentSnapshotID: parentID, SnapshotVersion: parentVersion + 1,
		SourceFingerprint: fingerprint(current.Snapshot, all, req), TriggerType: trigger(req.Trigger), Status: conversation.CompactionCompleted,
		Summary: summaryResult.Summary, PromptVersion: PromptVersion, PromptHash: promptHash(req.CompactPrompt), ProviderID: summaryResult.ProviderID,
		Model: summaryResult.Model, BeforeTokens: before.BeforeTokens, SummaryTokens: tokencounter.Count(req.Provider.ProviderType, req.Model, summaryResult.Summary).Tokens, CompletedAt: &now,
		ContextWindowTokens: req.WindowTokens,
	}
	if len(retained) > 0 {
		item.FirstMessageContent = retained[0].Content
	}
	next := Window{Snapshot: item, Messages: composeWindow(all, item)}
	prepared, err := c.render(req, next, false)
	if err != nil {
		return fail(err)
	}
	item.AfterTokens = prepared.Trace.BeforeTokens
	if prepared.Trace.BeforeTokens > prepared.Trace.HardLimit {
		return fail(overflow(prepared.Trace))
	}
	if err := c.Snapshots.CompleteSnapshot(ctx, item, parentID, parentVersion, token); err != nil {
		return fail(fmt.Errorf("%w: persist snapshot: %v", ErrCompactionFailed, err))
	}
	prepared.Window = next
	prepared.Trace.SnapshotID, prepared.Trace.SnapshotVersion = item.ID, item.SnapshotVersion
	prepared.Trace.FirstMessageID, prepared.Trace.LastMessageID = item.FirstMessageID, item.LastMessageID
	prepared.Trace.Created, prepared.Trace.Reused = true, false
	prepared.Trace.Usage, prepared.Trace.ModelCalls = summaryResult.Usage, summaryResult.ModelCalls
	prepared.Trace.ProviderID, prepared.Trace.Model = summaryResult.ProviderID, summaryResult.Model
	prepared.Trace.BeforeTokens, prepared.Trace.AfterTokens = before.BeforeTokens, item.AfterTokens
	return prepared, nil
}

// compactHistory delegates to the shared compaction core so cross-turn and
// mid-run triggers run the exact same algorithm.
func (c Coordinator) compactHistory(ctx context.Context, req Request, parent *conversation.Compaction, messages []conversation.Message) (summaryResult, error) {
	provider, providerID, model := req.CompactionProvider, req.CompactionProviderID, strings.TrimSpace(req.CompactionModel)
	if strings.TrimSpace(provider.ProviderType) == "" || model == "" {
		provider, providerID, model = req.Provider, req.ProviderID, req.Model
	}
	parentSummary := ""
	if parent != nil {
		parentSummary = strings.TrimSpace(parent.Summary)
	}
	coreReq := compaction.Request{
		SystemPrompt:  req.SystemPrompt,
		CompactPrompt: req.CompactPrompt,
		Provider:      provider,
		Model:         model,
		ParentSummary: parentSummary,
	}
	result, err := compaction.Compact(ctx, c.Client, coreReq, compaction.FromMessages(messages))
	summary := summaryResult{ProviderID: providerID, Model: model, Summary: result.Summary, Usage: result.Usage, ModelCalls: result.ModelCalls}
	if err != nil {
		return summary, fmt.Errorf("%w: %v", ErrCompactionFailed, err)
	}
	return summary, nil
}

// retainRoleMessages delegates selection and truncation to the shared
// compaction core and maps the retained entries back to their source rows.
func retainRoleMessages(req Request, messages []conversation.Message, role string, budget int) []conversation.Message {
	entries := compaction.RetainEntriesByRole(compaction.Request{
		Provider: req.Provider, Model: req.Model, UserBudget: budget,
	}, compaction.FromMessages(messages), role)
	if len(entries) == 0 {
		return nil
	}
	byID := make(map[int64]conversation.Message, len(messages))
	for _, message := range messages {
		byID[message.ID] = message
	}
	kept := make([]conversation.Message, 0, len(entries))
	for _, entry := range entries {
		message, ok := byID[entry.MessageID]
		if !ok {
			continue
		}
		message.Content = entry.Content
		kept = append(kept, message)
	}
	return kept
}

func composeWindow(all []conversation.Message, snapshot *conversation.Compaction) []conversation.Message {
	if snapshot == nil || snapshot.FirstMessageID <= 0 || snapshot.LastMessageID <= 0 {
		return nil
	}
	frozen := make([]conversation.Message, 0)
	frozenRole := conversation.RoleUser
	if snapshot.Summary == "" {
		frozenRole = conversation.RoleDeveloper
	}
	for _, message := range all {
		if message.ID >= snapshot.FirstMessageID && message.ID <= snapshot.LastMessageID && message.Role == frozenRole {
			if message.ID == snapshot.FirstMessageID && snapshot.FirstMessageContent != "" {
				message.Content = snapshot.FirstMessageContent
			}
			frozen = append(frozen, message)
		}
	}
	return append(frozen, messagesAfter(all, snapshot.LastMessageID)...)
}

func messagesAfter(all []conversation.Message, id int64) []conversation.Message {
	if id <= 0 {
		return nil
	}
	i := 0
	for i < len(all) && all[i].ID <= id {
		i++
	}
	return all[i:]
}

func trigger(value string) string {
	switch value {
	case conversation.CompactionTriggerManual, conversation.CompactionTriggerRuntime:
		return value
	default:
		return conversation.CompactionTriggerAuto
	}
}

func overflow(trace Trace) error {
	return fmt.Errorf("%w: estimated_prompt_tokens=%d allowed_prompt_tokens=%d", ErrOverflow, trace.BeforeTokens, trace.HardLimit)
}

func count(provider, model string, messages []llm.ChatMessage) int {
	data, err := json.Marshal(messages)
	if err == nil {
		return tokencounter.Count(provider, model, string(data)).Tokens
	}
	total := 0
	for _, message := range messages {
		total += tokencounter.Count(provider, model, message.Content).Tokens
	}
	return total
}

func fingerprint(parent *conversation.Compaction, messages []conversation.Message, req Request) string {
	h := sha256.New()
	if parent != nil {
		fmt.Fprintf(h, "%d:%d:%s\x00", parent.ID, parent.SnapshotVersion, parent.SourceFingerprint)
	}
	for _, message := range messages {
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00", message.ID, message.Role, message.Content)
	}
	fmt.Fprintf(h, "%d:%s:%s:%s", req.ProviderID, req.Model, PromptVersion, promptHash(req.CompactPrompt))
	return hex.EncodeToString(h.Sum(nil))
}

func promptHash(value string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(h[:])
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func addChatUsage(left, right llm.Usage) llm.Usage {
	return llm.Usage{PromptTokens: left.PromptTokens + right.PromptTokens, CompletionTokens: left.CompletionTokens + right.CompletionTokens, TotalTokens: left.TotalTokens + right.TotalTokens, CachedInputTokens: left.CachedInputTokens + right.CachedInputTokens, ReasoningTokens: left.ReasoningTokens + right.ReasoningTokens}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
