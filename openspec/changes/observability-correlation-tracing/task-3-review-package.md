# Task 3 Review Package — observability-correlation-tracing

BASE commit (no commits since; auto_commit=false): ff9eaf914eedd477874df0164308b5849788f6b2

## git diff --stat (tracked task files)
 internal/application/agent_usecase/service.go     | 95 ++++++++++++++++++++++-
 internal/application/agent_usecase/turn_worker.go | 21 +++++
 2 files changed, 115 insertions(+), 1 deletion(-)

## Untracked new test files
 M internal/application/agent_usecase/service.go
 M internal/application/agent_usecase/turn_worker.go
?? internal/application/agent_usecase/service_test.go
?? internal/application/agent_usecase/turn_worker_test.go

## git diff -U10 (tracked task files)
diff --git a/internal/application/agent_usecase/service.go b/internal/application/agent_usecase/service.go
index bc63452..3eace84 100644
--- a/internal/application/agent_usecase/service.go
+++ b/internal/application/agent_usecase/service.go
@@ -1,31 +1,33 @@
 package agent_usecase
 
 import (
 	"bytes"
 	"context"
 	"encoding/json"
 	"errors"
 	"fmt"
+	"log/slog"
 	"strings"
 	"sync"
 	"time"
 
 	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
 	"agentcanvas/internal/domain"
 	agentdomain "agentcanvas/internal/domain/agent"
 	"agentcanvas/internal/domain/conversation"
 	"agentcanvas/internal/domain/goal"
 	"agentcanvas/internal/domain/knowledge"
 	"agentcanvas/internal/domain/provider"
 	workspacedomain "agentcanvas/internal/domain/workspace"
 	agenterrors "agentcanvas/internal/pkg/errors"
+	"agentcanvas/internal/pkg/observability"
 	agentruntime "agentcanvas/internal/runtime/agentruntime"
 	"agentcanvas/internal/runtime/harness/rules"
 	"agentcanvas/internal/runtime/toolruntime"
 )
 
 type Service struct {
 	agents                 agentdomain.Repository
 	turns                  agentdomain.TurnRepository
 	conversations          conversation.AgentRepository
 	messages               conversation.MessageRepository
@@ -45,20 +47,31 @@ type Service struct {
 	leaseDuration          time.Duration
 	streamHub              runStreamHub
 	workspace              *workspaceusecase.Service
 	goals                  goal.Repository
 	goalTokenBudgetCeiling *int64
 	goalContinuationMu     sync.Mutex
 	goalContinuations      map[int64]struct{}
 	goalUsageMu            sync.Mutex
 	goalUsageByRun         map[int64]goalUsageState
 	goalStreams            *goalStreamHub
+	diagnostics            *slog.Logger
+}
+
+// diagnosticsLogger is the fail-open observation seam for lifecycle
+// diagnostics. It defaults to slog.Default so tests can capture events by
+// assigning a dedicated logger to the unexported diagnostics field.
+func (s *Service) diagnosticsLogger() *slog.Logger {
+	if s == nil || s.diagnostics == nil {
+		return slog.Default()
+	}
+	return s.diagnostics
 }
 
 func (s *Service) ConfigureGoalRepository(repository goal.Repository) { s.goals = repository }
 
 func (s *Service) ConfigureGoalTokenBudgetCeiling(ceiling *int64) {
 	if ceiling == nil {
 		s.goalTokenBudgetCeiling = nil
 		return
 	}
 	value := *ceiling
@@ -445,20 +458,98 @@ func decodeInputJSON(raw json.RawMessage) (map[string]any, error) {
 	}
 	if err := json.Unmarshal(trimmed, &input); err != nil {
 		return nil, err
 	}
 	if input == nil {
 		input = make(map[string]any)
 	}
 	return input, nil
 }
 
+// inputObservabilityVersion is the only supported schema version for the
+// additive observability namespace persisted in Run/Turn input JSON.
+const inputObservabilityVersion = 1
+
+// inputObservabilityMetadata builds the additive correlation namespace that
+// lets an async worker restore request identity after the HTTP context is
+// gone. Only identifiers available at creation time are persisted; run/turn
+// IDs are filled by the worker from the durable records it loads.
+func inputObservabilityMetadata(ctx context.Context, ownerID, conversationID int64) map[string]any {
+	metadata := map[string]any{
+		"version":         inputObservabilityVersion,
+		"request_id":      "",
+		"owner_id":        ownerID,
+		"conversation_id": conversationID,
+	}
+	if correlation, ok := observability.CorrelationFromContext(ctx); ok {
+		metadata["request_id"] = correlation.RequestID
+	}
+	return metadata
+}
+
+// decodeObservabilityRequestID extracts the request ID persisted in the
+// versioned observability namespace of Run/Turn input JSON. Absent metadata
+// (legacy records) returns ok=true with an empty request ID and requires no
+// diagnostic; malformed or unsupported metadata returns ok=false so callers
+// emit one bounded parse diagnostic while continuing with persisted IDs.
+func decodeObservabilityRequestID(input map[string]any) (string, bool) {
+	raw, present := input["observability"]
+	if !present {
+		return "", true
+	}
+	metadata, isObject := raw.(map[string]any)
+	if !isObject {
+		return "", false
+	}
+	version, _ := metadata["version"].(float64)
+	if version != inputObservabilityVersion {
+		return "", false
+	}
+	requestID, _ := metadata["request_id"].(string)
+	return requestID, true
+}
+
+// logTurnLifecycle emits one bounded, metadata-only turn lifecycle event
+// (turn.started / turn.finished / turn.failed and parse fallbacks). It is a
+// side-effect log: it never mutates durable state, return values, or the
+// business outcome of the turn.
+func (s *Service) logTurnLifecycle(ctx context.Context, event, result string, level slog.Level, turn *agentdomain.Turn, run *agentdomain.Run, latencyMS int64, errorClass string) {
+	attrs := []any{"event", event, "phase", "turn", "result", result, "latency_ms", latencyMS}
+	if correlation, ok := observability.CorrelationFromContext(ctx); ok && correlation.RequestID != "" {
+		attrs = append(attrs, "request_id", correlation.RequestID)
+	}
+	if turn != nil {
+		attrs = append(attrs, "owner_id", turn.OwnerID, "conversation_id", turn.ConversationID, "turn_id", turn.ID)
+	}
+	if run != nil {
+		attrs = append(attrs, "run_id", run.ID)
+		if run.ParentRunID != nil {
+			attrs = append(attrs, "parent_run_id", *run.ParentRunID)
+		}
+	} else if turn != nil && turn.RunID != nil {
+		attrs = append(attrs, "run_id", *turn.RunID)
+	}
+	if errorClass != "" {
+		attrs = append(attrs, "error_class", errorClass)
+	}
+	s.diagnosticsLogger().Log(ctx, level, event, attrs...)
+}
+
+// turnErrorClass derives a bounded, metadata-only error classification from a
+// cause; the full error message never enters diagnostics.
+func turnErrorClass(cause error) string {
+	if cause == nil {
+		return ""
+	}
+	return fmt.Sprintf("%T", cause)
+}
+
 func (s *Service) CreateConversation(ctx context.Context, ownerID, agentID int64, req CreateConversationRequest) (*conversation.Conversation, error) {
 	if _, err := s.getAgent(ctx, ownerID, agentID); err != nil {
 		return nil, err
 	}
 	title := strings.TrimSpace(req.Title)
 	if title == "" {
 		title = "New conversation"
 	}
 	mode, err := normalizeAgentMode(req.Mode)
 	if err != nil {
@@ -773,21 +864,23 @@ func (s *Service) StartTurn(ctx context.Context, ownerID, agentID, conversationI
 	if req.ManualCompaction {
 		// /compact is an operator echo, not real user input: tag it so
 		// compaction skips it and the search index never sees it.
 		userMessage.ContentType = conversation.ContentTypeSystemEcho
 	}
 	now := time.Now().UTC()
 	mode, err := normalizeAgentMode(conv.AgentMode)
 	if err != nil {
 		return nil, err
 	}
-	inputJSON, _ := json.Marshal(map[string]any{"query": content, "mode": mode, "manual_compaction": req.ManualCompaction})
+	input := map[string]any{"query": content, "mode": mode, "manual_compaction": req.ManualCompaction}
+	input["observability"] = inputObservabilityMetadata(ctx, ownerID, conversationID)
+	inputJSON, _ := json.Marshal(input)
 	run := &agentdomain.Run{
 		BaseModel: domain.BaseModel{
 			OwnerID:   ownerID,
 			CreatedAt: now,
 			UpdatedAt: now,
 		},
 		RunType:        agentdomain.RunTypeTurn,
 		AgentID:        agentID,
 		ConversationID: &conversationID,
 		Status:         agentdomain.RunStatusQueued,
diff --git a/internal/application/agent_usecase/turn_worker.go b/internal/application/agent_usecase/turn_worker.go
index 39b925c..e193f90 100644
--- a/internal/application/agent_usecase/turn_worker.go
+++ b/internal/application/agent_usecase/turn_worker.go
@@ -12,20 +12,21 @@ import (
 	"time"
 
 	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
 	"agentcanvas/internal/domain"
 	agentdomain "agentcanvas/internal/domain/agent"
 	"agentcanvas/internal/domain/conversation"
 	"agentcanvas/internal/domain/goal"
 	workspacedomain "agentcanvas/internal/domain/workspace"
 	"agentcanvas/internal/infrastructure/llm"
 	agenterrors "agentcanvas/internal/pkg/errors"
+	"agentcanvas/internal/pkg/observability"
 	runtimeagent "agentcanvas/internal/runtime/agent"
 	agentruntime "agentcanvas/internal/runtime/agentruntime"
 	runtimeevent "agentcanvas/internal/runtime/event"
 	"agentcanvas/internal/runtime/toolruntime"
 )
 
 type turnWorker struct{ *Service }
 
 func (w turnWorker) execute(ctx context.Context, turn *agentdomain.Turn) {
 	s := w.Service
@@ -66,20 +67,35 @@ func (w turnWorker) execute(ctx context.Context, turn *agentdomain.Turn) {
 	input, err := decodeInputJSON(turn.InputJSON)
 	if err != nil {
 		s.failTurn(ctx, turn, run, fmt.Errorf("decode turn input: %w", err))
 		return
 	}
 	task, _ := input["query"].(string)
 	manualCompaction, _ := input["manual_compaction"].(bool)
 	if mode, modeErr := normalizeAgentMode(fmt.Sprint(input["mode"])); modeErr == nil {
 		definition.Mode = mode
 	}
+	// Restore correlation from persisted metadata so async execution stays
+	// linkable without the original HTTP context. Legacy records keep an
+	// empty request ID; malformed metadata degrades to persisted IDs with a
+	// single bounded parse diagnostic.
+	requestID, metadataOK := decodeObservabilityRequestID(input)
+	if !metadataOK {
+		s.logTurnLifecycle(ctx, "turn.metadata_parse_error", "error", slog.LevelWarn, turn, run, 0, "invalid_observability_metadata")
+	}
+	ctx = observability.WithCorrelation(ctx, observability.Correlation{}.
+		WithRequestID(requestID).
+		WithOwnerID(turn.OwnerID).
+		WithConversationID(turn.ConversationID).
+		WithRunID(run.ID).
+		WithTurnID(turn.ID).
+		WithParentRunID(run.ParentRunID))
 	emitter := s.newRunEventEmitter(turn.OwnerID, run.ID, &turn.ConversationID)
 
 	var runtimeWorkspace *toolruntime.WorkspaceContext
 	var preparedWorkspace *workspacedomain.Workspace
 
 	projectID := int64(0)
 	if s.conversations != nil && run.ConversationID != nil {
 		conv, convErr := s.conversations.FindByID(ctx, turn.OwnerID, *run.ConversationID)
 		if convErr != nil {
 			s.failTurn(ctx, turn, run, convErr)
@@ -123,20 +139,21 @@ func (w turnWorker) execute(ctx context.Context, turn *agentdomain.Turn) {
 		_ = emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.WorkspaceReady, RunID: run.ID, Payload: workspaceEventPayload(preparedWorkspace, nil)})
 	}
 	if err := run.TransitionStatus(agentdomain.RunStatusRunning); err != nil {
 		s.failTurn(ctx, turn, run, err)
 		return
 	}
 	if err := s.turns.UpdateRunOwned(ctx, turn, run, false); err != nil {
 		s.failTurn(ctx, turn, run, err)
 		return
 	}
+	s.logTurnLifecycle(ctx, "turn.started", "ok", slog.LevelInfo, turn, run, 0, "")
 	result, execErr := s.runtime.Execute(ctx,
 		agentruntime.RunRequest{
 			RunIdentity:         agentruntime.RunIdentity{OwnerID: turn.OwnerID, AgentID: turn.AgentID, RunID: run.ID, UserMessageID: turn.UserMessageID, ParentRunID: run.ParentRunID},
 			ConversationContext: agentruntime.ConversationContext{ConversationID: &turn.ConversationID, ProjectID: projectID},
 			ExecutionTask:       agentruntime.ExecutionTask{Task: task, ManualCompaction: manualCompaction},
 			RuntimeResources:    agentruntime.RuntimeResources{Definition: definition},
 			RuntimePolicy:       agentruntime.RuntimePolicy{RuleHash: run.RuleHash},
 			WorkspaceContext:    agentruntime.WorkspaceContext{Workspace: runtimeWorkspace},
 			PersistenceHooks:    agentruntime.PersistenceHooks{StepRecorder: &runStepRecorder{repo: s.steps}},
 			Steering:            func() []string { return s.consumeSteering(run.ID) },
@@ -491,39 +508,41 @@ func (s *Service) failTurn(ctx context.Context, turn *agentdomain.Turn, run *age
 			}
 			return
 		}
 	}
 	now := time.Now().UTC()
 	failedTurn := *turn
 	failedTurn.Status, failedTurn.ErrorMessage, failedTurn.FinishedAt = agentdomain.TurnStatusFailed, cause.Error(), &now
 	if run == nil {
 		if err := s.turns.Update(ctx, &failedTurn); err == nil {
 			*turn = failedTurn
+			s.logTurnLifecycle(ctx, "turn.failed", "error", slog.LevelError, turn, nil, 0, turnErrorClass(cause))
 		} else if !errors.Is(err, agentdomain.ErrLeaseLost) {
 			slog.Default().Error("fail agent turn update failed", "turn_id", turn.ID, "run_id", turn.RunID, "error", err)
 		}
 		return
 	}
 	failedRun := *run
 	if err := failedRun.TransitionStatus(agentdomain.RunStatusFailed); err != nil {
 		failedRun.ErrorMessage = err.Error()
 	} else {
 		failedRun.ErrorMessage, failedRun.FinishedAt = cause.Error(), &now
 	}
 	failedRun.LatencyMS = int(now.Sub(failedRun.StartedAt).Milliseconds())
 	if err := s.turns.UpdateRunOwned(ctx, &failedTurn, &failedRun, true); err != nil {
 		if !errors.Is(err, agentdomain.ErrLeaseLost) {
 			slog.Default().Error("fail agent turn and run update failed", "turn_id", turn.ID, "run_id", run.ID, "error", err)
 		}
 		return
 	}
 	*turn, *run = failedTurn, failedRun
+	s.logTurnLifecycle(ctx, "turn.failed", "error", slog.LevelError, turn, run, int64(run.LatencyMS), turnErrorClass(cause))
 	// Flush wall-clock and any usage events already received before dropping the
 	// run accounting marker; error/abort paths are billable just like success.
 	s.accountGoalRun(ctx, run, &agentruntime.RunResult{Output: agentruntime.RunOutput{}})
 	s.clearSteering(run.ID)
 	if errors.Is(cause, llm.ErrRateLimited) {
 		s.markGoalStatus(ctx, run, goal.StatusUsageLimited)
 	} else {
 		s.markGoalBlocked(ctx, run)
 	}
 	s.clearGoalUsage(run.ID)
@@ -621,20 +640,21 @@ func (s *Service) completeTurn(ctx context.Context, turn *agentdomain.Turn, run
 		if value, ok := result.Output["total_tokens"].(int); ok {
 			run.TotalTokens = value
 		}
 		run.LatencyMS = int(now.Sub(run.StartedAt).Milliseconds())
 		if err := s.turns.UpdateRunOwned(ctx, turn, run, true); err != nil {
 			if !errors.Is(err, agentdomain.ErrLeaseLost) {
 				s.failTurn(ctx, turn, run, err)
 			}
 			return
 		}
+		s.logTurnLifecycle(ctx, "turn.finished", "ok", slog.LevelInfo, turn, run, int64(run.LatencyMS), "")
 		s.publishRunSnapshot(run, turn, nil, usageFromOutput(result.Output))
 		return
 	}
 	content, _ := result.Output["final_answer"].(string)
 	totalTokens, _ := result.Output["total_tokens"].(int)
 	// Realtime-written final answer: reference the row the sink already
 	// created instead of inserting a duplicate. JSON-decoded outputs carry
 	// the id as float64, direct runs as int64.
 	existingMessageID, _ := result.Output["assistant_message_id"].(int64)
 	if existingMessageID <= 0 {
@@ -680,20 +700,21 @@ func (s *Service) completeTurn(ctx context.Context, turn *agentdomain.Turn, run
 			s.failTurn(ctx, turn, run, err)
 			return
 		}
 	} else if err := s.turns.CompleteWithMessage(ctx, turn, assistant, run); err != nil {
 		if errors.Is(err, agentdomain.ErrLeaseLost) {
 			return
 		}
 		s.failTurn(ctx, turn, run, err)
 		return
 	}
+	s.logTurnLifecycle(ctx, "turn.finished", "ok", slog.LevelInfo, turn, run, int64(run.LatencyMS), "")
 	input, _ := decodeInputJSON(run.InputJSON)
 	mode, _ := normalizeAgentMode(fmt.Sprint(input["mode"]))
 	if s.improvement != nil && mode != "plan" {
 		if definition, err := agentruntime.DecodeDefinition(run.DefinitionJSON); err == nil {
 			_ = s.improvement.EnqueueTurnReview(ctx, turn, definition)
 		}
 	}
 	if s.sessionSearch != nil && assistant.ContentType == conversation.ContentTypeText {
 		_ = s.sessionSearch.IndexMessage(ctx, turn.OwnerID, turn.AgentID, assistant)
 	}

## NEW FILE: internal/application/agent_usecase/service_test.go
```go
package agent_usecase

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/pkg/observability"
)

// correlationTurnRepo extends the settings turn fake with an idempotency hit
// and a create call counter so idempotent replays can be asserted precisely.
type correlationTurnRepo struct {
	settingsTurnRepo
	existing    *agentdomain.Turn
	createCalls int
}

func (r *correlationTurnRepo) FindByIdempotencyKey(context.Context, int64, int64, string) (*agentdomain.Turn, error) {
	if r.existing != nil {
		return r.existing, nil
	}
	return nil, agentdomain.ErrNoTurnAvailable
}

func (r *correlationTurnRepo) CreateWithArtifacts(ctx context.Context, turn *agentdomain.Turn, message *conversation.Message, run *agentdomain.Run) error {
	r.createCalls++
	return r.settingsTurnRepo.CreateWithArtifacts(ctx, turn, message, run)
}

func startTurnCorrelationService(t *testing.T, turns *correlationTurnRepo, runs *settingsRunRepo) *Service {
	t.Helper()
	agentID := int64(10)
	conv := &conversation.Conversation{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 20, OwnerID: 3}}, AgentID: &agentID, AgentMode: "plan_execute"}
	conversations := &settingsConversationRepo{items: map[int64]*conversation.Conversation{20: conv}}
	definition := agentdomain.Definition{
		ModelConfig:     agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig:    agentdomain.PromptConfig{SystemPrompt: "test"},
		ExecutionLimits: agentdomain.ExecutionLimits{Mode: "react"},
	}
	agents := newSettingsAgentRepo()
	agents.items[agentID] = &agentdomain.Agent{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: agentID, OwnerID: 3}}, Status: agentdomain.StatusActive, DraftDefinition: definition}
	return NewService(agents, turns, conversations, nil, runs, nil, nil, nil, nil)
}

func TestStartTurnCorrelationPersistsRequestMetadata(t *testing.T) {
	turns := &correlationTurnRepo{}
	service := startTurnCorrelationService(t, turns, nil)

	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{RequestID: "rid-start-1", OwnerID: 3})
	accepted, err := service.StartTurn(ctx, 3, 10, 20, "request-obs-1", CreateTurnRequest{Content: " investigate "})
	if err != nil {
		t.Fatalf("StartTurn returned error: %v", err)
	}
	if accepted.Run == nil || accepted.Turn == nil {
		t.Fatalf("StartTurn must return both artifacts: %+v", accepted)
	}
	for name, raw := range map[string]json.RawMessage{"run": accepted.Run.InputJSON, "turn": accepted.Turn.InputJSON} {
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatalf("decode %s input: %v", name, err)
		}
		if input["query"] != "investigate" || input["mode"] != "plan" || input["manual_compaction"] != false {
			t.Fatalf("%s input lost business fields: %v", name, input)
		}
		metadata, _ := input["observability"].(map[string]any)
		if metadata == nil {
			t.Fatalf("%s input has no observability namespace: %v", name, input)
		}
		if metadata["version"] != float64(1) {
			t.Fatalf("%s observability version = %v, want 1", name, metadata["version"])
		}
		if metadata["request_id"] != "rid-start-1" || metadata["owner_id"] != float64(3) || metadata["conversation_id"] != float64(20) {
			t.Fatalf("%s observability correlation incomplete: %v", name, metadata)
		}
	}
}

func TestStartTurnCorrelationKeepsIdempotentExistingTurnMetadata(t *testing.T) {
	existingRunID := int64(91)
	existingInput := json.RawMessage(`{"query":"original","mode":"react","manual_compaction":false,"observability":{"version":1,"request_id":"rid-original","owner_id":3,"conversation_id":20}}`)
	existingTurn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 90, OwnerID: 3}, AgentID: 10, ConversationID: 20, RunID: &existingRunID, Status: agentdomain.TurnStatusQueued, InputJSON: existingInput}
	existingRun := &agentdomain.Run{BaseModel: domain.BaseModel{ID: existingRunID, OwnerID: 3}, Status: agentdomain.RunStatusQueued, InputJSON: existingInput}
	turns := &correlationTurnRepo{existing: existingTurn}
	runs := &settingsRunRepo{items: map[int64]*agentdomain.Run{existingRunID: existingRun}}
	service := startTurnCorrelationService(t, turns, runs)

	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{RequestID: "rid-duplicate", OwnerID: 3})
	accepted, err := service.StartTurn(ctx, 3, 10, 20, "request-dup", CreateTurnRequest{Content: "second attempt"})
	if err != nil {
		t.Fatalf("StartTurn returned error: %v", err)
	}
	if turns.createCalls != 0 {
		t.Fatalf("idempotent replay must not create artifacts, got %d create calls", turns.createCalls)
	}
	if accepted.Turn != existingTurn || accepted.Run != existingRun {
		t.Fatalf("idempotent replay must return the persisted objects: turn=%p run=%p", accepted.Turn, accepted.Run)
	}
	for name, raw := range map[string]json.RawMessage{"run": accepted.Run.InputJSON, "turn": accepted.Turn.InputJSON} {
		var input map[string]any
		if err := json.Unmarshal(raw, &input); err != nil {
			t.Fatalf("decode %s input: %v", name, err)
		}
		metadata, _ := input["observability"].(map[string]any)
		if metadata == nil || metadata["request_id"] != "rid-original" {
			t.Fatalf("%s metadata was overwritten by the duplicate request: %v", name, input)
		}
	}
}
```

## NEW FILE: internal/application/agent_usecase/turn_worker_test.go
```go
package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/pkg/observability"
	agentruntime "agentcanvas/internal/runtime/agentruntime"
)

// correlationRuntime captures the context handed to the runtime so tests can
// verify the worker restored correlation before execution.
type correlationRuntime struct {
	executeCtx   context.Context
	executeReq   agentruntime.RunRequest
	executeCalls int
	executeHook  func()
	result       *agentruntime.RunResult
	err          error
}

func (r *correlationRuntime) Execute(ctx context.Context, req agentruntime.RunRequest, _ agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	r.executeCtx = ctx
	r.executeReq = req
	r.executeCalls++
	if r.executeHook != nil {
		r.executeHook()
	}
	return r.result, r.err
}

func (*correlationRuntime) Resume(context.Context, agentruntime.ResumeRequest, agentruntime.EventEmitter) (*agentruntime.RunResult, error) {
	return nil, nil
}

// failingRunRepo injects a run load error into the worker.
type failingRunRepo struct {
	settingsRunRepo
	findErr error
}

func (r *failingRunRepo) FindByID(context.Context, int64, int64) (*agentdomain.Run, error) {
	return nil, r.findErr
}

// countingTurnRepo tracks durable status-update calls so diagnostics can be
// proven side-effect free.
type countingTurnRepo struct {
	settingsTurnRepo
	ownedUpdates int
	completions  int
}

func (r *countingTurnRepo) UpdateRunOwned(ctx context.Context, turn *agentdomain.Turn, run *agentdomain.Run, requeue bool) error {
	r.ownedUpdates++
	return r.settingsTurnRepo.UpdateRunOwned(ctx, turn, run, requeue)
}

func (r *countingTurnRepo) CompleteWithMessage(ctx context.Context, turn *agentdomain.Turn, message *conversation.Message, run *agentdomain.Run) error {
	r.completions++
	return r.settingsTurnRepo.CompleteWithMessage(ctx, turn, message, run)
}

type capturedLogEvent struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

// capturingLogHandler records every structured record emitted through the
// Service diagnostics seam.
type capturingLogHandler struct {
	mu     sync.Mutex
	events []capturedLogEvent
}

func (*capturingLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingLogHandler) Handle(_ context.Context, record slog.Record) error {
	event := capturedLogEvent{level: record.Level, msg: record.Message, attrs: map[string]any{}}
	record.Attrs(func(attr slog.Attr) bool {
		event.attrs[attr.Key] = attr.Value.Any()
		return true
	})
	h.mu.Lock()
	h.events = append(h.events, event)
	h.mu.Unlock()
	return nil
}

func (h *capturingLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingLogHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingLogHandler) eventsNamed(name string) []capturedLogEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	matched := make([]capturedLogEvent, 0)
	for _, event := range h.events {
		if event.attrs["event"] == name {
			matched = append(matched, event)
		}
	}
	return matched
}

func correlationWorkerDefinition(t *testing.T) json.RawMessage {
	t.Helper()
	definitionJSON, _, err := (agentdomain.Definition{
		ModelConfig:  agentdomain.ModelConfig{ProviderID: 1, Model: "test-model"},
		PromptConfig: agentdomain.PromptConfig{SystemPrompt: "test"},
	}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return definitionJSON
}

func newCorrelationWorkerService(turns agentdomain.TurnRepository, runs agentdomain.RunRepository, runtime *correlationRuntime, handler *capturingLogHandler) *Service {
	service := NewService(nil, turns, nil, nil, runs, nil, nil, nil, runtime)
	service.diagnostics = slog.New(handler)
	return service
}

func TestTurnWorkerCorrelationRestoresQueuedTurnContext(t *testing.T) {
	definitionJSON := correlationWorkerDefinition(t)
	runID, parentRunID, conversationID := int64(401), int64(77), int64(20)
	input := json.RawMessage(`{"query":"hello","mode":"react","observability":{"version":1,"request_id":"rid-worker-1","owner_id":3,"conversation_id":20}}`)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, ConversationID: &conversationID, ParentRunID: &parentRunID, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, InputJSON: input, StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 201, OwnerID: 3}, AgentID: 10, ConversationID: conversationID, RunID: &runID, Status: agentdomain.TurnStatusQueued, InputJSON: input}
	runtime := &correlationRuntime{result: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "done"}}}
	service := newCorrelationWorkerService(&settingsTurnRepo{}, &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}, runtime, &capturingLogHandler{})

	service.executeTurnOwned(context.Background(), turn)

	if runtime.executeCalls != 1 {
		t.Fatalf("runtime execute calls = %d, want 1", runtime.executeCalls)
	}
	correlation, ok := observability.CorrelationFromContext(runtime.executeCtx)
	if !ok {
		t.Fatal("runtime context must carry restored correlation")
	}
	if correlation.RequestID != "rid-worker-1" || correlation.OwnerID != 3 || correlation.ConversationID != conversationID || correlation.RunID != runID || correlation.TurnID != 201 {
		t.Fatalf("restored correlation mismatch: %+v", correlation)
	}
	if correlation.ParentRunID == nil || *correlation.ParentRunID != parentRunID {
		t.Fatalf("parent run id was not restored: %+v", correlation.ParentRunID)
	}
}

func TestTurnWorkerCorrelationFallsBackForLegacyMetadata(t *testing.T) {
	definitionJSON := correlationWorkerDefinition(t)
	runID, conversationID := int64(402), int64(20)
	input := json.RawMessage(`{"query":"legacy hello","mode":"react"}`)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, ConversationID: &conversationID, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, InputJSON: input, StartedAt: time.Now().UTC()}
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 202, OwnerID: 3}, AgentID: 10, ConversationID: conversationID, RunID: &runID, Status: agentdomain.TurnStatusQueued, InputJSON: input}
	runtime := &correlationRuntime{result: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "done"}}}
	handler := &capturingLogHandler{}
	service := newCorrelationWorkerService(&settingsTurnRepo{}, &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}, runtime, handler)

	service.executeTurnOwned(context.Background(), turn)

	if runtime.executeCalls != 1 {
		t.Fatalf("legacy turn must still execute, got %d calls", runtime.executeCalls)
	}
	correlation, ok := observability.CorrelationFromContext(runtime.executeCtx)
	if !ok {
		t.Fatal("legacy turn must still restore persisted identifiers")
	}
	if correlation.RequestID != "" {
		t.Fatalf("legacy turn must not fabricate a request id, got %q", correlation.RequestID)
	}
	if correlation.OwnerID != 3 || correlation.RunID != runID || correlation.TurnID != 202 {
		t.Fatalf("legacy correlation must use persisted identifiers: %+v", correlation)
	}
	if events := handler.eventsNamed("turn.metadata_parse_error"); len(events) != 0 {
		t.Fatalf("legacy metadata absence must not emit parse diagnostics: %+v", events)
	}
}

func TestTurnWorkerCorrelationFallsBackForMalformedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{name: "non-object", input: `{"query":"hello","mode":"react","manual_compaction":true,"observability":"oops"}`},
		{name: "unsupported-version", input: `{"query":"hello","mode":"react","manual_compaction":true,"observability":{"version":99,"request_id":"rid-hidden"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definitionJSON := correlationWorkerDefinition(t)
			runID, conversationID := int64(403), int64(20)
			input := json.RawMessage(tc.input)
			run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, ConversationID: &conversationID, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, InputJSON: input, StartedAt: time.Now().UTC()}
			turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 203, OwnerID: 3}, AgentID: 10, ConversationID: conversationID, RunID: &runID, Status: agentdomain.TurnStatusQueued, InputJSON: input}
			runtime := &correlationRuntime{result: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "done"}}}
			handler := &capturingLogHandler{}
			service := newCorrelationWorkerService(&settingsTurnRepo{}, &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}, runtime, handler)

			service.executeTurnOwned(context.Background(), turn)

			if runtime.executeCalls != 1 {
				t.Fatalf("malformed metadata must not block execution, got %d calls", runtime.executeCalls)
			}
			if runtime.executeReq.Task != "hello" || !runtime.executeReq.ManualCompaction {
				t.Fatalf("business input fields were lost: %+v", runtime.executeReq)
			}
			correlation, ok := observability.CorrelationFromContext(runtime.executeCtx)
			if !ok || correlation.RequestID != "" || correlation.RunID != runID {
				t.Fatalf("malformed metadata must degrade to persisted identifiers only: ok=%v %+v", ok, correlation)
			}
			events := handler.eventsNamed("turn.metadata_parse_error")
			if len(events) != 1 {
				t.Fatalf("expected exactly one bounded parse diagnostic, got %d", len(events))
			}
			attrs := events[0].attrs
			if attrs["run_id"] != runID || attrs["turn_id"] != int64(203) {
				t.Fatalf("parse diagnostic must carry run/turn identifiers: %v", attrs)
			}
			if turn.Status != agentdomain.TurnStatusSucceeded {
				t.Fatalf("parse fallback must not change business outcome: %s", turn.Status)
			}
		})
	}
}

