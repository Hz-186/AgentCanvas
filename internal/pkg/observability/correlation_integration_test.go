package observability_test

// End-to-end correlation integration suite (Task 5 of
// observability-correlation-tracing). It drives the real HTTP middleware
// chain, the real StartTurn metadata codec, the durable turn worker, the
// subagent child-run codec, and the real runtime Runner diagnostics seam,
// using only in-memory repositories and scripted executors.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentusecase "agentcanvas/internal/application/agent_usecase"
	authusecase "agentcanvas/internal/application/auth_usecase"
	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	conversationdomain "agentcanvas/internal/domain/conversation"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	httpserver "agentcanvas/internal/interface/http"
	"agentcanvas/internal/interface/http/handler"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/logger"
	"agentcanvas/internal/pkg/observability"
	runtimeagent "agentcanvas/internal/runtime/agent"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/toolruntime"

	"github.com/gin-gonic/gin"
)

const (
	integrationOwnerID        = int64(7)
	integrationAgentID        = int64(2)
	integrationConversationID = int64(10)
)

// diagnosticsAttributeWhitelist mirrors the privacy baseline enforced by
// logger.DiagnosticsHandler; the negative assertions use it as reference.
var diagnosticsAttributeWhitelist = map[string]struct{}{
	"event": {}, "phase": {}, "result": {},
	"request_id": {}, "owner_id": {}, "conversation_id": {}, "run_id": {},
	"turn_id": {}, "parent_run_id": {}, "step_index": {}, "tool_call_id": {},
	"route": {}, "status": {}, "provider": {}, "model": {}, "tool_name": {},
	"error_class": {}, "latency_ms": {}, "usage": {}, "error_summary": {},
}

type capturedDiagnostic struct {
	msg   string
	attrs map[string]any
}

// captureHandler is the capturing slog.Handler seam used by every layer of
// the integration suite.
type captureHandler struct {
	mu      sync.Mutex
	records []capturedDiagnostic
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		attr.Value = attr.Value.Resolve()
		if _, exists := attrs[attr.Key]; !exists {
			attrs[attr.Key] = attr.Value.Any()
		}
		return true
	})
	h.mu.Lock()
	h.records = append(h.records, capturedDiagnostic{msg: record.Message, attrs: attrs})
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) snapshot() []capturedDiagnostic {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]capturedDiagnostic(nil), h.records...)
}

func (h *captureHandler) eventsNamed(name string) []capturedDiagnostic {
	return eventsNamedIn(h.snapshot(), name)
}

func eventsNamedIn(events []capturedDiagnostic, name string) []capturedDiagnostic {
	matched := make([]capturedDiagnostic, 0)
	for _, event := range events {
		if event.attrs["event"] == name {
			matched = append(matched, event)
		}
	}
	return matched
}

// correlationStores is the shared durable-state fake backing every in-memory
// repository adapter used by the integration suite.
type correlationStores struct {
	mu            sync.Mutex
	nextID        int64
	agents        map[int64]*agentdomain.Agent
	conversations map[int64]*conversationdomain.Conversation
	messages      []*conversationdomain.Message
	runs          map[int64]*agentdomain.Run
	turns         map[int64]*agentdomain.Turn
	events        []*agentdomain.RunEvent
	steps         []*agentdomain.RunStep
	claimed       bool
}

func newCorrelationStores() *correlationStores {
	return &correlationStores{
		nextID:        1000,
		agents:        map[int64]*agentdomain.Agent{},
		conversations: map[int64]*conversationdomain.Conversation{},
		runs:          map[int64]*agentdomain.Run{},
		turns:         map[int64]*agentdomain.Turn{},
	}
}

func (s *correlationStores) assignID() int64 {
	s.nextID++
	return s.nextID
}

func (s *correlationStores) runCopy(id int64) *agentdomain.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return nil
	}
	copy := *run
	return &copy
}

func (s *correlationStores) turnCopy(id int64) *agentdomain.Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn := s.turns[id]
	if turn == nil {
		return nil
	}
	copy := *turn
	return &copy
}

func (s *correlationStores) onlyRun(t *testing.T) *agentdomain.Run {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) != 1 {
		t.Fatalf("runs in store = %d, want exactly 1", len(s.runs))
	}
	for _, run := range s.runs {
		copy := *run
		return &copy
	}
	return nil
}

func (s *correlationStores) onlyTurn(t *testing.T) *agentdomain.Turn {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.turns) != 1 {
		t.Fatalf("turns in store = %d, want exactly 1", len(s.turns))
	}
	for _, turn := range s.turns {
		copy := *turn
		return &copy
	}
	return nil
}

func (s *correlationStores) eventsSnapshot() []agentdomain.RunEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]agentdomain.RunEvent, 0, len(s.events))
	for _, event := range s.events {
		items = append(items, *event)
	}
	return items
}

func (s *correlationStores) durableCounts() (runs, turns, events, steps, messages int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs), len(s.turns), len(s.events), len(s.steps), len(s.messages)
}

func (s *correlationStores) seedRun(run *agentdomain.Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.ID == 0 {
		run.ID = s.assignID()
	}
	copy := *run
	s.runs[run.ID] = &copy
}

func (s *correlationStores) seedEvent(runID int64, eventType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, &agentdomain.RunEvent{
		ImmutableModel: domain.ImmutableModel{OwnerID: integrationOwnerID, CreatedAt: time.Now().UTC()},
		RunID:          runID,
		EventType:      eventType,
		PayloadJSON:    json.RawMessage(`{"seeded":true}`),
	})
}

func (s *correlationStores) seedStep(runID int64, stepIndex int, stepType, toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, &agentdomain.RunStep{
		ImmutableModel: domain.ImmutableModel{OwnerID: integrationOwnerID, CreatedAt: time.Now().UTC()},
		RunID:          runID,
		StepIndex:      stepIndex,
		StepType:       stepType,
		ToolName:       toolName,
	})
}

// seedRunAndTurn links a directly created Run/Turn pair the way legacy
// records (without the observability namespace) would already exist.
func (s *correlationStores) seedRunAndTurn(run *agentdomain.Run, turn *agentdomain.Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.ID == 0 {
		run.ID = s.assignID()
	}
	if turn.ID == 0 {
		turn.ID = s.assignID()
	}
	turn.RunID = &run.ID
	runCopy, turnCopy := *run, *turn
	s.runs[run.ID] = &runCopy
	s.turns[turn.ID] = &turnCopy
}

