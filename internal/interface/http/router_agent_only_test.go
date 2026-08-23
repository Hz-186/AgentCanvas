package httpserver

import (
	"agentcanvas/internal/domain"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentusecase "agentcanvas/internal/application/agent_usecase"
	authusecase "agentcanvas/internal/application/auth_usecase"
	agentdomain "agentcanvas/internal/domain/agent"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/interface/http/handler"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/runtime/eventhub"
)

func TestLegacyAgentPageRedirectsToAgentHome(t *testing.T) {
	legacyFlow := "/" + "work" + "flow"
	for _, path := range []string{legacyFlow + "s", legacyFlow + "-teams", "/flow-versions/2", "/eval-runs", "/canvas"} {
		if !isLegacyAgentPage(path) {
			t.Fatalf("legacy page %q was not recognized", path)
		}
	}
}

func TestCurrentAgentPagesAreNotLegacy(t *testing.T) {
	for _, path := range []string{"/app/agents", "/app/knowledge", "/login"} {
		if isLegacyAgentPage(path) {
			t.Fatalf("current page %q was incorrectly classified", path)
		}
	}
}

const (
	streamTestOwnerID = int64(71)
	streamTestRunID   = int64(91)
)

type streamTestRunRepository struct {
	agentdomain.RunRepository
	runs map[int64]*agentdomain.Run
}

func (r *streamTestRunRepository) FindByID(_ context.Context, ownerID, runID int64) (*agentdomain.Run, error) {
	run := r.runs[runID]
	if run == nil || run.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	copy := *run
	return &copy, nil
}

type streamSubscribeNotifier struct {
	eventhub.Hub
	once       sync.Once
	subscribed chan struct{}
}

func (h *streamSubscribeNotifier) Subscribe(runID int64, afterSeq uint64) ([]eventhub.StreamEvent, <-chan eventhub.StreamEvent, func()) {
	replay, live, cancel := h.Hub.Subscribe(runID, afterSeq)
	h.once.Do(func() { close(h.subscribed) })
	return replay, live, cancel
}

type streamFrame struct {
	ID    uint64
	Event string
	Data  eventhub.StreamEvent
}

func newRunStreamRouter(t *testing.T, run *agentdomain.Run, hub eventhub.Hub) (http.Handler, string) {
	t.Helper()
	runs := map[int64]*agentdomain.Run{}
	if run != nil {
		runs[run.ID] = run
	}
	service := agentusecase.NewService(nil, nil, nil, nil, &streamTestRunRepository{runs: runs}, nil, nil, nil, nil)
	service.ConfigureEventHub(hub)
	agentHandler := handler.NewAgentHandler(service)

	jwt := cryptoinfra.NewJWTService("run-stream-v1-test-secret", time.Hour)
	token, _, err := jwt.IssueAccessToken(streamTestOwnerID)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}
	authService := authusecase.NewService(nil, nil, nil, nil, nil, nil, jwt, cryptoinfra.NewTokenHasher("test"), nil, nil, time.Hour)
	router := NewRouter(RouterDeps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		AgentHandler: agentHandler,
		AuthService:  authService,
	})
	return router, token
}

func authenticatedStreamRequest(token, target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func parseRunStreamFrames(t *testing.T, body string) []streamFrame {
	t.Helper()
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	blocks := strings.Split(trimmed, "\n\n")
	frames := make([]streamFrame, 0, len(blocks))
	for _, block := range blocks {
		var frame streamFrame
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "id: "):
				id, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "id: ")), 10, 64)
				if err != nil {
					t.Fatalf("invalid SSE id line %q: %v", line, err)
				}
				frame.ID = id
			case strings.HasPrefix(line, "event: "):
				frame.Event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame.Data); err != nil {
					t.Fatalf("invalid SSE data line %q: %v", line, err)
				}
			}
		}
		frames = append(frames, frame)
	}
	return frames
}

func publishStreamEvent(hub *eventhub.MemoryHub, runID int64, kind string) eventhub.StreamEvent {
	event := hub.Prepare(runID, eventhub.StreamEvent{RunID: runID, Kind: kind, Data: json.RawMessage(`{"text":"chunk"}`)})
	hub.PublishPrepared(event)
	return event
}

func TestRunStreamV1RouterReplaysThenStreamsLiveUntilTerminal(t *testing.T) {
	baseHub := eventhub.NewMemoryHub(eventhub.Config{SubscriberBuffer: 8})
	first := publishStreamEvent(baseHub, streamTestRunID, "assistant.start")
	notifier := &streamSubscribeNotifier{Hub: baseHub, subscribed: make(chan struct{})}
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: streamTestRunID, OwnerID: streamTestOwnerID}, Status: agentdomain.RunStatusRunning}
	router, token := newRunStreamRouter(t, run, notifier)

	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(recorder, authenticatedStreamRequest(token, "/api/v1/runs/91/events/stream/v1"))
	}()
	select {
	case <-notifier.subscribed:
	case <-time.After(time.Second):
		t.Fatal("v1 handler did not subscribe to the event hub")
	}
	second := publishStreamEvent(baseHub, streamTestRunID, "assistant.delta")
	terminal := baseHub.Prepare(streamTestRunID, eventhub.StreamEvent{RunID: streamTestRunID, Kind: eventhub.RunComplete})
	baseHub.CloseRun(streamTestRunID, terminal)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("v1 handler did not return after terminal channel closure")
	}

	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("response status/content-type = %d/%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	frames := parseRunStreamFrames(t, recorder.Body.String())
	if len(frames) != 3 {
		t.Fatalf("SSE frames = %#v, want replay + live + terminal", frames)
	}
	want := []struct {
		seq  uint64
		kind string
	}{{first.Seq, first.Kind}, {second.Seq, second.Kind}, {terminal.Seq, terminal.Kind}}
	for index, expected := range want {
		if frames[index].ID != expected.seq || frames[index].Data.Seq != expected.seq || frames[index].Event != expected.kind {
			t.Fatalf("frame %d = %#v, want seq=%d kind=%q", index, frames[index], expected.seq, expected.kind)
		}
	}
}