func TestTurnWorkerCorrelationStopsBeforeRuntimeOnRunLoadError(t *testing.T) {
	runID := int64(404)
	turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 204, OwnerID: 3}, AgentID: 10, ConversationID: 20, RunID: &runID, Status: agentdomain.TurnStatusQueued, InputJSON: json.RawMessage(`{"query":"hello","mode":"react"}`)}
	runtime := &correlationRuntime{}
	handler := &capturingLogHandler{}
	service := newCorrelationWorkerService(&settingsTurnRepo{}, &failingRunRepo{findErr: errors.New("run storage unavailable")}, runtime, handler)

	service.executeTurnOwned(context.Background(), turn)

	if runtime.executeCalls != 0 {
		t.Fatalf("runtime must not execute when run load fails, got %d calls", runtime.executeCalls)
	}
	if turn.Status != agentdomain.TurnStatusFailed {
		t.Fatalf("run load failure must enter the existing failure path: %s", turn.Status)
	}
	failed := handler.eventsNamed("turn.failed")
	if len(failed) != 1 {
		t.Fatalf("expected one turn.failed diagnostic, got %d", len(failed))
	}
	if failed[0].attrs["turn_id"] != int64(204) || failed[0].attrs["run_id"] != runID {
		t.Fatalf("run load failure diagnostic must include run/turn identifiers: %v", failed[0].attrs)
	}
}

