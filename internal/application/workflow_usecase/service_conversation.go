package workflow_usecase

import (
	"context"
	"strings"
	"unicode/utf8"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/workflow"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeevent "agentcanvas/internal/runtime/event"

	"gorm.io/gorm"
)

type CreateWorkflowConversationRequest struct {
	Title string `json:"title"`
}

type WorkflowMessageRequest struct {
	Question      string         `json:"question" binding:"required"`
	FlowVersionID int64          `json:"flow_version_id"`
	Input         map[string]any `json:"input"`
}

type WorkflowMessageResponse struct {
	Conversation     *conversation.Conversation `json:"conversation"`
	UserMessage      *conversation.Message      `json:"user_message"`
	AssistantMessage *conversation.Message      `json:"assistant_message"`
	Run              *workflow.Run              `json:"run"`
	Output           map[string]any             `json:"output"`
}

func (s *Service) CreateWorkflowConversation(ctx context.Context, ownerID, workflowID int64, req CreateWorkflowConversationRequest) (*conversation.Conversation, error) {
	if s.conversations == nil || ownerID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "新会话"
	}
	item := &conversation.Conversation{OwnerID: ownerID, WorkflowID: &workflowID, Title: title, Source: conversation.SourceWorkflow}
	if err := s.conversations.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListWorkflowConversations(ctx context.Context, ownerID, workflowID int64) ([]conversation.Conversation, error) {
	if s.conversations == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	return s.conversations.ListByWorkflow(ctx, ownerID, workflowID)
}

func (s *Service) GetWorkflowConversation(ctx context.Context, ownerID, workflowID, conversationID int64) (*conversation.Conversation, error) {
	if s.conversations == nil || conversationID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.GetWorkflow(ctx, ownerID, workflowID); err != nil {
		return nil, err
	}
	item, err := s.conversations.FindByID(ctx, ownerID, conversationID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if item.Source != conversation.SourceWorkflow || item.WorkflowID == nil || *item.WorkflowID != workflowID {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

func (s *Service) ListWorkflowConversationMessages(ctx context.Context, ownerID, workflowID, conversationID int64) ([]conversation.Message, error) {
	if _, err := s.GetWorkflowConversation(ctx, ownerID, workflowID, conversationID); err != nil {
		return nil, err
	}
	return s.messages.ListByConversation(ctx, ownerID, conversationID)
}

func (s *Service) DeleteWorkflowConversation(ctx context.Context, ownerID, workflowID, conversationID int64) error {
	if _, err := s.GetWorkflowConversation(ctx, ownerID, workflowID, conversationID); err != nil {
		return err
	}
	return s.conversations.SoftDelete(ctx, ownerID, conversationID)
}

func (s *Service) StreamWorkflowMessage(
	ctx context.Context,
	ownerID, workflowID, conversationID int64,
	req WorkflowMessageRequest,
	emit func(runtimeevent.Event) error,
) (*WorkflowMessageResponse, error) {
	conv, err := s.GetWorkflowConversation(ctx, ownerID, workflowID, conversationID)
	if err != nil {
		return nil, err
	}
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	userMessage := &conversation.Message{
		OwnerID: ownerID, ConversationID: conversationID, Role: conversation.RoleUser,
		Content: question, ContentType: conversation.ContentTypeText, TokenCount: workflowMessageTokenEstimate(question),
	}
	if err := s.messages.Create(ctx, userMessage); err != nil {
		return nil, err
	}
	input := make(map[string]any, len(req.Input)+1)
	for key, value := range req.Input {
		input[key] = value
	}
	if _, exists := input["query"]; !exists {
		input["query"] = question
	}
	run, output, err := s.StreamRunWorkflow(ctx, ownerID, workflowID, RunWorkflowRequest{
		FlowVersionID: req.FlowVersionID, ConversationID: &conversationID, Input: input,
	}, emit)
	if err != nil {
		return nil, err
	}
	assistantMessage, err := s.ensureWorkflowAssistantMessage(ctx, ownerID, conversationID, run.ID, output)
	if err != nil {
		return nil, err
	}
	return &WorkflowMessageResponse{
		Conversation: conv, UserMessage: userMessage, AssistantMessage: assistantMessage,
		Run: run, Output: map[string]any(output),
	}, nil
}

func (s *Service) ensureWorkflowAssistantMessage(ctx context.Context, ownerID, conversationID, runID int64, output map[string]any) (*conversation.Message, error) {
	messages, err := s.messages.ListByRun(ctx, ownerID, runID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == conversation.RoleAssistant {
			return &messages[i], nil
		}
	}
	content := strings.TrimSpace(outputText(output))
	message := &conversation.Message{
		OwnerID: ownerID, ConversationID: conversationID, Role: conversation.RoleAssistant,
		Content: content, ContentType: conversation.ContentTypeText, RunID: &runID, TokenCount: workflowMessageTokenEstimate(content),
	}
	if err := s.messages.Create(ctx, message); err != nil {
		return nil, err
	}
	return message, nil
}

func workflowMessageTokenEstimate(content string) int {
	count := utf8.RuneCountInString(content)
	if count == 0 {
		return 0
	}
	return (count + 3) / 4
}