type storesAgentRepo struct{ st *correlationStores }

func (r *storesAgentRepo) Create(_ context.Context, item *agentdomain.Agent) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.st.assignID()
	}
	copy := *item
	r.st.agents[item.ID] = &copy
	return nil
}

func (r *storesAgentRepo) ListByOwner(context.Context, int64) ([]agentdomain.Agent, error) {
	return nil, nil
}

func (r *storesAgentRepo) FindByID(_ context.Context, ownerID, id int64) (*agentdomain.Agent, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	item := r.st.agents[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	copy := *item
	return &copy, nil
}

func (r *storesAgentRepo) Update(_ context.Context, item *agentdomain.Agent) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	copy := *item
	r.st.agents[item.ID] = &copy
	return nil
}

func (r *storesAgentRepo) SoftDelete(context.Context, int64, int64) error { return nil }

type storesConversationRepo struct{ st *correlationStores }

func (r *storesConversationRepo) Create(_ context.Context, item *conversationdomain.Conversation) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.st.assignID()
	}
	copy := *item
	r.st.conversations[item.ID] = &copy
	return nil
}

func (r *storesConversationRepo) ListByOwner(context.Context, int64) ([]conversationdomain.Conversation, error) {
	return nil, nil
}

func (r *storesConversationRepo) FindByID(_ context.Context, ownerID, id int64) (*conversationdomain.Conversation, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	item := r.st.conversations[id]
	if item == nil || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	copy := *item
	return &copy, nil
}

func (r *storesConversationRepo) Update(_ context.Context, item *conversationdomain.Conversation) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	copy := *item
	r.st.conversations[item.ID] = &copy
	return nil
}

func (r *storesConversationRepo) UpdateLastMessageAt(context.Context, int64, int64) error { return nil }

func (r *storesConversationRepo) SoftDelete(context.Context, int64, int64) error { return nil }

func (r *storesConversationRepo) ListByAgent(context.Context, int64, int64) ([]conversationdomain.Conversation, error) {
	return nil, nil
}

func (r *storesConversationRepo) UpdateAgentMode(context.Context, int64, int64, string) error {
	return nil
}

type storesMessageRepo struct{ st *correlationStores }

func (r *storesMessageRepo) Create(_ context.Context, message *conversationdomain.Message) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if message.ID == 0 {
		message.ID = r.st.assignID()
	}
	copy := *message
	r.st.messages = append(r.st.messages, &copy)
	return nil
}

func (r *storesMessageRepo) ListByConversation(_ context.Context, ownerID, conversationID int64) ([]conversationdomain.Message, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	items := make([]conversationdomain.Message, 0)
	for _, message := range r.st.messages {
		if message.OwnerID == ownerID && message.ConversationID == conversationID {
			items = append(items, *message)
		}
	}
	return items, nil
}

func (r *storesMessageRepo) ListActiveByConversation(ctx context.Context, ownerID, conversationID int64) ([]conversationdomain.Message, error) {
	return r.ListByConversation(ctx, ownerID, conversationID)
}

func (r *storesMessageRepo) ListByRun(_ context.Context, ownerID, runID int64) ([]conversationdomain.Message, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	items := make([]conversationdomain.Message, 0)
	for _, message := range r.st.messages {
		if message.OwnerID == ownerID && message.RunID != nil && *message.RunID == runID {
			items = append(items, *message)
		}
	}
	return items, nil
}

func (r *storesMessageRepo) ListThroughIncludingArchived(context.Context, int64, int64, int64, int64) ([]conversationdomain.Message, error) {
	return nil, nil
}

type storesRunRepo struct{ st *correlationStores }

func (r *storesRunRepo) Create(_ context.Context, item *agentdomain.Run) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.st.assignID()
	}
	copy := *item
	r.st.runs[item.ID] = &copy
	return nil
}

func (r *storesRunRepo) FindByID(_ context.Context, ownerID, runID int64) (*agentdomain.Run, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	run := r.st.runs[runID]
	if run == nil || run.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	copy := *run
	return &copy, nil
}

func (r *storesRunRepo) ListByParent(_ context.Context, ownerID, parentRunID int64) ([]agentdomain.Run, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	items := make([]agentdomain.Run, 0)
	for _, run := range r.st.runs {
		if run.OwnerID == ownerID && run.ParentRunID != nil && *run.ParentRunID == parentRunID {
			items = append(items, *run)
		}
	}
	return items, nil
}

func (r *storesRunRepo) Update(_ context.Context, item *agentdomain.Run) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	copy := *item
	r.st.runs[item.ID] = &copy
	return nil
}

func (r *storesRunRepo) CancelActive(context.Context, *agentdomain.Run, time.Time) (bool, error) {
	return false, nil
}

type storesEventRepo struct{ st *correlationStores }

func (r *storesEventRepo) Create(_ context.Context, item *agentdomain.RunEvent) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.st.assignID()
	}
	copy := *item
	r.st.events = append(r.st.events, &copy)
	return nil
}

func (r *storesEventRepo) ListByRun(_ context.Context, ownerID, runID int64) ([]agentdomain.RunEvent, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	items := make([]agentdomain.RunEvent, 0)
	for _, event := range r.st.events {
		if event.OwnerID == ownerID && event.RunID == runID {
			items = append(items, *event)
		}
	}
	return items, nil
}

type storesStepRepo struct{ st *correlationStores }

func (r *storesStepRepo) Create(_ context.Context, item *agentdomain.RunStep) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.st.assignID()
	}
	copy := *item
	r.st.steps = append(r.st.steps, &copy)
	return nil
}

func (r *storesStepRepo) ListByRun(_ context.Context, ownerID, runID int64) ([]agentdomain.RunStep, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	items := make([]agentdomain.RunStep, 0)
	for _, step := range r.st.steps {
		if step.OwnerID == ownerID && step.RunID == runID {
			items = append(items, *step)
		}
	}
	return items, nil
}

type storesTurnRepo struct{ st *correlationStores }