func TestTurnLifecycleDiagnosticsLogsTurnLifecycleEvents(t *testing.T) {
	definitionJSON := correlationWorkerDefinition(t)

	t.Run("success", func(t *testing.T) {
		runID, conversationID := int64(405), int64(20)
		input := json.RawMessage(`{"query":"hello","mode":"react","observability":{"version":1,"request_id":"rid-life-1","owner_id":3,"conversation_id":20}}`)
		run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, ConversationID: &conversationID, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, InputJSON: input, StartedAt: time.Now().UTC()}
		turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 205, OwnerID: 3}, AgentID: 10, ConversationID: conversationID, RunID: &runID, Status: agentdomain.TurnStatusQueued, InputJSON: input}
		runtime := &correlationRuntime{result: &agentruntime.RunResult{Output: agentruntime.RunOutput{"final_answer": "done"}}}
		handler := &capturingLogHandler{}
		startedBeforeRuntime := 0
		runtime.executeHook = func() { startedBeforeRuntime = len(handler.eventsNamed("turn.started")) }
		turns := &countingTurnRepo{}
		service := newCorrelationWorkerService(turns, &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}, runtime, handler)

		service.executeTurnOwned(context.Background(), turn)

		if startedBeforeRuntime != 1 {
			t.Fatalf("turn.started must be emitted exactly once before runtime execution, got %d", startedBeforeRuntime)
		}
		started := handler.eventsNamed("turn.started")
		if len(started) != 1 || started[0].attrs["result"] != "ok" || started[0].attrs["phase"] != "turn" {
			t.Fatalf("unexpected turn.started events: %+v", started)
		}
		if started[0].attrs["run_id"] != runID || started[0].attrs["turn_id"] != int64(205) || started[0].attrs["owner_id"] != int64(3) {
			t.Fatalf("turn.started must carry run/turn/owner correlation: %v", started[0].attrs)
		}
		finished := handler.eventsNamed("turn.finished")
		if len(finished) != 1 {
			t.Fatalf("expected exactly one turn.finished event, got %d", len(finished))
		}
		attrs := finished[0].attrs
		if attrs["result"] != "ok" || attrs["phase"] != "turn" {
			t.Fatalf("turn.finished must report phase/result: %v", attrs)
		}
		latency, latencyOK := attrs["latency_ms"].(int64)
		if !latencyOK || latency < 0 {
			t.Fatalf("turn.finished must carry latency_ms: %v", attrs["latency_ms"])
		}
		if attrs["request_id"] != "rid-life-1" || attrs["run_id"] != runID || attrs["turn_id"] != int64(205) {
			t.Fatalf("turn.finished must carry correlation metadata: %v", attrs)
		}
		if len(handler.eventsNamed("turn.failed")) != 0 {
			t.Fatal("success path must not emit turn.failed")
		}
		if turn.Status != agentdomain.TurnStatusSucceeded || turns.ownedUpdates != 1 || turns.completions != 1 {
			t.Fatalf("diagnostics altered durable state ordering: status=%s ownedUpdates=%d completions=%d", turn.Status, turns.ownedUpdates, turns.completions)
		}
	})

	t.Run("failure", func(t *testing.T) {
		runID, conversationID := int64(406), int64(20)
		input := json.RawMessage(`{"query":"hello","mode":"react","observability":{"version":1,"request_id":"rid-life-2","owner_id":3,"conversation_id":20}}`)
		run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: runID, OwnerID: 3}, AgentID: 10, ConversationID: &conversationID, Status: agentdomain.RunStatusQueued, DefinitionJSON: definitionJSON, InputJSON: input, StartedAt: time.Now().UTC()}
		turn := &agentdomain.Turn{BaseModel: domain.BaseModel{ID: 206, OwnerID: 3}, AgentID: 10, ConversationID: conversationID, RunID: &runID, Status: agentdomain.TurnStatusQueued, InputJSON: input, AttemptCount: 1, MaxAttempts: 1}
		runtime := &correlationRuntime{err: errors.New("model provider exploded")}
		handler := &capturingLogHandler{}
		turns := &countingTurnRepo{}
		service := newCorrelationWorkerService(turns, &settingsRunRepo{items: map[int64]*agentdomain.Run{runID: run}}, runtime, handler)

		service.executeTurnOwned(context.Background(), turn)

		if len(handler.eventsNamed("turn.started")) != 1 {
			t.Fatalf("turn.started must still precede the failed runtime call: %+v", handler.eventsNamed("turn.started"))
		}
		failed := handler.eventsNamed("turn.failed")
		if len(failed) != 1 {
			t.Fatalf("expected exactly one turn.failed event, got %d", len(failed))
		}
		attrs := failed[0].attrs
		if attrs["result"] != "error" || attrs["phase"] != "turn" {
			t.Fatalf("turn.failed must report phase/result: %v", attrs)
		}
		if class, _ := attrs["error_class"].(string); class == "" {
			t.Fatalf("turn.failed must carry a bounded error_class: %v", attrs)
		}
		if attrs["run_id"] != runID || attrs["turn_id"] != int64(206) {
			t.Fatalf("turn.failed must carry run/turn correlation: %v", attrs)
		}
		if len(handler.eventsNamed("turn.finished")) != 0 {
			t.Fatal("failure path must not emit turn.finished")
		}
		if turn.Status != agentdomain.TurnStatusFailed || run.Status != agentdomain.RunStatusFailed || turns.ownedUpdates != 2 || turns.completions != 0 {
			t.Fatalf("diagnostics altered durable state ordering: turn=%s run=%s ownedUpdates=%d completions=%d", turn.Status, run.Status, turns.ownedUpdates, turns.completions)
		}
	})
}
```
