package chat_usecase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"agentcanvas/internal/application/memory_usecase"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/dialog"
	"agentcanvas/internal/domain/knowledge"
	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/usage"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/runtime/conversationcontext"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	defaultTopK        = 8
	maxTopK            = 20
	maxHistoryMessages = 30
)

type Service struct {
	providers          providerdomain.Repository
	dialogs            dialog.Repository
	kbs                knowledge.KnowledgeBaseRepository
	conversations      conversation.Repository
	messages           conversation.MessageRepository
	usages             usage.Repository
	retriever          retrieval.Retriever
	llm                llm.ChatClient
	secrets            *cryptoinfra.SecretBox
	packer             *ContextPacker
	prompts            *PromptBuilder
	dreamQueue         queueinfra.JobQueue
	redisClient        *redis.Client
	dreamCfg           memory_usecase.DreamConfig
	contextCoordinator *conversationcontext.Coordinator
}

type ChatRequest struct {
	ProviderID                 int64   `json:"provider_id" binding:"required"`
	KBIDs                      []int64 `json:"kb_ids" binding:"required"`
	Question                   string  `json:"question" binding:"required"`
	ConversationID             int64   `json:"conversation_id"`
	Model                      string  `json:"model"`
	TopK                       int     `json:"top_k"`
	ContextWindowTokens        int     `json:"context_window_tokens"`
	ReservedOutputTokens       int     `json:"reserved_output_tokens"`
	ContextSafetyMarginTokens  int     `json:"context_safety_margin_tokens"`
	ModelAutoCompactTokenLimit int     `json:"model_auto_compact_token_limit"`
	CompactPrompt              string  `json:"compact_prompt"`
}

type ChatResponse struct {
	Conversation       *conversation.Conversation      `json:"conversation"`
	UserMessage        *conversation.Message           `json:"user_message"`
	AssistantMessage   *conversation.Message           `json:"assistant_message"`
	References         []conversation.MessageReference `json:"references"`
	Usage              llm.Usage                       `json:"usage"`
	RetrievalLatencyMS int                             `json:"retrieval_latency_ms"`
	QueryPlan          *retrieval.QueryPlan            `json:"query_plan,omitempty"`
	Clarification      *retrieval.Clarification        `json:"clarification,omitempty"`
	Compaction         *conversation.Compaction        `json:"compaction,omitempty"`
}

type StreamEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func NewService(
	providers providerdomain.Repository,
	dialogs dialog.Repository,
	kbs knowledge.KnowledgeBaseRepository,
	conversations conversation.Repository,
	messages conversation.MessageRepository,
	usages usage.Repository,
	retriever retrieval.Retriever,
	llmClient llm.ChatClient,
	secrets *cryptoinfra.SecretBox,
) *Service {
	return &Service{
		providers:     providers,
		dialogs:       dialogs,
		kbs:           kbs,
		conversations: conversations,
		messages:      messages,
		usages:        usages,
		retriever:     retriever,
		llm:           llmClient,
		secrets:       secrets,
		packer:        NewContextPacker(),
		prompts:       NewPromptBuilder(),
	}
}

func (s *Service) ConfigureDream(queue queueinfra.JobQueue, redisClient *redis.Client, dreamCfg memory_usecase.DreamConfig) {
	s.dreamQueue = queue
	s.redisClient = redisClient
	s.dreamCfg = dreamCfg
}

func (s *Service) Chat(ctx context.Context, ownerID, dialogID int64, req ChatRequest) (*ChatResponse, error) {
	ctx = llm.WithOwnerID(ctx, ownerID)
	prepared, err := s.prepare(ctx, ownerID, dialogID, req)
	if err != nil {
		return nil, err
	}
	if prepared.clarification != nil && prepared.clarification.Required {
		assistantMessage, _, saveErr := s.saveAssistant(ctx, ownerID, prepared.conversation.ID, prepared.clarification.Question, nil, 0)
		if saveErr != nil {
			return nil, saveErr
		}
		return &ChatResponse{Conversation: prepared.conversation, UserMessage: prepared.userMessage, AssistantMessage: assistantMessage, QueryPlan: prepared.queryPlan, Clarification: prepared.clarification, RetrievalLatencyMS: prepared.retrievalLatencyMS}, nil
	}
	started := time.Now()
	resp, err := s.llm.Chat(ctx, prepared.providerConfig, prepared.llmRequest)
	latencyMS := int(time.Since(started).Milliseconds())
	if err != nil {
		_ = s.writeUsage(ctx, ownerID, prepared.provider, prepared.model, llm.Usage{}, latencyMS, false, err.Error())
		return nil, err
	}
	assistantMessage, refs, err := s.saveAssistant(ctx, ownerID, prepared.conversation.ID, resp.Content, prepared.packed.References, resp.Usage.CompletionTokens)
	if err != nil {
		return nil, err
	}
	totalUsage := chatAddUsage(prepared.compactionUsage, resp.Usage)
	_ = s.writeUsage(ctx, ownerID, prepared.provider, prepared.model, totalUsage, latencyMS, true, "")
	return &ChatResponse{
		Conversation:       prepared.conversation,
		UserMessage:        prepared.userMessage,
		AssistantMessage:   assistantMessage,
		References:         refs,
		Usage:              totalUsage,
		RetrievalLatencyMS: prepared.retrievalLatencyMS,
		QueryPlan:          prepared.queryPlan,
		Compaction:         prepared.compaction,
	}, nil
}