func (r *storesTurnRepo) Create(_ context.Context, item *agentdomain.Turn) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if item.ID == 0 {
		item.ID = r.st.assignID()
	}
	copy := *item
	r.st.turns[item.ID] = &copy
	return nil
}

func (r *storesTurnRepo) CreateWithArtifacts(_ context.Context, item *agentdomain.Turn, userMessage *conversationdomain.Message, run *agentdomain.Run) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if userMessage.ID == 0 {
		userMessage.ID = r.st.assignID()
	}
	if run.ID == 0 {
		run.ID = r.st.assignID()
	}
	if item.ID == 0 {
		item.ID = r.st.assignID()
	}
	item.UserMessageID = userMessage.ID
	item.RunID = &run.ID
	messageCopy, runCopy, turnCopy := *userMessage, *run, *item
	r.st.messages = append(r.st.messages, &messageCopy)
	r.st.runs[run.ID] = &runCopy
	r.st.turns[item.ID] = &turnCopy
	return nil
}

func (r *storesTurnRepo) CompleteWithMessage(_ context.Context, item *agentdomain.Turn, assistantMessage *conversationdomain.Message, run *agentdomain.Run) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if assistantMessage.ID == 0 {
		assistantMessage.ID = r.st.assignID()
	}
	item.AssistantMessageID = &assistantMessage.ID
	messageCopy, runCopy, turnCopy := *assistantMessage, *run, *item
	r.st.messages = append(r.st.messages, &messageCopy)
	r.st.runs[run.ID] = &runCopy
	r.st.turns[item.ID] = &turnCopy
	return nil
}

func (r *storesTurnRepo) UpdateRunOwned(_ context.Context, item *agentdomain.Turn, run *agentdomain.Run, _ bool) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	runCopy, turnCopy := *run, *item
	r.st.runs[run.ID] = &runCopy
	r.st.turns[item.ID] = &turnCopy
	return nil
}

func (r *storesTurnRepo) FindByID(_ context.Context, ownerID, id int64) (*agentdomain.Turn, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	turn := r.st.turns[id]
	if turn == nil || turn.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	copy := *turn
	return &copy, nil
}

func (r *storesTurnRepo) FindByIdempotencyKey(_ context.Context, ownerID, conversationID int64, key string) (*agentdomain.Turn, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	for _, turn := range r.st.turns {
		if turn.OwnerID == ownerID && turn.ConversationID == conversationID && turn.IdempotencyKey == key {
			copy := *turn
			return &copy, nil
		}
	}
	return nil, agenterrors.ErrNotFound
}

func (r *storesTurnRepo) FindByRunID(_ context.Context, ownerID, runID int64) (*agentdomain.Turn, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	for _, turn := range r.st.turns {
		if turn.OwnerID == ownerID && turn.RunID != nil && *turn.RunID == runID {
			copy := *turn
			return &copy, nil
		}
	}
	return nil, agenterrors.ErrNotFound
}

func (r *storesTurnRepo) FindLatestByConversation(context.Context, int64, int64, int64) (*agentdomain.Turn, error) {
	return nil, agenterrors.ErrNotFound
}

func (r *storesTurnRepo) Update(_ context.Context, item *agentdomain.Turn) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	copy := *item
	r.st.turns[item.ID] = &copy
	return nil
}

func (r *storesTurnRepo) CancelByRun(context.Context, int64, int64, time.Time) (*agentdomain.Turn, error) {
	return nil, agenterrors.ErrNotFound
}

func (r *storesTurnRepo) ListQueued(context.Context, int) ([]agentdomain.Turn, error) {
	return nil, nil
}

func (r *storesTurnRepo) ClaimNext(_ context.Context, workerID, leaseToken string, _ time.Time) (*agentdomain.Turn, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if r.st.claimed {
		return nil, agentdomain.ErrNoTurnAvailable
	}
	for _, turn := range r.st.turns {
		if turn.Status != agentdomain.TurnStatusQueued {
			continue
		}
		// Leave LeaseToken empty: the worker only starts a lease heartbeat
		// for claimed turns that carry one.
		turn.Status = agentdomain.TurnStatusRunning
		turn.WorkerID = workerID
		r.st.claimed = true
		copy := *turn
		return &copy, nil
	}
	return nil, agentdomain.ErrNoTurnAvailable
}

func (r *storesTurnRepo) RenewLease(context.Context, int64, string, time.Time) error {
	return nil
}

func (r *storesTurnRepo) ListExpiredRunning(context.Context, time.Time, int) ([]agentdomain.Turn, error) {
	return nil, nil
}

func (r *storesTurnRepo) RecoverExpired(context.Context, *agentdomain.Turn, *agentdomain.Run) error {
	return nil
}

// correlationCapturingRuntime is the scripted executor for full turn flows.
type correlationCapturingRuntime struct {
	mu           sync.Mutex
	emitEvents   bool
	correlations []observability.Correlation
}

func (r *correlationCapturingRuntime) Execute(ctx context.Context, req agentruntime.RunRequest, emit agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	if correlation, ok := observability.CorrelationFromContext(ctx); ok {
		r.mu.Lock()
		r.correlations = append(r.correlations, correlation)
		r.mu.Unlock()
	}
	if r.emitEvents && emit != nil {
		_ = emit.Emit(ctx, runtimeevent.Event{Type: runtimeevent.AgentStarted, RunID: req.RunID})
		defer func() {
			_ = emit.Emit(ctx, runtimeevent.Event{Type: runtimeevent.AgentFinished, RunID: req.RunID})
		}()
	}
	return &agentruntime.RunResult{Output: agentruntime.RunOutput{
		"final_answer": "integration-ok",
		"stop_reason":  runtimeagent.StopReasonFinalAnswer,
		"total_tokens": 7,
	}}, nil
}

func (r *correlationCapturingRuntime) Resume(ctx context.Context, req agentruntime.ResumeRequest, emit agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	return r.Execute(ctx, req.RunRequest, emit)
}

func (r *correlationCapturingRuntime) correlationsSnapshot() []observability.Correlation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observability.Correlation(nil), r.correlations...)
}

