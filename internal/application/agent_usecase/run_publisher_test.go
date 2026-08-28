package agent_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/conversation"
	workspacedomain "agentcanvas/internal/domain/workspace"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/observability"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/eventhub"
)

type publisherEventRepo struct {
	items []agentdomain.RunEvent
	err   error
}

func (r *publisherEventRepo) Create(_ context.Context, item *agentdomain.RunEvent) error {
	if r.err != nil {
		return r.err
	}
	r.items = append(r.items, *item)
	return nil
}

func (r *publisherEventRepo) ListByRun(context.Context, int64, int64) ([]agentdomain.RunEvent, error) {
	return append([]agentdomain.RunEvent(nil), r.items...), nil
}

type blockingPublisherEventRepo struct {
	calls        atomic.Int32
	entered      chan int32
	releaseFirst chan struct{}
}

func (r *blockingPublisherEventRepo) Create(_ context.Context, _ *agentdomain.RunEvent) error {
	call := r.calls.Add(1)
	r.entered <- call
	if call == 1 {
		<-r.releaseFirst
	}
	return nil
}

func (*blockingPublisherEventRepo) ListByRun(context.Context, int64, int64) ([]agentdomain.RunEvent, error) {
	return nil, nil
}

func receiveStreamEvent(t *testing.T, live <-chan eventhub.StreamEvent) eventhub.StreamEvent {
	t.Helper()
	select {
	case event := <-live:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream event")
		return eventhub.StreamEvent{}
	}
}

func TestRunEventEmitterPublishesOnlyAfterAuditWrite(t *testing.T) {
	hub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 4})
	_, live, cancel := hub.Subscribe(9, 0)
	defer cancel()
	repo := &publisherEventRepo{err: errors.New("audit failed")}
	emitter := &runEventEmitter{repo: repo, hub: hub, ownerID: 1, runID: 9}
	if err := emitter.Emit(context.Background(), runtimeevent.Event{Type: runtimeevent.AgentStarted}); err == nil {
		t.Fatal("expected audit failure")
	}
	select {
	case event := <-live:
		t.Fatalf("failed audit became visible: %+v", event)
	default:
	}
	repo.err = nil
	if err := emitter.Emit(context.Background(), runtimeevent.Event{Type: runtimeevent.AgentStarted}); err != nil {
		t.Fatal(err)
	}
	if got := receiveStreamEvent(t, live); got.Seq != 2 || got.Kind != "status.update" {
		t.Fatalf("unexpected projected event: %+v", got)
	}
}

func TestRunEventEmitterSerializesPrepareAuditAndPublishPerRun(t *testing.T) {
	repo := &blockingPublisherEventRepo{entered: make(chan int32, 2), releaseFirst: make(chan struct{})}
	hub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 4})
	emitter := &runEventEmitter{repo: repo, hub: hub, ownerID: 1, runID: 13}
	errors := make(chan error, 2)

	go func() {
		errors <- emitter.Emit(context.Background(), runtimeevent.Event{Type: runtimeevent.AgentStarted})
	}()
	if call := <-repo.entered; call != 1 {
		t.Fatalf("first audit call = %d", call)
	}
	go func() {
		errors <- emitter.Emit(context.Background(), runtimeevent.Event{Type: runtimeevent.AgentFinished})
	}()
	select {
	case call := <-repo.entered:
		t.Fatalf("audit call %d entered before the previous event was published", call)
	case <-time.After(50 * time.Millisecond):
	}
	close(repo.releaseFirst)
	if call := <-repo.entered; call != 2 {
		t.Fatalf("second audit call = %d", call)
	}
	for index := 0; index < 2; index++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	replay, _, cancel := hub.Subscribe(13, 0)
	defer cancel()
	if len(replay) != 2 || replay[0].Seq != 1 || replay[1].Seq != 2 {
		t.Fatalf("published sequence = %+v", replay)
	}
}

func TestRunEventEmitterKeepsReasoningLiveOnly(t *testing.T) {
	hub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 8})
	_, live, cancel := hub.Subscribe(10, 0)
	defer cancel()
	repo := &publisherEventRepo{}
	emitter := &runEventEmitter{repo: repo, hub: hub, ownerID: 1, runID: 10}
	for _, event := range []llm.ModelStreamEvent{{Kind: llm.ModelReasoningStart}, {Kind: llm.ModelReasoningDelta, Text: "secret"}, {Kind: llm.ModelReasoningEnd}} {
		if err := emitter.EmitModelEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"reasoning.start", "reasoning.delta", "reasoning.end"} {
		if got := receiveStreamEvent(t, live); got.Kind != kind {
			t.Fatalf("event kind = %q, want %q", got.Kind, kind)
		}
	}
	if len(repo.items) != 0 {
		t.Fatalf("reasoning was persisted: %+v", repo.items)
	}
}