func (s *Service) StreamChat(ctx context.Context, ownerID, dialogID int64, req ChatRequest, emit func(StreamEvent) error) error {
	ctx = llm.WithOwnerID(ctx, ownerID)
	prepared, err := s.prepare(ctx, ownerID, dialogID, req)
	if err != nil {
		return err
	}
	if err := emit(StreamEvent{Type: "conversation", Data: prepared.conversation}); err != nil {
		return err
	}
	if err := emit(StreamEvent{Type: "user_message", Data: prepared.userMessage}); err != nil {
		return err
	}
	if prepared.clarification != nil && prepared.clarification.Required {
		if err := emit(StreamEvent{Type: "clarification_required", Data: prepared.clarification}); err != nil {
			return err
		}
		assistantMessage, _, saveErr := s.saveAssistant(ctx, ownerID, prepared.conversation.ID, prepared.clarification.Question, nil, 0)
		if saveErr != nil {
			return saveErr
		}
		return emit(StreamEvent{Type: "done", Data: ChatResponse{Conversation: prepared.conversation, UserMessage: prepared.userMessage, AssistantMessage: assistantMessage, QueryPlan: prepared.queryPlan, Clarification: prepared.clarification, RetrievalLatencyMS: prepared.retrievalLatencyMS}})
	}
	if err := emit(StreamEvent{Type: "retrieval", Data: map[string]any{"references": prepared.packed.References, "latency_ms": prepared.retrievalLatencyMS}}); err != nil {
		return err
	}

	started := time.Now()
	content := strings.Builder{}
	usageData := llm.Usage{}
	err = s.llm.StreamChat(ctx, prepared.providerConfig, prepared.llmRequest, func(event llm.StreamEvent) error {
		if event.Delta != "" {
			content.WriteString(event.Delta)
			return emit(StreamEvent{Type: "delta", Data: map[string]string{"content": event.Delta}})
		}
		if event.Usage.TotalTokens > 0 || event.Usage.PromptTokens > 0 || event.Usage.CompletionTokens > 0 {
			usageData = event.Usage
		}
		return nil
	})
	latencyMS := int(time.Since(started).Milliseconds())
	if err != nil {
		_ = s.writeUsage(ctx, ownerID, prepared.provider, prepared.model, usageData, latencyMS, false, err.Error())
		return err
	}
	assistantMessage, refs, err := s.saveAssistant(ctx, ownerID, prepared.conversation.ID, content.String(), prepared.packed.References, usageData.CompletionTokens)
	if err != nil {
		return err
	}
	totalUsage := chatAddUsage(prepared.compactionUsage, usageData)
	_ = s.writeUsage(ctx, ownerID, prepared.provider, prepared.model, totalUsage, latencyMS, true, "")
	return emit(StreamEvent{Type: "done", Data: ChatResponse{
		Conversation:       prepared.conversation,
		UserMessage:        prepared.userMessage,
		AssistantMessage:   assistantMessage,
		References:         refs,
		Usage:              totalUsage,
		RetrievalLatencyMS: prepared.retrievalLatencyMS,
		QueryPlan:          prepared.queryPlan,
		Compaction:         prepared.compaction,
	}})
}

func (s *Service) ListConversations(ctx context.Context, ownerID, dialogID int64) ([]conversation.Conversation, error) {
	if _, err := s.getDialog(ctx, ownerID, dialogID); err != nil {
		return nil, err
	}
	return s.conversations.ListByDialog(ctx, ownerID, dialogID)
}

func (s *Service) GetConversation(ctx context.Context, ownerID, dialogID, id int64) (*conversation.Conversation, error) {
	if _, err := s.getDialog(ctx, ownerID, dialogID); err != nil {
		return nil, err
	}
	item, err := s.conversations.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if !conversationInDialog(item, dialogID) {
		return nil, agenterrors.ErrNotFound
	}
	return item, nil
}