// runnerBackedRuntime wires the real runtimeagent.Runner behind the
// agentruntime.Runtime seam so subagent/turn flows exercise real tool
// diagnostics.
type runnerBackedRuntime struct {
	model     string
	tools     []toolruntime.RuntimeTool
	responses []llm.ToolChatResponse
	logger    *slog.Logger

	mu    sync.Mutex
	steps []runtimeagent.RunStep
}

func (r *runnerBackedRuntime) Execute(ctx context.Context, req agentruntime.RunRequest, emit agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	// Derive the child-run correlation the same way the durable turn worker
	// does (turn_worker.go:85-91): durable IDs come from the request, the
	// request ID survives from the parent correlation.
	correlation, _ := observability.CorrelationFromContext(ctx)
	correlation = correlation.WithOwnerID(req.OwnerID).WithRunID(req.RunID).WithParentRunID(req.ParentRunID)
	if req.ConversationID != nil {
		correlation = correlation.WithConversationID(*req.ConversationID)
	}
	ctx = observability.WithCorrelation(ctx, correlation)
	if emit != nil {
		_ = emit.Emit(ctx, runtimeevent.Event{Type: runtimeevent.AgentStarted, RunID: req.RunID})
	}
	runner := &runtimeagent.Runner{
		LLM:    &scriptedToolClient{responses: append([]llm.ToolChatResponse(nil), r.responses...)},
		Logger: r.logger,
	}
	result, err := runner.Run(ctx, runtimeagent.RunRequest{
		OwnerID:        req.OwnerID,
		RunID:          req.RunID,
		ConversationID: req.ConversationID,
		Model:          r.model,
		Task:           req.Task,
		MaxIterations:  4,
		MaxToolCalls:   4,
		Tools:          r.tools,
	})
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.steps = append(r.steps, result.Steps...)
	r.mu.Unlock()
	if emit != nil {
		_ = emit.Emit(ctx, runtimeevent.Event{Type: runtimeevent.AgentFinished, RunID: req.RunID})
	}
	return &agentruntime.RunResult{Output: agentruntime.RunOutput{
		"final_answer": result.FinalAnswer,
		"stop_reason":  result.StopReason,
	}}, nil
}

func (r *runnerBackedRuntime) Resume(ctx context.Context, req agentruntime.ResumeRequest, emit agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	return r.Execute(ctx, req.RunRequest, emit)
}

func (r *runnerBackedRuntime) stepsSnapshot() []runtimeagent.RunStep {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtimeagent.RunStep(nil), r.steps...)
}

// scriptedToolClient replays canned model responses for the Runner.
type scriptedToolClient struct {
	responses []llm.ToolChatResponse
}

func (c *scriptedToolClient) ChatWithTools(context.Context, llm.ChatProviderConfig, llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	if len(c.responses) == 0 {
		return &llm.ToolChatResponse{Message: llm.ChatMessage{Role: conversationdomain.RoleAssistant, Content: "done"}}, nil
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return &response, nil
}

// recordingTool is a minimal successful tool that keeps its arguments for
// assertions.
type recordingTool struct {
	name   string
	output string

	mu     sync.Mutex
	inputs []json.RawMessage
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Description() string { return "integration recording tool" }
func (t *recordingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
}

func (t *recordingTool) Execute(_ context.Context, _ toolruntime.ToolRunContext, input json.RawMessage) (*toolruntime.ToolResult, error) {
	t.mu.Lock()
	t.inputs = append(t.inputs, append(json.RawMessage(nil), input...))
	t.mu.Unlock()
	return &toolruntime.ToolResult{ContentText: t.output, ContentJSON: json.RawMessage(`{"ok":true}`)}, nil
}

// traceCountingRuns wraps the run repository to count trace API queries.
type traceCountingRuns struct {
	inner             *storesRunRepo
	mu                sync.Mutex
	findByIDCalls     int
	listByParentCalls int
}

func (r *traceCountingRuns) Create(ctx context.Context, item *agentdomain.Run) error {
	return r.inner.Create(ctx, item)
}

func (r *traceCountingRuns) FindByID(ctx context.Context, ownerID, runID int64) (*agentdomain.Run, error) {
	r.mu.Lock()
	r.findByIDCalls++
	r.mu.Unlock()
	return r.inner.FindByID(ctx, ownerID, runID)
}

func (r *traceCountingRuns) ListByParent(ctx context.Context, ownerID, parentRunID int64) ([]agentdomain.Run, error) {
	r.mu.Lock()
	r.listByParentCalls++
	r.mu.Unlock()
	return r.inner.ListByParent(ctx, ownerID, parentRunID)
}

func (r *traceCountingRuns) Update(ctx context.Context, item *agentdomain.Run) error {
	return r.inner.Update(ctx, item)
}

func (r *traceCountingRuns) CancelActive(ctx context.Context, item *agentdomain.Run, at time.Time) (bool, error) {
	return r.inner.CancelActive(ctx, item, at)
}

// traceCountingEvents wraps the run-event repository to count trace queries.
type traceCountingEvents struct {
	inner          *storesEventRepo
	mu             sync.Mutex
	listByRunCalls int
}

func (r *traceCountingEvents) Create(ctx context.Context, item *agentdomain.RunEvent) error {
	return r.inner.Create(ctx, item)
}

func (r *traceCountingEvents) ListByRun(ctx context.Context, ownerID, runID int64) ([]agentdomain.RunEvent, error) {
	r.mu.Lock()
	r.listByRunCalls++
	r.mu.Unlock()
	return r.inner.ListByRun(ctx, ownerID, runID)
}

// traceCountingSteps wraps the run-step repository to count trace queries.
type traceCountingSteps struct {
	inner          *storesStepRepo
	mu             sync.Mutex
	listByRunCalls int
}

func (r *traceCountingSteps) Create(ctx context.Context, item *agentdomain.RunStep) error {
	return r.inner.Create(ctx, item)
}

func (r *traceCountingSteps) ListByRun(ctx context.Context, ownerID, runID int64) ([]agentdomain.RunStep, error) {
	r.mu.Lock()
	r.listByRunCalls++
	r.mu.Unlock()
	return r.inner.ListByRun(ctx, ownerID, runID)
}

type integrationResponseBody struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func integrationAgentDefinition() agentdomain.Definition {
	return agentdomain.Definition{
		ModelConfig:     agentdomain.ModelConfig{ProviderID: 1, Model: "integration-model"},
		ExecutionLimits: agentdomain.ExecutionLimits{Mode: "default"},
	}
}

func integrationAgentDefinitionJSON(t *testing.T) json.RawMessage {
	t.Helper()
	raw, _, err := integrationAgentDefinition().Snapshot()
	if err != nil {
		t.Fatalf("snapshot integration agent definition: %v", err)
	}
	return raw
}

func seedIntegrationWorld() *correlationStores {
	st := newCorrelationStores()
	st.agents[integrationAgentID] = &agentdomain.Agent{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: integrationAgentID, OwnerID: integrationOwnerID}},
		Name:            "integration-agent",
		Status:          "active",
		DraftDefinition: integrationAgentDefinition(),
	}
	agentID := integrationAgentID
	st.conversations[integrationConversationID] = &conversationdomain.Conversation{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: integrationConversationID, OwnerID: integrationOwnerID}},
		Title:           "integration conversation",
		AgentID:         &agentID,
		AgentMode:       "default",
	}
	return st
}

