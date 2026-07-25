package chat_usecase

import (
	"context"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/tokencounter"
	"agentcanvas/internal/runtime/conversationcontext"
)

type CompactConversationRequest struct {
	ProviderID       int64  `json:"provider_id" binding:"required"`
	Model            string `json:"model"`
	CompactPrompt    string `json:"compact_prompt"`
	ThroughMessageID int64  `json:"through_message_id"`
}

func (s *Service) ConfigureConversationContext(coordinator *conversationcontext.Coordinator) {
	s.contextCoordinator = coordinator
}

func (s *Service) CompactConversation(ctx context.Context, ownerID, conversationID int64, req CompactConversationRequest) (*conversation.Compaction, error) {
	if ownerID <= 0 || conversationID <= 0 || req.ProviderID <= 0 || s.contextCoordinator == nil {
		return nil, fmt.Errorf("conversation snapshot compaction is not configured")
	}
	if _, err := s.conversations.FindByID(ctx, ownerID, conversationID); err != nil {
		return nil, err
	}
	_, model, provider, err := s.providerConfig(ctx, ownerID, req.ProviderID, req.Model)
	if err != nil {
		return nil, err
	}
	prepared, err := s.contextCoordinator.Prepare(ctx, conversationcontext.Request{
		OwnerID: ownerID, ConversationID: conversationID, ProviderID: req.ProviderID, Provider: provider, Model: model,
		WindowTokens: 128000, Trigger: conversation.CompactionTriggerManual, CompactPrompt: req.CompactPrompt, Force: true, ThroughMessageID: req.ThroughMessageID,
		Render: func(window conversationcontext.Window) ([]llm.ChatMessage, int, error) {
			return s.chatMessagesForWindow("", window, ""), 0, nil
		},
	})
	if err != nil {
		return nil, err
	}
	if !prepared.Trace.Created {
		return prepared.Window.Snapshot, nil
	}
	return prepared.Window.Snapshot, nil
}

func (s *Service) prepareChatMessages(ctx context.Context, ownerID, conversationID, providerID int64, model string, provider llm.ChatProviderConfig, req ChatRequest, systemPrompt string, question string) ([]llm.ChatMessage, *conversation.Compaction, llm.Usage, error) {
	if s.contextCoordinator == nil {
		history, err := s.messages.ListActiveByConversation(ctx, ownerID, conversationID)
		if err != nil {
			return nil, nil, llm.Usage{}, err
		}
		messages := s.chatMessagesForWindow(systemPrompt, conversationcontext.Window{Messages: history}, question)
		if estimateChatMessages(provider.ProviderType, model, messages) > chatHardPromptLimit(req, req.ContextWindowTokens) {
			return nil, nil, llm.Usage{}, fmt.Errorf("context_overflow")
		}
		return messages, nil, llm.Usage{}, nil
	}
	prepared, err := s.contextCoordinator.Prepare(ctx, conversationcontext.Request{
		OwnerID: ownerID, ConversationID: conversationID, ProviderID: providerID, Provider: provider, Model: model,
		WindowTokens: req.ContextWindowTokens, ReservedOutput: req.ReservedOutputTokens, SafetyMargin: req.ContextSafetyMarginTokens,
		AutoLimit: req.ModelAutoCompactTokenLimit, Trigger: conversation.CompactionTriggerAuto, CompactPrompt: req.CompactPrompt,
		Render: func(window conversationcontext.Window) ([]llm.ChatMessage, int, error) {
			return s.chatMessagesForWindow(systemPrompt, window, question), 0, nil
		},
	})
	if err != nil {
		return nil, nil, llm.Usage{}, err
	}
	return prepared.Messages, prepared.Window.Snapshot, prepared.Trace.Usage, nil
}

func (s *Service) chatMessagesForWindow(systemPrompt string, window conversationcontext.Window, question string) []llm.ChatMessage {
	messages := make([]llm.ChatMessage, 0, len(window.Messages)+3)
	if strings.TrimSpace(systemPrompt) != "" {
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleSystem, Content: systemPrompt})
	}
	if window.Snapshot != nil && strings.TrimSpace(window.Snapshot.Summary) != "" {
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleSystem, Content: "EARLIER CONVERSATION SNAPSHOT:\n" + window.Snapshot.Summary})
	}
	for _, item := range window.Messages {
		if role, content := strings.TrimSpace(item.Role), strings.TrimSpace(item.Content); content != "" && validChatRole(role) {
			messages = append(messages, llm.ChatMessage{Role: role, Content: content})
		}
	}
	if strings.TrimSpace(question) != "" {
		messages = append(messages, llm.ChatMessage{Role: conversation.RoleUser, Content: question})
	}
	return messages
}

func estimateChatMessages(providerType, model string, messages []llm.ChatMessage) int {
	total := 0
	for _, message := range messages {
		total += estimateModelTokens(providerType, model, message.Content)
	}
	return total
}

func estimateModelTokens(providerType, model, text string) int {
	return tokencounter.Count(providerType, model, text).Tokens
}
func chatHardPromptLimit(req ChatRequest, window int) int {
	if window <= 0 {
		window = 128000
	}
	reserved := req.ReservedOutputTokens
	if reserved <= 0 {
		reserved = min(8000, max(1, window/8))
	}
	margin := req.ContextSafetyMarginTokens
	if margin <= 0 {
		margin = min(1024, max(1, window/100))
	}
	return max(1, window-reserved-margin)
}
func chatAddUsage(left, right llm.Usage) llm.Usage {
	return llm.Usage{PromptTokens: left.PromptTokens + right.PromptTokens, CompletionTokens: left.CompletionTokens + right.CompletionTokens, TotalTokens: left.TotalTokens + right.TotalTokens}
}