func (s *Service) ListMessages(ctx context.Context, ownerID, dialogID, conversationID int64) ([]conversation.Message, error) {
	if _, err := s.GetConversation(ctx, ownerID, dialogID, conversationID); err != nil {
		return nil, err
	}
	return s.messages.ListByConversation(ctx, ownerID, conversationID)
}

func (s *Service) DeleteConversation(ctx context.Context, ownerID, dialogID, id int64) error {
	if _, err := s.GetConversation(ctx, ownerID, dialogID, id); err != nil {
		return err
	}
	s.publishDream(ctx, ownerID, id)
	return s.conversations.SoftDelete(ctx, ownerID, id)
}

func (s *Service) publishDream(ctx context.Context, ownerID, conversationID int64) {
	if s.dreamQueue == nil || !s.dreamCfg.Enabled || ownerID <= 0 || conversationID <= 0 {
		return
	}
	if s.redisClient != nil {
		key := "dream:pending:" + strconv.FormatInt(conversationID, 10)
		locked, err := s.redisClient.SetNX(ctx, key, 1, time.Minute).Result()
		if err != nil || !locked {
			return
		}
	}
	_ = s.dreamQueue.Publish(ctx, queueinfra.Job{ID: fmt.Sprintf("dream-%d-%d-%d", ownerID, conversationID, time.Now().UnixNano()), Type: memory_usecase.DreamJobType, Payload: map[string]any{"owner_id": ownerID, "conversation_id": conversationID}})
}

type preparedChat struct {
	provider           *providerdomain.ModelProvider
	providerConfig     llm.ChatProviderConfig
	model              string
	conversation       *conversation.Conversation
	userMessage        *conversation.Message
	packed             PackedContext
	llmRequest         llm.ChatRequest
	retrievalLatencyMS int
	queryPlan          *retrieval.QueryPlan
	clarification      *retrieval.Clarification
	compaction         *conversation.Compaction
	compactionUsage    llm.Usage
}

func (s *Service) prepare(ctx context.Context, ownerID, dialogID int64, req ChatRequest) (*preparedChat, error) {
	question := strings.TrimSpace(req.Question)
	topK := req.TopK
	if topK == 0 {
		topK = defaultTopK
	}
	if dialogID <= 0 || req.ProviderID <= 0 || len(req.KBIDs) == 0 || question == "" || topK <= 0 || topK > maxTopK {
		return nil, agenterrors.ErrInvalidInput
	}
	if _, err := s.getDialog(ctx, ownerID, dialogID); err != nil {
		return nil, err
	}
	provider, model, providerConfig, err := s.providerConfig(ctx, ownerID, req.ProviderID, req.Model)
	if err != nil {
		return nil, err
	}
	retrievalMode := retrieval.ModeKeyword
	for i, kbID := range req.KBIDs {
		kb, err := s.kbs.FindByID(ctx, ownerID, kbID)
		if err != nil {
			return nil, mapNotFound(err)
		}
		if kb.Status != knowledge.KnowledgeBaseStatusActive {
			return nil, agenterrors.ErrInvalidInput
		}
		if i == 0 && kb.RetrievalMode != "" {
			retrievalMode = retrieval.Mode(kb.RetrievalMode)
		}
	}
	conv, err := s.ensureConversation(ctx, ownerID, dialogID, req.ConversationID, question)
	if err != nil {
		return nil, err
	}
	userMessage := &conversation.Message{
		OwnerID:        ownerID,
		ConversationID: conv.ID,
		Role:           conversation.RoleUser,
		Content:        question,
		ContentType:    conversation.ContentTypeText,
		TokenCount:     estimateTokens(question),
	}
	if err := s.messages.Create(ctx, userMessage); err != nil {
		return nil, err
	}
	_ = s.conversations.UpdateLastMessageAt(ctx, ownerID, conv.ID)
	history, err := s.messages.ListByConversation(ctx, ownerID, conv.ID)
	if err != nil {
		return nil, err
	}
	queryTurns := make([]retrieval.QueryTurn, 0, len(history))
	for _, item := range history {
		if item.ID == userMessage.ID || strings.TrimSpace(item.Content) == "" {
			continue
		}
		queryTurns = append(queryTurns, retrieval.QueryTurn{Role: item.Role, Content: item.Content})
	}

	retrievalResp, err := s.retriever.Search(ctx, retrieval.RetrievalRequest{
		OwnerID:           ownerID,
		KBIDs:             req.KBIDs,
		Query:             question,
		Conversation:      queryTurns,
		RewriteProviderID: req.ProviderID,
		RewriteModel:      model,
		TopK:              topK,
		Mode:              retrievalMode,
		EnableHighlight:   true,
	})
	if err != nil {
		return nil, err
	}
	packed := s.packer.Pack(ownerID, retrievalResp.Results)
	prompt := s.prompts.Build(question, packed.Text)
	messages, compaction, compactionUsage, compactErr := s.prepareChatMessages(ctx, ownerID, conv.ID, req.ProviderID, model, providerConfig, req, prompt, question)
	if compactErr != nil {
		return nil, compactErr
	}
	return &preparedChat{
		provider:           provider,
		providerConfig:     providerConfig,
		model:              model,
		conversation:       conv,
		userMessage:        userMessage,
		packed:             packed,
		retrievalLatencyMS: retrievalResp.LatencyMS,
		queryPlan:          retrievalResp.QueryPlan,
		clarification:      retrievalResp.Clarification,
		compaction:         compaction,
		compactionUsage:    compactionUsage,
		llmRequest: llm.ChatRequest{
			Model:    model,
			Messages: messages,
		},
	}, nil
}