func integrationToken(t *testing.T) (string, *authusecase.Service) {
	t.Helper()
	jwt := cryptoinfra.NewJWTService("correlation-integration-secret", time.Hour)
	token, _, err := jwt.IssueAccessToken(integrationOwnerID)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}
	authService := authusecase.NewService(nil, nil, nil, nil, nil, nil, jwt, cryptoinfra.NewTokenHasher("test"), nil, nil, time.Hour)
	return token, authService
}

func newIntegrationRouter(t *testing.T, capture *captureHandler, service *agentusecase.Service, authService *authusecase.Service) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return httpserver.NewRouter(httpserver.RouterDeps{
		Logger:        slog.New(capture),
		HealthHandler: handler.NewHealthHandler(nil),
		AgentHandler:  handler.NewAgentHandler(service),
		AuthService:   authService,
	})
}

func postStartTurn(t *testing.T, router http.Handler, token, requestID, idempotencyKey, content string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		t.Fatalf("marshal StartTurn payload: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/agents/%d/conversations/%d/turns", integrationAgentID, integrationConversationID),
		strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	if requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// swapDefaultDiagnostics points slog.Default at the DiagnosticsHandler seam
// so service/runtime diagnostics that fall back to the default logger are
// captured by the suite.
func swapDefaultDiagnostics(t *testing.T, capture *captureHandler) {
	t.Helper()
	previous := slog.Default()
	slog.SetDefault(slog.New(logger.NewDiagnosticsHandler(capture)))
	t.Cleanup(func() { slog.SetDefault(previous) })
}

func runWorkerUntilTerminal(t *testing.T, service *agentusecase.Service, st *correlationStores, turnID int64) *agentdomain.Turn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.RunWorker(ctx, "integration-worker", 1)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		turn := st.turnCopy(turnID)
		if turn != nil {
			switch turn.Status {
			case agentdomain.TurnStatusSucceeded, agentdomain.TurnStatusFailed, agentdomain.TurnStatusCancelled:
				return turn
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("turn %d did not reach a terminal status before the deadline", turnID)
	return nil
}

func assertObservabilityMetadata(t *testing.T, label string, inputJSON json.RawMessage, requestID string) {
	t.Helper()
	var input map[string]any
	if err := json.Unmarshal(inputJSON, &input); err != nil {
		t.Fatalf("decode %s input JSON %s: %v", label, inputJSON, err)
	}
	metadata, ok := input["observability"].(map[string]any)
	if !ok {
		t.Fatalf("%s input has no observability namespace: %s", label, inputJSON)
	}
	if metadata["version"] != float64(1) || metadata["request_id"] != requestID ||
		metadata["owner_id"] != float64(integrationOwnerID) ||
		metadata["conversation_id"] != float64(integrationConversationID) {
		t.Fatalf("%s observability metadata = %#v, want version 1 with request_id %s", label, metadata, requestID)
	}
}

type CorrelationIntegrationTest struct{}

func TestCorrelationIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	suite := &CorrelationIntegrationTest{}
	t.Run("shouldLinkHTTPStartTurnWorkerAndRuntime", suite.shouldLinkHTTPStartTurnWorkerAndRuntime)
	t.Run("shouldLinkParentRunAndToolStep", suite.shouldLinkParentRunAndToolStep)
	t.Run("shouldKeepTraceAPIShape", suite.shouldKeepTraceAPIShape)
	t.Run("shouldRejectSensitiveLogAttributes", suite.shouldRejectSensitiveLogAttributes)
	t.Run("shouldRemainCompatibleWithLegacyRun", suite.shouldRemainCompatibleWithLegacyRun)
}

func (s *CorrelationIntegrationTest) shouldLinkHTTPStartTurnWorkerAndRuntime(t *testing.T) {
	routerCapture := &captureHandler{}
	diagnosticCapture := &captureHandler{}
	swapDefaultDiagnostics(t, diagnosticCapture)
	st := seedIntegrationWorld()
	executor := &correlationCapturingRuntime{emitEvents: true}
	service := agentusecase.NewService(
		&storesAgentRepo{st}, &storesTurnRepo{st}, &storesConversationRepo{st}, &storesMessageRepo{st},
		&storesRunRepo{st}, &storesEventRepo{st}, &storesStepRepo{st}, nil, executor)
	token, authService := integrationToken(t)
	router := newIntegrationRouter(t, routerCapture, service, authService)

	const requestID = "rid-integration-1"
	recorder := postStartTurn(t, router, token, requestID, "idem-integration-1", "hello correlation")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("StartTurn status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusAccepted)
	}

	access := routerCapture.eventsNamed("http.access")
	if len(access) != 1 {
		t.Fatalf("http.access records = %d, want 1", len(access))
	}
	if access[0].attrs["request_id"] != requestID || access[0].attrs["owner_id"] != integrationOwnerID ||
		access[0].attrs["status"] != int64(http.StatusAccepted) ||
		access[0].attrs["route"] != "/api/v1/agents/:id/conversations/:conversation_id/turns" {
		t.Fatalf("http.access attrs = %#v", access[0].attrs)
	}

	run := st.onlyRun(t)
	turn := st.onlyTurn(t)
	assertObservabilityMetadata(t, "run", run.InputJSON, requestID)
	assertObservabilityMetadata(t, "turn", turn.InputJSON, requestID)

	terminal := runWorkerUntilTerminal(t, service, st, turn.ID)
	if terminal.Status != agentdomain.TurnStatusSucceeded {
		t.Fatalf("turn status = %q error=%q, want succeeded", terminal.Status, terminal.ErrorMessage)
	}

	started := diagnosticCapture.eventsNamed("turn.started")
	finished := diagnosticCapture.eventsNamed("turn.finished")
	if len(started) != 1 || len(finished) != 1 {
		t.Fatalf("turn lifecycle records = started %d finished %d, want 1/1", len(started), len(finished))
	}
	for _, event := range append(append([]capturedDiagnostic{}, started...), finished...) {
		if event.attrs["request_id"] != requestID || event.attrs["owner_id"] != integrationOwnerID ||
			event.attrs["conversation_id"] != integrationConversationID ||
			event.attrs["run_id"] != run.ID || event.attrs["turn_id"] != turn.ID {
			t.Fatalf("turn lifecycle correlation mismatch: %s attrs = %#v", event.msg, event.attrs)
		}
	}

	correlations := executor.correlationsSnapshot()
	if len(correlations) != 1 {
		t.Fatalf("runtime executions with captured correlation = %d, want 1", len(correlations))
	}
	got := correlations[0]
	if got.RequestID != requestID || got.OwnerID != integrationOwnerID ||
		got.ConversationID != integrationConversationID || got.RunID != run.ID || got.TurnID != turn.ID {
		t.Fatalf("runtime correlation = %#v, want request %s owner %d conversation %d run %d turn %d",
			got, requestID, integrationOwnerID, integrationConversationID, run.ID, turn.ID)
	}

	audited := diagnosticCapture.eventsNamed("run_event.audited")
	if len(audited) == 0 {
		t.Fatal("run_event.audited diagnostics missing for runtime events")
	}
	for _, event := range audited {
		if event.attrs["owner_id"] != integrationOwnerID || event.attrs["run_id"] != run.ID ||
			event.attrs["conversation_id"] != integrationConversationID {
			t.Fatalf("run_event.audited correlation mismatch: %#v", event.attrs)
		}
	}
	if persisted := st.eventsSnapshot(); len(persisted) == 0 {
		t.Fatal("runtime event was not persisted through the emitter")
	}
}

func (s *CorrelationIntegrationTest) shouldLinkParentRunAndToolStep(t *testing.T) {
	diagnosticCapture := &captureHandler{}
	st := newCorrelationStores()
	parent := &agentdomain.Run{
		BaseModel: domain.BaseModel{OwnerID: integrationOwnerID},
		AgentID:   integrationAgentID,
		RunType:   agentdomain.RunTypeTurn,
		Status:    agentdomain.RunStatusRunning,
		StartedAt: time.Now().UTC(),
	}
	st.seedRun(parent)

	const parentRequestID = "rid-child-parent-1"
	const toolCallID = "call_child_1"
	tool := &recordingTool{name: "lookup", output: "child-result"}
	responses := []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversationdomain.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: toolCallID, Name: "lookup", Arguments: json.RawMessage(`{"query":"child"}`)},
		}}},
		{Message: llm.ChatMessage{Role: conversationdomain.RoleAssistant, Content: "child done"}},
	}
	executor := &runnerBackedRuntime{
		model:     "child-model",
		tools:     []toolruntime.RuntimeTool{tool},
		responses: responses,
		logger:    slog.New(logger.NewDiagnosticsHandler(diagnosticCapture)),
	}
	service := agentusecase.NewService(nil, nil, nil, nil,
		&storesRunRepo{st}, &storesEventRepo{st}, &storesStepRepo{st}, nil, executor)

	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{}.
		WithRequestID(parentRequestID).WithOwnerID(integrationOwnerID).WithRunID(parent.ID))
	result, err := service.RunSubagent(ctx, toolruntime.SubagentRequest{
		OwnerID:         integrationOwnerID,
		ParentRunID:     parent.ID,
		AgentID:         integrationAgentID,
		DelegationDepth: 0,
		MaxDepth:        2,
		Definition: toolruntime.SubagentDefinition{
			Task: "child correlation task", ProviderID: 1, Model: "child-model", Mode: "default",
			MaxIterations: 4, MaxToolCalls: 4,
		},
	})
	if err != nil {
		t.Fatalf("RunSubagent() error = %v", err)
	}
	if result.Status != agentdomain.RunStatusSucceeded {
		t.Fatalf("child run status = %q error=%q, want succeeded", result.Status, result.Error)
	}

	children, err := (&storesRunRepo{st}).ListByParent(ctx, integrationOwnerID, parent.ID)
	if err != nil || len(children) != 1 {
		t.Fatalf("ListByParent() children = %#v err=%v, want exactly one child", children, err)
	}
	child := children[0]
	if child.ParentRunID == nil || *child.ParentRunID != parent.ID {
		t.Fatalf("child run lost parent link: %#v", child)
	}
	if child.RunType != agentdomain.RunTypeSubagent || child.DelegationDepth != 1 {
		t.Fatalf("child run codec mismatch: type=%q depth=%d", child.RunType, child.DelegationDepth)
	}

	started := diagnosticCapture.eventsNamed("tool.started")
	completed := diagnosticCapture.eventsNamed("tool.completed")
	if len(started) != 1 || len(completed) != 1 {
		t.Fatalf("tool diagnostics = started %d completed %d, want 1/1", len(started), len(completed))
	}
	for _, event := range append(append([]capturedDiagnostic{}, started...), completed...) {
		if event.attrs["parent_run_id"] != parent.ID || event.attrs["run_id"] != child.ID ||
			event.attrs["owner_id"] != integrationOwnerID || event.attrs["request_id"] != parentRequestID ||
			event.attrs["tool_call_id"] != toolCallID || event.attrs["tool_name"] != "lookup" {
			t.Fatalf("tool diagnostic correlation mismatch: %s attrs = %#v", event.msg, event.attrs)
		}
	}

	toolCallStepIndex := 0
	for _, step := range executor.stepsSnapshot() {
		if step.Type == runtimeagent.StepTypeToolCall && step.ToolCallID == toolCallID {
			toolCallStepIndex = step.Index
		}
	}
	if toolCallStepIndex == 0 {
		t.Fatalf("runner did not record a tool_call step for %s", toolCallID)
	}
	if started[0].attrs["step_index"] != int64(toolCallStepIndex) {
		t.Fatalf("tool.started step_index = %v, want runner step index %d", started[0].attrs["step_index"], toolCallStepIndex)
	}
}

