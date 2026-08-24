package agentruntime

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/tokencounter"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/conversationcontext"
	"agentcanvas/internal/runtime/harness/rules"
	"agentcanvas/internal/runtime/toolruntime"
)

func buildConversationContext(ctx context.Context, n runtimeCore, rc *RunContext, task string, maxInputChars int, policy retrievalPolicy) []runtimeagent.ContextBlock {
	if n.MessageHistory == nil || rc.ConversationID == nil || *rc.ConversationID <= 0 {
		return nil
	}
	msgs, err := n.MessageHistory.ListByConversation(ctx, rc.OwnerID, *rc.ConversationID)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	const maxRecentMessages = 20
	recent := msgs
	older := msgs[:0]
	if len(msgs) > maxRecentMessages {
		older = msgs[:len(msgs)-maxRecentMessages]
		recent = msgs[len(msgs)-maxRecentMessages:]
	}
	selected := make([]conversation.Message, 0, len(recent)+8)
	if len(older) > 0 && n.ContextIndex != nil && strings.TrimSpace(task) != "" && (policy.Enabled == nil || *policy.Enabled) {
		topK := 8
		if policy.CandidateK > 0 {
			topK = min(20, policy.CandidateK)
		}
		hits, searchErr := n.ContextIndex.Search(ctx, contextresource.SearchRequest{
			OwnerID:        rc.OwnerID,
			AgentID:        rc.AgentID,
			ConversationID: *rc.ConversationID,
			ResourceTypes:  []string{contextresource.TypeConversationMessage},
			Query:          task,
			Mode:           policy.Mode,
			TopK:           topK,
			Profile: contextresource.EmbeddingProfile{
				ProviderID: policy.EmbeddingProviderID,
				Model:      policy.EmbeddingModel,
				Dimensions: policy.EmbeddingDimensions,
			},
		})
		// // ***
		if searchErr == nil {
			olderByID := make(map[string]conversation.Message, len(older))
			for i := range older {
				olderByID[strconv.FormatInt(older[i].ID, 10)] = older[i]
			}
			for _, hit := range hits {
				if message, ok := olderByID[hit.ResourceID]; ok {
					selected = append(selected, message)
					delete(olderByID, hit.ResourceID)
				}
			}
		}
		// // ***
	}
	selected = append(selected, recent...)
	blocks := make([]runtimeagent.ContextBlock, 0, len(selected))
	for _, m := range selected {
		blocks = append(blocks, runtimeagent.ContextBlock{
			Name:    "conversation",
			Role:    m.Role,
			Content: m.Content,
			Pinned:  false,
		})
	}
	return blocks
}

