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
