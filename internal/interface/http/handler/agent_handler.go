package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentusecase "agentcanvas/internal/application/agent_usecase"
	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	workspacedomain "agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/interface/http/sse"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/response"
	runtimeevent "agentcanvas/internal/runtime/event"

	"github.com/gin-gonic/gin"
)

type AgentHandler struct {
	service     *agentusecase.Service
	improvement *agentusecase.ImprovementService
	workspace   *workspaceusecase.Service
}

// runDTO is the public execution view. Definition snapshots remain server-side.
type runDTO struct {
	ID              int64                      `json:"id"`
	OwnerID         int64                      `json:"owner_id"`
	AgentID         int64                      `json:"agent_id"`
	ConversationID  *int64                     `json:"conversation_id,omitempty"`
	WorkspaceID     *int64                     `json:"workspace_id,omitempty"`
	Workspace       *workspacedomain.Workspace `json:"workspace,omitempty"`
	ParentRunID     *int64                     `json:"parent_run_id,omitempty"`
	RunType         string                     `json:"run_type"`
	DelegationDepth int                        `json:"delegation_depth"`
	DefinitionHash  string                     `json:"definition_hash,omitempty"`
	RuleHash        string                     `json:"rule_hash,omitempty"`
	Status          string                     `json:"status"`
	InputJSON       json.RawMessage            `json:"input_json"`
	OutputJSON      json.RawMessage            `json:"output_json"`
	ErrorMessage    string                     `json:"error_message"`
	TotalTokens     int                        `json:"total_tokens"`
	LatencyMS       int                        `json:"latency_ms"`
	StartedAt       time.Time                  `json:"started_at"`
	FinishedAt      *time.Time                 `json:"finished_at,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       time.Time                  `json:"updated_at"`
}

func (h *AgentHandler) publicRun(ctx *gin.Context, item *agentdomain.Run) *runDTO {
	dto := publicRun(item)
	if dto == nil || h.workspace == nil || item == nil || item.WorkspaceID == nil {
		return dto
	}
	if workspace, err := h.workspace.GetWorkspace(ctx.Request.Context(), item.OwnerID, *item.WorkspaceID); err == nil {
		if workspace.Kind == workspacedomain.KindShared && workspace.RunID != item.ID {
			view := *workspace
			view.RunID = item.ID
			workspace = &view
		}
		dto.Workspace = workspace
	}
	return dto
}

func publicRun(item *agentdomain.Run) *runDTO {
	if item == nil {
		return nil
	}
	return &runDTO{ID: item.ID, OwnerID: item.OwnerID, AgentID: item.AgentID,
		ConversationID: item.ConversationID, WorkspaceID: item.WorkspaceID, ParentRunID: item.ParentRunID, RunType: item.RunType,
		DelegationDepth: item.DelegationDepth, DefinitionHash: item.DefinitionHash, RuleHash: item.RuleHash,
		Status: item.Status, InputJSON: item.InputJSON, OutputJSON: item.OutputJSON, ErrorMessage: item.ErrorMessage,
		TotalTokens: item.TotalTokens, LatencyMS: item.LatencyMS, StartedAt: item.StartedAt, FinishedAt: item.FinishedAt,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

type turnAcceptedDTO struct {
	Turn        *agentdomain.Turn     `json:"turn"`
	Run         *runDTO               `json:"run"`
	UserMessage *conversation.Message `json:"user_message"`
}

func NewAgentHandler(service *agentusecase.Service, improvement ...*agentusecase.ImprovementService) *AgentHandler {
	handler := &AgentHandler{service: service}
	if len(improvement) > 0 {
		handler.improvement = improvement[0]
	}
	return handler
}

func (h *AgentHandler) ConfigureWorkspace(service *workspaceusecase.Service) { h.workspace = service }

func workspaceEventPayload(item *workspacedomain.Workspace) map[string]any {
	return map[string]any{"workspace_id": item.ID, "run_id": item.RunID, "project_id": item.ProjectID, "kind": item.Kind, "repository_root": item.RepositoryRoot, "workspace_path": item.WorkspacePath, "branch_name": item.BranchName, "base_sha": item.BaseSHA, "head_sha": item.HeadSHA, "dirty": item.Dirty, "has_unpushed_commits": item.HasUnpushedCommits, "status": item.Status, "locked": item.Locked, "lock_reason": item.LockReason, "cleanup_reason": item.CleanupReason, "error_message": item.ErrorMessage}
}

func (h *AgentHandler) workspaceForRun(c *gin.Context, ownerID, runID int64) (*workspacedomain.Workspace, error) {
	run, err := h.service.GetRun(c.Request.Context(), ownerID, runID)
	if err != nil {
		return nil, err
	}
	if run.WorkspaceID != nil {
		item, itemErr := h.workspace.GetWorkspace(c.Request.Context(), ownerID, *run.WorkspaceID)
		if itemErr == nil && item.Kind == workspacedomain.KindShared && item.RunID != runID {
			view := *item
			view.RunID = runID
			return &view, nil
		}
		return item, itemErr
	}
	return h.workspace.GetRunWorkspace(c.Request.Context(), ownerID, runID)
}

func (h *AgentHandler) Create(c *gin.Context) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return
	}
	var req agentusecase.CreateAgentRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.CreateAgent(c.Request.Context(), ownerID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) List(c *gin.Context) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return
	}
	items, err := h.service.ListAgents(c.Request.Context(), ownerID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) Get(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetAgent(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) Update(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.UpdateAgentRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.UpdateAgent(c.Request.Context(), ownerID, id, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) UpdateSettings(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var settings agentusecase.AgentEditableSettings
	if err := bindStrictAgentJSON(c, &settings); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.UpdateAgentSettings(c.Request.Context(), ownerID, id, settings)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) Delete(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteAgent(c.Request.Context(), ownerID, id); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *AgentHandler) Validate(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	result, err := h.service.ValidateAgent(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, result)
}

func (h *AgentHandler) CreateConversation(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var req agentusecase.CreateConversationRequest
	if c.Request.ContentLength > 0 {
		if err := bindStrictAgentJSON(c, &req); err != nil {
			writeAppError(c, agenterrors.ErrInvalidInput)
			return
		}
	}
	item, err := h.service.CreateConversation(c.Request.Context(), ownerID, agentID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) UpdateConversationMode(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	var req agentusecase.UpdateConversationModeRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.UpdateConversationMode(c.Request.Context(), ownerID, agentID, conversationID, req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) ListConversations(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListConversations(c.Request.Context(), ownerID, agentID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ListMessages(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	items, err := h.service.ListMessages(c.Request.Context(), ownerID, agentID, conversationID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) DeleteConversation(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	if err := h.service.DeleteConversation(c.Request.Context(), ownerID, agentID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"success": true})
}

func (h *AgentHandler) ForkConversation(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	deferGoalContinuation, _ := strconv.ParseBool(strings.TrimSpace(c.Query("defer_goal_continuation")))
	if c.Request.Body != nil && c.Request.ContentLength > 0 {
		var body struct {
			DeferGoalContinuation bool `json:"defer_goal_continuation"`
		}
		if err := bindStrictAgentJSON(c, &body); err != nil {
			writeAppError(c, agenterrors.ErrInvalidInput)
			return
		}
		deferGoalContinuation = deferGoalContinuation || body.DeferGoalContinuation
	}
	item, err := h.service.ForkConversationWithOptions(c.Request.Context(), ownerID, agentID, conversationID, deferGoalContinuation)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) StartTurn(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	var req agentusecase.CreateTurnRequest
	if err := bindStrictAgentJSON(c, &req); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	accepted, err := h.service.StartTurn(c.Request.Context(), ownerID, agentID, conversationID, c.GetHeader("Idempotency-Key"), req)
	if err != nil {
		writeAppError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, response.Body{
		Code:    0,
		Message: http.StatusText(http.StatusAccepted),
		Data: turnAcceptedDTO{
			Turn:        accepted.Turn,
			Run:         h.publicRun(c, accepted.Run),
			UserMessage: accepted.UserMessage,
		},
	})
}

func (h *AgentHandler) CompactConversation(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	accepted, err := h.service.StartTurn(c.Request.Context(), ownerID, agentID, conversationID, c.GetHeader("Idempotency-Key"), agentusecase.CreateTurnRequest{Content: "/compact", ManualCompaction: true})
	if err != nil {
		writeAppError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, response.Body{Code: 0, Message: http.StatusText(http.StatusAccepted), Data: turnAcceptedDTO{Turn: accepted.Turn, Run: h.publicRun(c, accepted.Run), UserMessage: accepted.UserMessage}})
}

func bindStrictAgentJSON(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func (h *AgentHandler) GetTurn(c *gin.Context) {
	ownerID, id, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetTurn(c.Request.Context(), ownerID, id)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) GetLatestTurn(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	item, err := h.service.GetLatestTurn(c.Request.Context(), ownerID, agentID, conversationID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) SearchSessions(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	request := conversation.MessageSearchRequest{Query: strings.TrimSpace(c.Query("q"))}
	if value, err := strconv.ParseInt(strings.TrimSpace(c.Query("conversation_id")), 10, 64); err == nil && value > 0 {
		request.ConversationID = &value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(c.Query("limit"))); err == nil {
		request.Limit = value
	}
	if value, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("from"))); err == nil {
		request.From = &value
	}
	if value, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("to"))); err == nil {
		request.To = &value
	}
	items, err := h.service.SearchSessions(c.Request.Context(), ownerID, agentID, request)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ListImprovementReviews(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.improvement == nil {
		writeAppError(c, agenterrors.ErrNotFound)
		return
	}
	items, err := h.improvement.ListReviews(c.Request.Context(), ownerID, agentID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ListChangeProposals(c *gin.Context) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.improvement == nil {
		writeAppError(c, agenterrors.ErrNotFound)
		return
	}
	items, err := h.improvement.ListProposals(c.Request.Context(), ownerID, agentID, strings.TrimSpace(c.Query("status")))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ApproveChangeProposal(c *gin.Context) { h.decideChangeProposal(c, true) }
func (h *AgentHandler) RejectChangeProposal(c *gin.Context)  { h.decideChangeProposal(c, false) }
func (h *AgentHandler) decideChangeProposal(c *gin.Context, approved bool) {
	ownerID, proposalID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.improvement == nil {
		writeAppError(c, agenterrors.ErrNotFound)
		return
	}
	var request struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&request)
	item, err := h.improvement.DecideProposal(c.Request.Context(), ownerID, proposalID, approved, request.Note)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) StreamRunEvents(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	afterID, _ := strconv.ParseInt(strings.TrimSpace(c.Query("after_id")), 10, 64)
	if headerID, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader("Last-Event-ID")), 10, 64); err == nil && headerID > afterID {
		afterID = headerID
	}
	if _, err := h.service.GetRun(c.Request.Context(), ownerID, runID); err != nil {
		writeAppError(c, err)
		return
	}
	writer := sse.NewWriter(c)
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := h.service.ListRunEvents(c.Request.Context(), ownerID, runID, afterID)
		if err != nil {
			_ = writer.Event("error", gin.H{"message": err.Error()})
			return
		}
		for _, item := range items {
			if err := writer.EventWithID(item.ID, item.EventType, item); err != nil {
				return
			}
			afterID = item.ID
		}
		run, err := h.service.GetRun(c.Request.Context(), ownerID, runID)
		if err != nil {
			return
		}
		if run.Status != "queued" && run.Status != "running" && run.Status != "resuming" {
			_ = writer.Event("run_status", h.publicRun(c, run))
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// StreamRunEventsV1 consumes the per-run in-memory hub. The legacy polling
// endpoint remains available while the frontend migrates to the typed v1
// reducer and terminal snapshot protocol.
func (h *AgentHandler) StreamRunEventsV1(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	afterSeq, _ := strconv.ParseUint(strings.TrimSpace(c.Query("after_seq")), 10, 64)
	if headerSeq, err := strconv.ParseUint(strings.TrimSpace(c.GetHeader("Last-Event-ID")), 10, 64); err == nil && headerSeq > afterSeq {
		afterSeq = headerSeq
	}
	replay, live, cancel, err := h.service.SubscribeRunStream(c.Request.Context(), ownerID, runID, afterSeq)
	if err != nil {
		writeAppError(c, err)
		return
	}
	defer cancel()
	writer := sse.NewWriter(c)
	for _, event := range replay {
		if err := writer.EventWithID(int64(event.Seq), event.Kind, event); err != nil {
			return
		}
	}
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, open := <-live:
			if !open {
				return
			}
			if err := writer.EventWithID(int64(event.Seq), event.Kind, event); err != nil {
				return
			}
		}
	}
}

func (h *AgentHandler) GetRun(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.GetRun(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, h.publicRun(c, item))
}

func (h *AgentHandler) ListRunEvents(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	afterID, _ := strconv.ParseInt(strings.TrimSpace(c.Query("after_id")), 10, 64)
	items, err := h.service.ListRunEvents(c.Request.Context(), ownerID, runID, afterID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) ListChildRuns(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListChildRuns(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	public := make([]*runDTO, 0, len(items))
	for index := range items {
		public = append(public, h.publicRun(c, &items[index]))
	}
	response.OK(c, public)
}

func (h *AgentHandler) ListRunSteps(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListRunSteps(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) GetRunTrace(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	run, err := h.service.GetRun(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	events, err := h.service.ListRunEvents(c.Request.Context(), ownerID, runID, 0)
	if err != nil {
		writeAppError(c, err)
		return
	}
	steps, err := h.service.ListRunSteps(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	children, err := h.service.ListChildRuns(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	publicChildren := make([]*runDTO, 0, len(children))
	for index := range children {
		publicChildren = append(publicChildren, h.publicRun(c, &children[index]))
	}
	response.OK(c, gin.H{"run": h.publicRun(c, run), "events": events, "steps": steps, "children": publicChildren})
}

func (h *AgentHandler) CancelRun(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if err := h.service.CancelRun(c.Request.Context(), ownerID, runID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"cancelled": true})
}

func (h *AgentHandler) ResumeRun(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.ResumeByID(c.Request.Context(), ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) GetRunWorkspace(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.workspace == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.workspaceForRun(c, ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) RunGitStatus(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.workspace == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.workspaceForRun(c, ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	value, err := h.workspace.GitStatus(c.Request.Context(), item)
	if err != nil {
		payload := workspaceEventPayload(item)
		payload["dirty"], payload["has_unpushed_commits"], payload["error_message"] = true, true, err.Error()
		_ = h.service.EmitWorkspaceEvent(c.Request.Context(), ownerID, runID, runtimeevent.GitStatusChanged, payload)
		writeAppError(c, err)
		return
	}
	payload := workspaceEventPayload(item)
	payload["branch_name"], payload["dirty"], payload["has_unpushed_commits"] = value.Branch, value.Dirty, value.HasUnpushedCommits
	payload["head_sha"] = value.Head
	_ = h.service.EmitWorkspaceEvent(c.Request.Context(), ownerID, runID, runtimeevent.GitStatusChanged, payload)
	response.OK(c, value)
}

func (h *AgentHandler) RunGitDiff(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.workspace == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.workspaceForRun(c, ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	value, err := h.workspace.GitDiff(c.Request.Context(), item, c.Query("staged") == "true")
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"diff": value})
}

func (h *AgentHandler) RunGitLog(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.workspace == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.workspaceForRun(c, ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	value, err := h.workspace.GitLog(c.Request.Context(), item, limit)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"log": value})
}

func (h *AgentHandler) RunGitCommit(c *gin.Context) {
	ownerID, runID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.workspace == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	var body struct {
		Message string   `json:"message"`
		Paths   []string `json:"paths"`
	}
	if err := bindStrictAgentJSON(c, &body); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.workspaceForRun(c, ownerID, runID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	value, err := h.workspace.Commit(c.Request.Context(), item, body.Message, body.Paths)
	if err != nil {
		writeAppError(c, err)
		return
	}
	payload := workspaceEventPayload(item)
	payload["message"], payload["paths"], payload["hash"], payload["head_sha"] = value.Message, value.Paths, value.Hash, value.Hash
	if item.Kind == workspacedomain.KindWorktree {
		payload["has_unpushed_commits"] = true
	}
	_ = h.service.EmitWorkspaceEvent(c.Request.Context(), ownerID, runID, runtimeevent.GitCommitCreated, payload)
	response.OK(c, value)
}

func (h *AgentHandler) CleanupWorkspace(c *gin.Context) {
	ownerID, workspaceID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.workspace == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.workspace.GetWorkspace(c.Request.Context(), ownerID, workspaceID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	force := c.Query("force") == "true"
	value, err := h.workspace.CleanupRunWorkspace(c.Request.Context(), ownerID, item.RunID, force)
	if err != nil {
		if value != nil {
			payload := workspaceEventPayload(value)
			payload["error_message"] = err.Error()
			eventType := runtimeevent.WorkspacePreserved
			if value.Status == workspacedomain.StatusCleaned {
				eventType = runtimeevent.WorkspaceCleaned
			}
			_ = h.service.EmitWorkspaceEvent(c.Request.Context(), ownerID, value.RunID, eventType, payload)
		}
		writeAppError(c, err)
		return
	}
	eventType := runtimeevent.WorkspaceStatusChanged
	if value.Status == "preserved" {
		eventType = runtimeevent.WorkspacePreserved
	} else if value.Status == "cleaned" {
		eventType = runtimeevent.WorkspaceCleaned
	}
	_ = h.service.EmitWorkspaceEvent(c.Request.Context(), ownerID, value.RunID, eventType, workspaceEventPayload(value))
	response.OK(c, value)
}

func (h *AgentHandler) RefreshWorkspace(c *gin.Context) {
	ownerID, workspaceID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	if h.workspace == nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.workspace.GetWorkspace(c.Request.Context(), ownerID, workspaceID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	value, err := h.workspace.RefreshGitStatus(c.Request.Context(), item)
	if err != nil {
		payload := workspaceEventPayload(item)
		payload["error_message"] = err.Error()
		_ = h.service.EmitWorkspaceEvent(c.Request.Context(), ownerID, item.RunID, runtimeevent.WorkspaceStatusChanged, payload)
		writeAppError(c, err)
		return
	}
	_ = h.service.EmitWorkspaceEvent(c.Request.Context(), ownerID, value.RunID, runtimeevent.WorkspaceStatusChanged, workspaceEventPayload(value))
	response.OK(c, value)
}

func (h *AgentHandler) ListApprovalRequests(c *gin.Context) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return
	}
	items, err := h.service.ListApprovalRequests(c.Request.Context(), ownerID, c.Query("status"))
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, items)
}

func (h *AgentHandler) GetGoal(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	if _, err := h.service.GetConversation(c.Request.Context(), ownerID, agentID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	item, err := h.service.GetGoal(c.Request.Context(), ownerID, conversationID)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) SetGoal(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	if _, err := h.service.GetConversation(c.Request.Context(), ownerID, agentID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	var request agentusecase.GoalUpdateRequest
	if err := bindStrictAgentJSON(c, &request); err != nil {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return
	}
	item, err := h.service.SetGoal(c.Request.Context(), ownerID, conversationID, request)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, item)
}

func (h *AgentHandler) ClearGoal(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	if _, err := h.service.GetConversation(c.Request.Context(), ownerID, agentID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	if err := h.service.ClearGoal(c.Request.Context(), ownerID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, gin.H{"cleared": true})
}

func (h *AgentHandler) StreamGoalEvents(c *gin.Context) {
	ownerID, agentID, conversationID, ok := agentConversationIDs(c)
	if !ok {
		return
	}
	if _, err := h.service.GetConversation(c.Request.Context(), ownerID, agentID, conversationID); err != nil {
		writeAppError(c, err)
		return
	}
	afterSeq, _ := strconv.ParseUint(strings.TrimSpace(c.Query("after_seq")), 10, 64)
	if headerSeq, err := strconv.ParseUint(strings.TrimSpace(c.GetHeader("Last-Event-ID")), 10, 64); err == nil && headerSeq > afterSeq {
		afterSeq = headerSeq
	}
	item, replay, live, cancel, err := h.service.SubscribeGoalStream(c.Request.Context(), ownerID, conversationID, afterSeq)
	if err != nil {
		writeAppError(c, err)
		return
	}
	defer cancel()
	writer := sse.NewWriter(c)
	if err := writer.Event("goal.snapshot", gin.H{"conversation_id": conversationID, "goal": item}); err != nil {
		return
	}
	for _, event := range replay {
		if err := writer.EventWithID(int64(event.Seq), event.Kind, event); err != nil {
			return
		}
	}
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, open := <-live:
			if !open {
				return
			}
			if err := writer.EventWithID(int64(event.Seq), event.Kind, event); err != nil {
				return
			}
		}
	}
}

func (h *AgentHandler) ApproveRequest(c *gin.Context) { h.decideApproval(c, true) }
func (h *AgentHandler) RejectRequest(c *gin.Context)  { h.decideApproval(c, false) }

func (h *AgentHandler) decideApproval(c *gin.Context, approved bool) {
	ownerID, requestID, ok := ownerAndID(c, "id")
	if !ok {
		return
	}
	var body struct {
		Note    string            `json:"note"`
		Answers map[string]string `json:"answers"`
	}
	if c.Request.Body != nil {
		if err := c.ShouldBindJSON(&body); err != nil && err != io.EOF {
			writeAppError(c, agenterrors.ErrInvalidInput)
			return
		}
	}
	run, err := h.service.DecideApprovalRequestWithAnswers(c.Request.Context(), ownerID, requestID, approved, body.Note, body.Answers)
	if err != nil {
		writeAppError(c, err)
		return
	}
	response.OK(c, h.publicRun(c, run))
}

func requireOwner(c *gin.Context) (int64, bool) {
	ownerID, ok := currentUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
	}
	return ownerID, ok
}

func ownerAndID(c *gin.Context, key string) (int64, int64, bool) {
	ownerID, ok := requireOwner(c)
	if !ok {
		return 0, 0, false
	}
	id, err := parseInt64Param(c, key)
	if err != nil || id <= 0 {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, false
	}
	return ownerID, id, true
}

func agentConversationIDs(c *gin.Context) (int64, int64, int64, bool) {
	ownerID, agentID, ok := ownerAndID(c, "id")
	if !ok {
		return 0, 0, 0, false
	}
	conversationID, err := parseInt64Param(c, "conversation_id")
	if err != nil || conversationID <= 0 {
		writeAppError(c, agenterrors.ErrInvalidInput)
		return 0, 0, 0, false
	}
	return ownerID, agentID, conversationID, true
}