func TestTodoUpdatedIsPersistedAndReplayableThroughV1Stream(t *testing.T) {
	hub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 4})
	repo := &publisherEventRepo{}
	emitter := &runEventEmitter{repo: repo, hub: hub, ownerID: 1, runID: 14}
	payload := map[string]any{
		"explanation": "working",
		"plan":        []map[string]any{{"step": "inspect", "status": "in_progress"}},
	}
	if err := emitter.Emit(context.Background(), runtimeevent.Event{Type: runtimeevent.TodoUpdated, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(repo.items) != 1 || repo.items[0].EventType != runtimeevent.TodoUpdated || !strings.Contains(string(repo.items[0].PayloadJSON), `"status":"in_progress"`) {
		t.Fatalf("Todo event was not durably persisted: %+v", repo.items)
	}
	replay, _, cancel := hub.Subscribe(14, 0)
	defer cancel()
	if len(replay) != 1 || replay[0].Seq != 1 || replay[0].Kind != "todo.updated" || !strings.Contains(string(replay[0].Data), `"step":"inspect"`) {
		t.Fatalf("Todo v1 replay mismatch: %+v", replay)
	}
}

func TestTerminalSnapshotIsAuthoritativeAndClosesCompletedRun(t *testing.T) {
	hub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 4})
	_, live, cancel := hub.Subscribe(11, 0)
	defer cancel()
	service := &Service{streamHub: hub}
	now := time.Now().UTC()
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 11, OwnerID: 1}, AgentID: 2, RunType: agentdomain.RunTypeTurn, Status: agentdomain.RunStatusSucceeded,
		DefinitionJSON: json.RawMessage(`{"system_prompt":"private"}`), OutputJSON: json.RawMessage(`{"final_answer":"final"}`), StartedAt: now, FinishedAt: &now}
	message := &conversation.Message{ImmutableModel: domain.ImmutableModel{ID: 12, OwnerID: 1}, ConversationID: 3, Role: conversation.RoleAssistant, Content: "final"}
	service.publishRunSnapshot(run, nil, message, llm.Usage{TotalTokens: 7})
	event := receiveStreamEvent(t, live)
	if event.Kind != eventhub.RunComplete {
		t.Fatalf("terminal event kind = %q", event.Kind)
	}
	if strings.Contains(string(event.Data), "system_prompt") || !strings.Contains(string(event.Data), `"content":"final"`) {
		t.Fatalf("terminal snapshot is unsafe or incomplete: %s", event.Data)
	}
	select {
	case _, open := <-live:
		if open {
			t.Fatal("completed run stream remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("completed run stream did not close")
	}
}

func TestPublicStreamRunIncludesResolvedWorkspace(t *testing.T) {
	workspaceID := int64(70)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 11, OwnerID: 1}, AgentID: 2, WorkspaceID: &workspaceID, RunType: agentdomain.RunTypeTurn}
	workspace := &workspacedomain.Workspace{BaseModel: domain.BaseModel{ID: workspaceID, OwnerID: 1}, RunID: 7, Kind: workspacedomain.KindShared, WorkspacePath: "/repo", BranchName: "main"}
	snapshot := publicStreamRun(run, workspace)
	if snapshot.Workspace == nil || snapshot.Workspace.ID != workspaceID || snapshot.Workspace.BranchName != "main" || snapshot.Workspace.RunID != run.ID {
		t.Fatalf("workspace missing from terminal run snapshot: %#v", snapshot)
	}
	if workspace.RunID != 7 {
		t.Fatalf("shared workspace record was mutated while building a child Run view: %#v", workspace)
	}
}

func TestWorkspaceEventPayloadPreservesCompleteGitSnapshot(t *testing.T) {
	item := &workspacedomain.Workspace{BaseModel: domain.BaseModel{ID: 70, OwnerID: 1}, ProjectID: 3, RunID: 11, Kind: workspacedomain.KindWorktree,
		RepositoryRoot: "/repo", WorkspacePath: "/repo/.worktrees/11-task", BranchName: "demo/11-task",
		BaseSHA: "aaaaaaaa", HeadSHA: "bbbbbbbb", Status: workspacedomain.StatusReady,
		Dirty: true, HasUnpushedCommits: true, Locked: true, LockReason: "agentcanvas run=11 pid=1234",
	}
	payload := workspaceEventPayload(item, nil)
	want := map[string]any{
		"workspace_id": int64(70), "project_id": int64(3), "run_id": int64(11),
		"kind": workspacedomain.KindWorktree, "repository_root": "/repo", "workspace_path": "/repo/.worktrees/11-task",
		"branch_name": "demo/11-task", "base_sha": "aaaaaaaa", "head_sha": "bbbbbbbb",
		"status": workspacedomain.StatusReady, "dirty": true, "has_unpushed_commits": true, "locked": true,
		"lock_reason": "agentcanvas run=11 pid=1234", "error_message": "",
	}
	for key, expected := range want {
		if payload[key] != expected {
			t.Fatalf("payload[%q] = %#v, want %#v (payload=%#v)", key, payload[key], expected, payload)
		}
	}
}