// buildPreparedConversationContext uses the durable snapshot coordinator as
// the single source of truth for cross-run history. The legacy builder remains
// the small compatibility fallback when snapshot persistence is unavailable.
func buildPreparedConversationContext(
	ctx context.Context,
	n runtimeCore,
	rc *RunContext,
	task string,
	cfg agentRuntimeConfig,
	provider llm.ChatProviderConfig,
	providerID int64,
	model string,
	compactionProvider llm.ChatProviderConfig,
	compactionProviderID int64,
	compactionModel string,
	systemPrompt string,
	tools []llm.ToolDefinition,
	budgetBlocks []runtimeagent.ContextBlock,
) ([]runtimeagent.ContextBlock, conversationcontext.Trace, bool, error) {
	if n.Coordinator == nil || rc == nil || rc.ConversationID == nil || *rc.ConversationID <= 0 {
		return buildConversationContext(ctx, n, rc, task, cfg.MaxInputChars, cfg.RetrievalPolicy), conversationcontext.Trace{}, false, nil
	}
	extraTokens := coordinatorExtraTokens(provider.ProviderType, model, systemPrompt, task, cfg.MaxRuleTokens, tools, budgetBlocks)
	started := time.Now()
	prepared, err := n.Coordinator.Prepare(ctx, conversationcontext.Request{
		OwnerID:              rc.OwnerID,
		ConversationID:       *rc.ConversationID,
		ProviderID:           providerID,
		Provider:             provider,
		Model:                model,
		CompactionProviderID: compactionProviderID,
		CompactionProvider:   compactionProvider,
		CompactionModel:      compactionModel,
		WindowTokens:         cfg.ContextWindowTokens,
		ReservedOutput:       cfg.ReservedOutputTokens,
		SafetyMargin:         cfg.ContextSafetyMarginTokens,
		AutoLimit:            cfg.ModelAutoCompactTokenLimit,
		Trigger:              conversation.CompactionTriggerAuto,
		CompactPrompt:        cfg.CompactPrompt,
		Render: func(window conversationcontext.Window) ([]llm.ChatMessage, int, error) {
			messages := make([]llm.ChatMessage, 0, len(window.Messages)+1)
			if window.Snapshot != nil && strings.TrimSpace(window.Snapshot.Summary) != "" {
				messages = append(messages, llm.ChatMessage{Role: conversation.RoleSystem, Content: "EARLIER CONVERSATION SNAPSHOT:\n" + strings.TrimSpace(window.Snapshot.Summary)})
			}
			for _, item := range window.Messages {
				messages = append(messages, llm.ChatMessage{Role: item.Role, Content: item.Content})
			}
			return messages, extraTokens, nil
		},
	})
	if err != nil {
		return nil, prepared.Trace, true, err
	}
	prepared.Trace.LatencyMS = time.Since(started).Milliseconds()
	blocks := make([]runtimeagent.ContextBlock, 0, len(prepared.Messages))
	for _, message := range prepared.Messages {
		name := "conversation"
		if message.Role == conversation.RoleSystem && strings.HasPrefix(message.Content, "EARLIER CONVERSATION SNAPSHOT:") {
			name = "history_snapshot"
		}
		blocks = append(blocks, runtimeagent.ContextBlock{Name: name, Role: message.Role, Content: message.Content})
	}
	return blocks, prepared.Trace, true, nil
}

func runtimeToolDefinitions(tools []toolruntime.RuntimeTool) []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(tools))
	for _, item := range tools {
		name := strings.TrimSpace(item.Name())
		if name == "" {
			continue
		}
		definitions = append(definitions, llm.ToolDefinition{Type: "function", Function: llm.ToolFunctionDefinition{
			Name: name, Description: item.Description(), Parameters: item.Parameters(), Strict: true,
		}})
	}
	return definitions
}

func coordinatorExtraTokens(providerType, model, systemPrompt, task string, maxRuleTokens int, tools []llm.ToolDefinition, blocks []runtimeagent.ContextBlock) int {
	total := tokencounter.Count(providerType, model, systemPrompt).Tokens
	total += tokencounter.Count(providerType, model, task).Tokens
	total += maxRuleTokens
	for _, block := range blocks {
		total += tokencounter.Count(providerType, model, block.Content).Tokens
	}
	if len(tools) == 0 {
		return total
	}
	data, err := json.Marshal(tools)
	if err != nil {
		return total
	}
	return total + tokencounter.Count(providerType, model, string(data)).Tokens
}

func queryTurnsFromConversation(ctx context.Context, history MessageHistoryReader, rc *RunContext) []retrieval.QueryTurn {
	if history == nil || rc.ConversationID == nil || *rc.ConversationID <= 0 {
		return nil
	}
	messages, err := history.ListByConversation(ctx, rc.OwnerID, *rc.ConversationID)
	if err != nil || len(messages) == 0 {
		return nil
	}
	if len(messages) > 20 {
		messages = messages[len(messages)-20:]
	}
	turns := make([]retrieval.QueryTurn, 0, len(messages))
	for i := range messages {
		if content := strings.TrimSpace(messages[i].Content); content != "" {
			turns = append(turns, retrieval.QueryTurn{Role: messages[i].Role, Content: content})
		}
	}
	return turns
}