func (s *CorrelationIntegrationTest) shouldKeepTraceAPIShape(t *testing.T) {
	routerCapture := &captureHandler{}
	st := newCorrelationStores()
	parent := &agentdomain.Run{
		BaseModel: domain.BaseModel{OwnerID: integrationOwnerID},
		AgentID:   integrationAgentID,
		RunType:   agentdomain.RunTypeTurn,
		Status:    agentdomain.RunStatusSucceeded,
		StartedAt: time.Now().UTC(),
	}
	st.seedRun(parent)
	child := &agentdomain.Run{
		BaseModel:   domain.BaseModel{OwnerID: integrationOwnerID},
		AgentID:     integrationAgentID,
		RunType:     agentdomain.RunTypeSubagent,
		ParentRunID: &parent.ID,
		Status:      agentdomain.RunStatusSucceeded,
		StartedAt:   time.Now().UTC(),
	}
	st.seedRun(child)
	st.seedEvent(parent.ID, runtimeevent.AgentStarted)
	st.seedEvent(parent.ID, runtimeevent.AgentFinished)
	st.seedStep(parent.ID, 1, runtimeagent.StepTypeToolCall, "lookup")

	countingRuns := &traceCountingRuns{inner: &storesRunRepo{st}}
	countingEvents := &traceCountingEvents{inner: &storesEventRepo{st}}
	countingSteps := &traceCountingSteps{inner: &storesStepRepo{st}}
	service := agentusecase.NewService(nil, nil, nil, nil, countingRuns, countingEvents, countingSteps, nil, nil)
	token, authService := integrationToken(t)
	router := newIntegrationRouter(t, routerCapture, service, authService)

	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/runs/%d/trace", parent.ID), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Request-ID", "rid-trace-1")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GetRunTrace status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}

	var body integrationResponseBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode trace response %s: %v", recorder.Body.String(), err)
	}
	var trace struct {
		Run      json.RawMessage   `json:"run"`
		Events   []json.RawMessage `json:"events"`
		Steps    []json.RawMessage `json:"steps"`
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(body.Data, &trace); err != nil {
		t.Fatalf("decode trace data %s: %v", body.Data, err)
	}
	if len(trace.Run) == 0 || trace.Events == nil || trace.Steps == nil || trace.Children == nil {
		t.Fatalf("trace API lost its run/events/steps/children shape: %s", body.Data)
	}
	if len(trace.Events) != 2 || len(trace.Steps) != 1 || len(trace.Children) != 1 {
		t.Fatalf("trace payload sizes = events %d steps %d children %d, want 2/1/1",
			len(trace.Events), len(trace.Steps), len(trace.Children))
	}

	countingRuns.mu.Lock()
	findByIDCalls, listByParentCalls := countingRuns.findByIDCalls, countingRuns.listByParentCalls
	countingRuns.mu.Unlock()
	countingEvents.mu.Lock()
	eventListCalls := countingEvents.listByRunCalls
	countingEvents.mu.Unlock()
	countingSteps.mu.Lock()
	stepListCalls := countingSteps.listByRunCalls
	countingSteps.mu.Unlock()
	if findByIDCalls != 4 || listByParentCalls != 1 || eventListCalls != 1 || stepListCalls != 1 {
		t.Fatalf("trace repository call contract = FindByID %d ListByParent %d events.ListByRun %d steps.ListByRun %d, want 4/1/1/1",
			findByIDCalls, listByParentCalls, eventListCalls, stepListCalls)
	}
}

