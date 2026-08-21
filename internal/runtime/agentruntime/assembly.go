package agentruntime

import (
	"context"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/retrieval"
	runtimeagent "agentcanvas/internal/runtime/agent"
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
	if len(cfg.KnowledgeIDs) > 0 {
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