func TestRunStreamV1RouterUsesHighestCursor(t *testing.T) {
	hub := eventhub.NewMemoryHub()
	for _, kind := range []string{"assistant.start", "assistant.delta", "assistant.end"} {
		publishStreamEvent(hub, streamTestRunID, kind)
	}
	terminal := hub.Prepare(streamTestRunID, eventhub.StreamEvent{RunID: streamTestRunID, Kind: eventhub.RunComplete})
	hub.CloseRun(streamTestRunID, terminal)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: streamTestRunID, OwnerID: streamTestOwnerID}, Status: agentdomain.RunStatusSucceeded}
	router, token := newRunStreamRouter(t, run, hub)

	tests := []struct {
		name        string
		target      string
		header      string
		wantFirstID uint64
	}{
		{name: "Last-Event-ID wins", target: "/api/v1/runs/91/events/stream/v1?after_seq=1", header: "2", wantFirstID: 3},
		{name: "after_seq wins", target: "/api/v1/runs/91/events/stream/v1?after_seq=3", header: "1", wantFirstID: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := authenticatedStreamRequest(token, test.target)
			request.Header.Set("Last-Event-ID", test.header)
			router.ServeHTTP(recorder, request)
			frames := parseRunStreamFrames(t, recorder.Body.String())
			if len(frames) == 0 || frames[0].ID != test.wantFirstID {
				t.Fatalf("first frame = %#v, want id %d", frames, test.wantFirstID)
			}
			for _, frame := range frames {
				if frame.ID < test.wantFirstID {
					t.Fatalf("cursor replayed stale frame %#v", frame)
				}
			}
		})
	}
}

func TestRunStreamV1RouterReturnsSnapshotForReplayGap(t *testing.T) {
	var snapshotCalls atomic.Int32
	hub := eventhub.NewMemoryHub(eventhub.Config{
		MaxEvents: 2,
		SnapshotProvider: func(runID int64) (eventhub.StreamEvent, error) {
			snapshotCalls.Add(1)
			return eventhub.StreamEvent{RunID: runID, Kind: eventhub.StreamSnapshot, Data: json.RawMessage(`{"run":{"id":91}}`)}, nil
		},
	})
	for index := 0; index < 3; index++ {
		publishStreamEvent(hub, streamTestRunID, "reasoning.delta")
	}
	terminal := hub.Prepare(streamTestRunID, eventhub.StreamEvent{RunID: streamTestRunID, Kind: eventhub.RunComplete})
	hub.CloseRun(streamTestRunID, terminal)
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: streamTestRunID, OwnerID: streamTestOwnerID}, Status: agentdomain.RunStatusSucceeded}
	router, token := newRunStreamRouter(t, run, hub)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authenticatedStreamRequest(token, "/api/v1/runs/91/events/stream/v1?after_seq=0"))
	frames := parseRunStreamFrames(t, recorder.Body.String())
	if len(frames) != 1 || frames[0].Event != eventhub.StreamSnapshot || frames[0].ID != terminal.Seq {
		t.Fatalf("gap frames = %#v, want one snapshot at seq %d", frames, terminal.Seq)
	}
	if snapshotCalls.Load() != 1 {
		t.Fatalf("snapshot provider calls = %d, want 1", snapshotCalls.Load())
	}
	if strings.Contains(recorder.Body.String(), "reasoning.delta") {
		t.Fatalf("gap response leaked transient reasoning: %s", recorder.Body.String())
	}
}

func TestRunStreamV1RouterRequiresAuthenticationAndHidesMissingRun(t *testing.T) {
	hub := eventhub.NewMemoryHub()
	run := &agentdomain.Run{BaseModel: domain.BaseModel{ID: streamTestRunID, OwnerID: streamTestOwnerID}, Status: agentdomain.RunStatusRunning}
	router, token := newRunStreamRouter(t, run, hub)

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/runs/91/events/stream/v1", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, authenticatedStreamRequest(token, "/api/v1/runs/999/events/stream/v1"))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing run status = %d body=%s, want %d", missing.Code, missing.Body.String(), http.StatusNotFound)
	}
	if strings.Contains(strings.ToLower(missing.Body.String()), "stream") {
		t.Fatalf("missing run response exposed stream internals: %s", missing.Body.String())
	}
}