func (s *CorrelationIntegrationTest) shouldRejectSensitiveLogAttributes(t *testing.T) {
	routerCapture := &captureHandler{}
	diagnosticCapture := &captureHandler{}
	swapDefaultDiagnostics(t, diagnosticCapture)
	st := seedIntegrationWorld()

	const promptBait = "TOP_SECRET_PROMPT_BAIT_42"
	const apiKeyBait = "sk-live-BAIT-42"
	const outputBait = "SENSITIVE_TOOL_OUTPUT_BAIT_42"
	tool := &recordingTool{name: "lookup", output: "tool says " + outputBait}
	responses := []llm.ToolChatResponse{
		{Message: llm.ChatMessage{Role: conversationdomain.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "call_secret_1", Name: "lookup", Arguments: json.RawMessage(`{"api_key":"` + apiKeyBait + `"}`)},
		}}},
		{Message: llm.ChatMessage{Role: conversationdomain.RoleAssistant, Content: "done"}},
	}
	executor := &runnerBackedRuntime{
		model:     "integration-model",
		tools:     []toolruntime.RuntimeTool{tool},
		responses: responses,
		logger:    slog.New(logger.NewDiagnosticsHandler(diagnosticCapture)),
	}
	service := agentusecase.NewService(
		&storesAgentRepo{st}, &storesTurnRepo{st}, &storesConversationRepo{st}, &storesMessageRepo{st},
		&storesRunRepo{st}, &storesEventRepo{st}, &storesStepRepo{st}, nil, executor)
	token, authService := integrationToken(t)
	router := newIntegrationRouter(t, routerCapture, service, authService)

	recorder := postStartTurn(t, router, token, "rid-privacy-1", "idem-privacy-1", "Remember "+promptBait+" forever")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("StartTurn status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusAccepted)
	}
	if access := routerCapture.eventsNamed("http.access"); len(access) != 1 {
		t.Fatalf("http.access records = %d, want 1", len(access))
	}

	turn := st.onlyTurn(t)
	terminal := runWorkerUntilTerminal(t, service, st, turn.ID)
	if terminal.Status != agentdomain.TurnStatusSucceeded {
		t.Fatalf("turn status = %q error=%q, want succeeded", terminal.Status, terminal.ErrorMessage)
	}

	all := append(routerCapture.snapshot(), diagnosticCapture.snapshot()...)
	if len(all) == 0 {
		t.Fatal("no diagnostics were captured at any layer")
	}
	for _, name := range []string{"http.access", "turn.started", "turn.finished", "tool.started", "tool.completed", "run_event.audited"} {
		if len(eventsNamedIn(all, name)) == 0 {
			t.Fatalf("expected diagnostic event %q was never captured", name)
		}
	}

	banned := []string{promptBait, apiKeyBait, outputBait, "api_key", "authorization", "Authorization", "Bearer ", token, "hello correlation", "Remember"}
	for _, event := range all {
		for key := range event.attrs {
			if _, allowed := diagnosticsAttributeWhitelist[key]; !allowed {
				t.Fatalf("non-whitelisted diagnostic attribute %q in event %q", key, event.msg)
			}
		}
		payload := event.msg
		for _, value := range event.attrs {
			payload += " " + fmt.Sprintf("%v", value)
		}
		for _, needle := range banned {
			if strings.Contains(payload, needle) {
				t.Fatalf("diagnostic %q leaked sensitive content %q: %#v", event.msg, needle, event.attrs)
			}
		}
	}
}