func TestPausedSnapshotKeepsStreamOpenForResume(t *testing.T) {
	hub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 4})
	_, live, cancel := hub.Subscribe(12, 0)
	defer cancel()
	service := &Service{streamHub: hub}
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: 12, OwnerID: 1}, AgentID: 2, RunType: agentdomain.RunTypeTurn, Status: agentdomain.RunStatusPaused}
	service.publishRunSnapshot(run, nil, nil, llm.Usage{})
	if got := receiveStreamEvent(t, live); got.Kind != eventhub.RunPaused {
		t.Fatalf("pause event = %+v", got)
	}
	emitter := &runEventEmitter{hub: hub, runID: 12}
	if err := emitter.EmitModelEvent(context.Background(), llm.ModelStreamEvent{Kind: llm.ModelTextStart}); err != nil {
		t.Fatal(err)
	}
	if got := receiveStreamEvent(t, live); got.Kind != "assistant.start" {
		t.Fatalf("resume event = %+v", got)
	}
}

type publisherOrderTracker struct {
	mu  sync.Mutex
	ops []string
}

func (o *publisherOrderTracker) record(op string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ops = append(o.ops, op)
}

func (o *publisherOrderTracker) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.ops...)
}

type orderingRunEventRepo struct {
	tracker *publisherOrderTracker
	err     error
}

func (r *orderingRunEventRepo) Create(_ context.Context, _ *agentdomain.RunEvent) error {
	if r.err != nil {
		return r.err
	}
	r.tracker.record("create")
	return nil
}

func (r *orderingRunEventRepo) ListByRun(context.Context, int64, int64) ([]agentdomain.RunEvent, error) {
	return nil, nil
}

type recordingOrderHub struct {
	inner   runStreamHub
	tracker *publisherOrderTracker
}

func (h *recordingOrderHub) Prepare(runID int64, event eventhub.StreamEvent) eventhub.StreamEvent {
	return h.inner.Prepare(runID, event)
}

func (h *recordingOrderHub) PublishPrepared(event eventhub.StreamEvent) {
	h.tracker.record("publish")
	h.inner.PublishPrepared(event)
}

func (h *recordingOrderHub) Subscribe(runID int64, afterSeq uint64) ([]eventhub.StreamEvent, <-chan eventhub.StreamEvent, func()) {
	return h.inner.Subscribe(runID, afterSeq)
}

func (h *recordingOrderHub) Snapshot(runID int64) (eventhub.StreamEvent, error) { return h.inner.Snapshot(runID) }

func (h *recordingOrderHub) CloseRun(runID int64, terminal eventhub.StreamEvent) {
	h.inner.CloseRun(runID, terminal)
}

type failingDiagnosticsHandler struct {
	calls atomic.Int32
}

func (h *failingDiagnosticsHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *failingDiagnosticsHandler) Handle(context.Context, slog.Record) error {
	h.calls.Add(1)
	return errors.New("diagnostics sink unavailable")
}

func (h *failingDiagnosticsHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *failingDiagnosticsHandler) WithGroup(string) slog.Handler      { return h }

func TestRunPublisherDiagnosticsPublishesAfterAuditEvenWhenLoggerFails(t *testing.T) {
	tracker := &publisherOrderTracker{}
	repo := &orderingRunEventRepo{tracker: tracker}
	innerHub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 4})
	hub := &recordingOrderHub{inner: innerHub, tracker: tracker}
	_, live, cancel := hub.Subscribe(9, 0)
	defer cancel()
	failing := &failingDiagnosticsHandler{}
	emitter := &runEventEmitter{repo: repo, hub: hub, ownerID: 1, runID: 9, diagnostics: slog.New(failing)}
	ctx := observability.WithCorrelation(context.Background(), observability.Correlation{}.
		WithRequestID("rid-pub-1").WithOwnerID(1).WithRunID(9))

	payload := map[string]any{"content": "payload-secret"}
	if err := emitter.Emit(ctx, runtimeevent.Event{Type: runtimeevent.AgentStarted, Payload: payload}); err != nil {
		t.Fatalf("diagnostics failure must not change Emit's business result: %v", err)
	}
	if failing.calls.Load() == 0 {
		t.Fatal("diagnostics were not attempted during Emit")
	}
	ops := tracker.snapshot()
	if len(ops) != 2 || ops[0] != "create" || ops[1] != "publish" {
		t.Fatalf("RunEvent must stay DB-first (create -> publish) despite logger failure: %v", ops)
	}
	if got := receiveStreamEvent(t, live); got.Kind != "status.update" {
		t.Fatalf("run event was not published after audit despite logger failure: %+v", got)
	}
}