func validChatRole(role string) bool {
	switch role {
	case conversation.RoleSystem, conversation.RoleUser, conversation.RoleAssistant, conversation.RoleTool:
		return true
	default:
		return false
	}
}

func (s *Service) providerConfig(
	ctx context.Context,
	ownerID, providerID int64,
	requestedModel string,
) (*providerdomain.ModelProvider, string, llm.ChatProviderConfig, error) {
	provider, err := s.providers.FindByID(ctx, ownerID, providerID)
	if err != nil {
		return nil, "", llm.ChatProviderConfig{}, mapNotFound(err)
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, "", llm.ChatProviderConfig{}, agenterrors.ErrInvalidInput
	}
	model := strings.TrimSpace(requestedModel)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultChatModel)
	}
	if model == "" {
		return nil, "", llm.ChatProviderConfig{}, agenterrors.ErrInvalidInput
	}
	apiKey, err := s.secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, "", llm.ChatProviderConfig{}, err
	}
	return provider, model, llm.ChatProviderConfig{ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey}, nil
}

func (s *Service) getDialog(ctx context.Context, ownerID, dialogID int64) (*dialog.Dialog, error) {
	if dialogID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	item, err := s.dialogs.FindByID(ctx, ownerID, dialogID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return item, nil
}

func (s *Service) ensureConversation(
	ctx context.Context,
	ownerID, dialogID,
	conversationID int64,
	question string,
) (*conversation.Conversation, error) {
	if conversationID > 0 {
		item, err := s.conversations.FindByID(ctx, ownerID, conversationID)
		if err != nil {
			return nil, mapNotFound(err)
		}
		if !conversationInDialog(item, dialogID) {
			return nil, agenterrors.ErrNotFound
		}
		return item, nil
	}
	title := question
	if utf8.RuneCountInString(title) > 40 {
		title = string([]rune(title)[:40])
	}
	item := &conversation.Conversation{OwnerID: ownerID, DialogID: &dialogID, Title: title, Source: conversation.SourceRAGChat}
	if err := s.conversations.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func conversationInDialog(item *conversation.Conversation, dialogID int64) bool {
	return item != nil && item.DialogID != nil && *item.DialogID == dialogID
}

func (s *Service) saveAssistant(
	ctx context.Context,
	ownerID, conversationID int64,
	content string,
	references []conversation.MessageReference,
	tokenCount int,
) (*conversation.Message, []conversation.MessageReference, error) {
	message := &conversation.Message{
		OwnerID:        ownerID,
		ConversationID: conversationID,
		Role:           conversation.RoleAssistant,
		Content:        content,
		ContentType:    conversation.ContentTypeText,
		TokenCount:     tokenCount,
	}
	if message.TokenCount == 0 {
		message.TokenCount = estimateTokens(content)
	}
	if err := s.messages.Create(ctx, message); err != nil {
		return nil, nil, err
	}
	for i := range references {
		references[i].MessageID = message.ID
	}
	if err := s.messages.CreateReferences(ctx, references); err != nil {
		return nil, nil, err
	}
	_ = s.conversations.UpdateLastMessageAt(ctx, ownerID, conversationID)
	return message, references, nil
}

func (s *Service) writeUsage(
	ctx context.Context,
	ownerID int64,
	provider *providerdomain.ModelProvider,
	model string,
	usageData llm.Usage,
	latencyMS int,
	success bool,
	message string,
) error {
	return s.usages.Create(ctx, &usage.ModelUsageLog{
		OwnerID:          ownerID,
		ProviderID:       provider.ID,
		ProviderType:     provider.ProviderType,
		ModelName:        model,
		UsageType:        usage.TypeChat,
		PromptTokens:     usageData.PromptTokens,
		CompletionTokens: usageData.CompletionTokens,
		TotalTokens:      usageData.TotalTokens,
		LatencyMS:        latencyMS,
		Success:          success,
		ErrorMessage:     message,
	})
}

func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return agenterrors.ErrNotFound
	}
	return err
}