func (s *CorrelationIntegrationTest) shouldRemainCompatibleWithLegacyRun(t *testing.T) {
	routerCapture := &captureHandler{}
	diagnosticCapture := &captureHandler{}
	swapDefaultDiagnostics(t, diagnosticCapture)
	st := seedIntegrationWorld()
	executor := &correlationCapturingRuntime{}
	service := agentusecase.NewService(
		&storesAgentRepo{st}, &storesTurnRepo{st}, &storesConversationRepo{st}, &storesMessageRepo{st},
		&storesRunRepo{st}, &storesEventRepo{st}, &storesStepRepo{st}, nil, executor)
	_, authService := integrationToken(t)
	router := newIntegrationRouter(t, routerCapture, service, authService)

	// X-Request-ID behavior at the HTTP layer must stay unchanged: explicit
	// values echo verbatim, absent values keep being generated.
	echoRequest := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	echoRequest.Header.Set("X-Request-ID", "rid-legacy-echo")
	echoRecorder := httptest.NewRecorder()
	router.ServeHTTP(echoRecorder, echoRequest)
	if echoRecorder.Code != http.StatusOK || echoRecorder.Header().Get("X-Request-ID") != "rid-legacy-echo" {
		t.Fatalf("legacy X-Request-ID echo changed: status=%d header=%q",
			echoRecorder.Code, echoRecorder.Header().Get("X-Request-ID"))
	}
	generatedRecorder := httptest.NewRecorder()
	router.ServeHTTP(generatedRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if generatedRecorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("X-Request-ID is no longer generated when the header is absent")
	}

	// Legacy Run/Turn records carry no observability namespace in their input.
	legacyInput := json.RawMessage(`{"query":"legacy task","mode":"default"}`)
	conversationID := integrationConversationID
	run := &agentdomain.Run{
		BaseModel:      domain.BaseModel{OwnerID: integrationOwnerID},
		AgentID:        integrationAgentID,
		ConversationID: &conversationID,
		RunType:        agentdomain.RunTypeTurn,
		Status:         agentdomain.RunStatusQueued,
		DefinitionJSON: integrationAgentDefinitionJSON(t),
		InputJSON:      legacyInput,
		StartedAt:      time.Now().UTC(),
	}
	turn := &agentdomain.Turn{
		BaseModel:      domain.BaseModel{OwnerID: integrationOwnerID},
		AgentID:        integrationAgentID,
		ConversationID: integrationConversationID,
		Status:         agentdomain.TurnStatusQueued,
		InputJSON:      legacyInput,
	}
	st.seedRunAndTurn(run, turn)

	beforeRuns, beforeTurns, beforeEvents, beforeSteps, beforeMessages := st.durableCounts()
	terminal := runWorkerUntilTerminal(t, service, st, turn.ID)
	if terminal.Status != agentdomain.TurnStatusSucceeded {
		t.Fatalf("legacy turn status = %q error=%q, want succeeded", terminal.Status, terminal.ErrorMessage)
	}

	if parsed := diagnosticCapture.eventsNamed("turn.metadata_parse_error"); len(parsed) != 0 {
		t.Fatalf("legacy run emitted a metadata parse diagnostic: %#v", parsed)
	}
	started := diagnosticCapture.eventsNamed("turn.started")
	if len(started) != 1 {
		t.Fatalf("turn.started records = %d, want 1", len(started))
	}
	if _, present := started[0].attrs["request_id"]; present {
		t.Fatalf("legacy turn.started unexpectedly gained a request_id: %#v", started[0].attrs)
	}
	if started[0].attrs["run_id"] != run.ID || started[0].attrs["turn_id"] != turn.ID ||
		started[0].attrs["owner_id"] != integrationOwnerID {
		t.Fatalf("legacy turn.started correlation mismatch: %#v", started[0].attrs)
	}
	if finished := diagnosticCapture.eventsNamed("turn.finished"); len(finished) != 1 {
		t.Fatalf("turn.finished records = %d, want 1", len(finished))
	}

	var input map[string]any
	if err := json.Unmarshal(st.runCopy(run.ID).InputJSON, &input); err != nil {
		t.Fatalf("decode persisted legacy run input: %v", err)
	}
	if _, exists := input["observability"]; exists {
		t.Fatal("legacy run input was rewritten with an observability namespace")
	}

	afterRuns, afterTurns, afterEvents, afterSteps, afterMessages := st.durableCounts()
	if afterRuns != beforeRuns || afterTurns != beforeTurns || afterEvents != beforeEvents || afterSteps != beforeSteps {
		t.Fatalf("legacy run created extra durable records: runs/turns/events/steps before=%d/%d/%d/%d after=%d/%d/%d/%d",
			beforeRuns, beforeTurns, beforeEvents, beforeSteps, afterRuns, afterTurns, afterEvents, afterSteps)
	}
	if afterMessages != beforeMessages+1 {
		t.Fatalf("legacy completion must add only the assistant message: messages before=%d after=%d",
			beforeMessages, afterMessages)
	}
}