func buildRuleContextBlocks(
	systemPrompt, task, mode string,
	cfg agentRuntimeConfig,
	tools []toolruntime.RuntimeTool,
	conversation []runtimeagent.ContextBlock,
) ([]runtimeagent.ContextBlock, rules.Trace, []string, string) {
	risk := highestToolRisk(tools)
	if risk == "" {
		risk = highestConfiguredRisk(cfg.RequireApprovalForRisk)
	}
	tags := inferRuleTags(task, mode, cfg, tools, conversation)
	selected, trace := rules.SelectMandatoryRules(cfg.Rules)
	trace.RuleHash = cfg.RuleHash
	blocks := make([]runtimeagent.ContextBlock, 0, 2)
	for _, item := range selected {
		if item.Strength != rules.RuleMandatory {
			continue
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		blocks = append(blocks, runtimeagent.ContextBlock{
			Name:    ruleBlockName(item.ID),
			Role:    "system",
			Content: content,
			Pinned:  true,
		})
	}
	return blocks, trace, tags, risk
}

func highestToolRisk(tools []toolruntime.RuntimeTool) string {
	best := ""
	bestWeight := -1
	for _, item := range tools {
		risk := strings.TrimSpace(toolruntime.MetadataOf(item).RiskLevel)
		weight := riskWeight(risk)
		if weight > bestWeight {
			best = risk
			bestWeight = weight
		}
	}
	return best
}

func highestConfiguredRisk(values []string) string {
	best := ""
	bestWeight := -1
	for _, value := range values {
		weight := riskWeight(value)
		if weight > bestWeight {
			best = strings.TrimSpace(value)
			bestWeight = weight
		}
	}
	return best
}

func riskWeight(risk string) int {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case toolruntime.RiskHigh:
		return 3
	case toolruntime.RiskMedium:
		return 2
	case toolruntime.RiskLow:
		return 1
	default:
		return 0
	}
}

func inferRuleTags(task, mode string, cfg agentRuntimeConfig, tools []toolruntime.RuntimeTool, conversation []runtimeagent.ContextBlock) []string {
	tags := make([]string, 0, 16)
	if len(cfg.KnowledgeBaseIDs) > 0 {
		tags = append(tags, "retrieval", "knowledge", "rag")
	}
	if cfg.MemoryEnabled {
		tags = append(tags, "memory")
	}
	if cfg.CodeExecutionEnabled {
		tags = append(tags, "code", "engineering")
	}
	if mode == "plan_execute" {
		tags = append(tags, "planning")
	}
	text := strings.ToLower(strings.TrimSpace(task + "\n" + conversationText(conversation)))
	for _, marker := range []struct {
		substring string
		tag       string
	}{
		{"review", "review"},
		{"code", "code"},
		{"bug", "engineering"},
		{"test", "engineering"},
		{"build", "engineering"},
		{"citation", "retrieval"},
		{"pdf", "document"},
		{"summary", "compression"},
		{"32k", "long_context"},
		{"上下文", "long_context"},
	} {
		if strings.Contains(text, marker.substring) {
			tags = append(tags, marker.tag)
		}
	}
	for _, tool := range tools {
		name := strings.ToLower(strings.TrimSpace(tool.Name()))
		if name != "" {
			tags = append(tags, name)
		}
	}
	return dedupeLower(tags)
}

func conversationText(blocks []runtimeagent.ContextBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		if content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}

func ruleBlockName(ruleID string) string {
	return "rules_mandatory:" + strings.TrimSpace(ruleID)
}

func dedupeLower(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func (n runtimeCore) checkExtractionTrigger(ctx context.Context, rc *RunContext, result *runtimeagent.RunResult, roundNumber int, memoryEnabled bool) {
	if !memoryEnabled || n.OnExtractTrigger == nil || rc == nil || rc.ConversationID == nil || result == nil || result.StopReason != runtimeagent.StopReasonFinalAnswer {
		return
	}
	n.OnExtractTrigger(ctx, rc.OwnerID, *rc.ConversationID, roundNumber)
}
