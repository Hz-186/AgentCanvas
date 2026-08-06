package eventhub

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeevent "agentcanvas/internal/runtime/event"
)

func testEvent(kind string) StreamEvent {
	return StreamEvent{Kind: kind, Data: []byte(`{"text":"hello"}`)}
}

func receiveEvent(t *testing.T, ch <-chan StreamEvent) StreamEvent {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatal("event channel closed before an event was received")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return StreamEvent{}
	}
}

func assertClosed(t *testing.T, ch <-chan StreamEvent) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected event channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("event channel did not close")
	}
}

func TestMemoryHubSubscribeBoundaryHasNoGap(t *testing.T) {
	hub := NewMemoryHub(Config{SubscriberBuffer: 4})
	prepared := hub.Prepare(42, testEvent("assistant.delta"))
	if prepared.Seq != 1 {
		t.Fatalf("first sequence = %d, want 1", prepared.Seq)
	}
	replay, live, cancel := hub.Subscribe(42, 0)
	defer cancel()
	if len(replay) != 0 {
		t.Fatalf("replay length = %d, want 0", len(replay))
	}
	hub.PublishPrepared(prepared)
	got := receiveEvent(t, live)
	if got.Seq != prepared.Seq || got.Kind != prepared.Kind {
		t.Fatalf("live event = %#v, want %#v", got, prepared)
	}
}

func TestMemoryHubConcurrentSubscribeAndPublishHasExactlyOneDelivery(t *testing.T) {
	for iteration := 0; iteration < 200; iteration++ {
		hub := NewMemoryHub(Config{SubscriberBuffer: 2})
		prepared := hub.Prepare(1, testEvent("assistant.delta"))
		start := make(chan struct{})
		var replay []StreamEvent
		var live <-chan StreamEvent
		var cancel func()
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			replay, live, cancel = hub.Subscribe(1, 0)
		}()
		go func() {
			defer wait.Done()
			<-start
			hub.PublishPrepared(prepared)
		}()
		close(start)
		wait.Wait()
		if cancel == nil {
			t.Fatal("Subscribe returned a nil cancel function")
		}
		if len(replay) == 1 {
			if replay[0].Seq != prepared.Seq {
				t.Fatalf("iteration %d replay seq = %d, want %d", iteration, replay[0].Seq, prepared.Seq)
			}
			select {
			case unexpected := <-live:
				t.Fatalf("iteration %d delivered event twice: replay and live %#v", iteration, unexpected)
			default:
			}
		} else if len(replay) == 0 {
			if got := receiveEvent(t, live); got.Seq != prepared.Seq {
				t.Fatalf("iteration %d live seq = %d, want %d", iteration, got.Seq, prepared.Seq)
			}
		} else {
			t.Fatalf("iteration %d replay length = %d, want 0 or 1", iteration, len(replay))
		}
		cancel()
	}
}

func TestMemoryHubSequencesAreIndependentPerRun(t *testing.T) {
	hub := NewMemoryHub()
	firstRunFirst := hub.Prepare(101, testEvent("status.update"))
	secondRunFirst := hub.Prepare(202, testEvent("status.update"))
	firstRunSecond := hub.Prepare(101, testEvent("status.update"))
	if firstRunFirst.Seq != 1 || secondRunFirst.Seq != 1 || firstRunSecond.Seq != 2 {
		t.Fatalf("per-run sequences = %d, %d, %d; want 1, 1, 2", firstRunFirst.Seq, secondRunFirst.Seq, firstRunSecond.Seq)
	}
}

func TestMemoryHubDeduplicatesPublishedSequence(t *testing.T) {
	hub := NewMemoryHub()
	prepared := hub.Prepare(7, testEvent("status.update"))
	hub.PublishPrepared(prepared)
	duplicate := prepared
	duplicate.Data = []byte(`{"text":"duplicate"}`)
	hub.PublishPrepared(duplicate)

	replay, live, cancel := hub.Subscribe(7, 0)
	defer cancel()
	if len(replay) != 1 || replay[0].Seq != 1 {
		t.Fatalf("replay = %#v, want one event with seq 1", replay)
	}
	select {
	case <-live:
		t.Fatal("duplicate sequence unexpectedly delivered to live subscriber")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestMemoryHubSlowConsumerIsDisconnected(t *testing.T) {
	hub := NewMemoryHub(Config{SubscriberBuffer: 1})
	_, live, cancel := hub.Subscribe(9, 0)
	defer cancel()
	hub.PublishPrepared(hub.Prepare(9, testEvent("assistant.delta")))
	// The first event fills the buffer.  The second event must not block the
	// publisher and instead disconnect this consumer.
	hub.PublishPrepared(hub.Prepare(9, testEvent("assistant.delta")))
	first := receiveEvent(t, live)
	if first.Seq != 1 {
		t.Fatalf("first buffered event seq = %d, want 1", first.Seq)
	}
	assertClosed(t, live)
}

func TestMemoryHubGapUsesSnapshotProvider(t *testing.T) {
	var calls atomic.Int32
	hub := NewMemoryHub(Config{
		MaxEvents: 2,
		SnapshotProvider: func(runID int64) (StreamEvent, error) {
			calls.Add(1)
			return StreamEvent{RunID: runID, Kind: StreamSnapshot, Data: []byte(`{"run":"authoritative"}`)}, nil
		},
	})
	for i := 0; i < 3; i++ {
		hub.PublishPrepared(hub.Prepare(10, testEvent("assistant.delta")))
	}
	replay, live, cancel := hub.Subscribe(10, 0)
	defer cancel()
	if len(replay) != 1 {
		t.Fatalf("gap replay = %#v, want one snapshot", replay)
	}
	if replay[0].Kind != StreamSnapshot || replay[0].Seq != 3 {
		t.Fatalf("gap replay = %#v, want snapshot at latest seq 3", replay[0])
	}
	if calls.Load() != 1 {
		t.Fatalf("snapshot provider calls = %d, want 1", calls.Load())
	}
	if _, err := hub.Snapshot(10); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	select {
	case <-live:
		t.Fatal("gap recovery unexpectedly produced a live event")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestMemoryHubByteLimitEvictsOldestEvent(t *testing.T) {
	hub := NewMemoryHub(Config{
		MaxEvents: 10,
		MaxBytes:  250,
		SnapshotProvider: func(runID int64) (StreamEvent, error) {
			return StreamEvent{RunID: runID, Kind: StreamSnapshot}, nil
		},
	})
	for i := 0; i < 2; i++ {
		hub.PublishPrepared(hub.Prepare(15, StreamEvent{Kind: "assistant.delta", Data: make([]byte, 100)}))
	}
	replay, _, cancel := hub.Subscribe(15, 0)
	defer cancel()
	if len(replay) != 1 || replay[0].Kind != StreamSnapshot || replay[0].Seq != 2 {
		t.Fatalf("byte-limited replay = %#v, want snapshot at seq 2", replay)
	}
}

func TestMemoryHubSequenceHoleTriggersSnapshot(t *testing.T) {
	hub := NewMemoryHub(Config{
		SnapshotProvider: func(runID int64) (StreamEvent, error) {
			return StreamEvent{RunID: runID, Kind: StreamSnapshot}, nil
		},
	})
	first := hub.Prepare(11, testEvent("assistant.delta"))
	second := hub.Prepare(11, testEvent("assistant.delta"))
	hub.PublishPrepared(second) // seq 1 was reserved but never became visible.
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("prepared sequences = %d, %d; want 1, 2", first.Seq, second.Seq)
	}
	replay, _, cancel := hub.Subscribe(11, 0)
	defer cancel()
	if len(replay) != 1 || replay[0].Kind != StreamSnapshot {
		t.Fatalf("replay = %#v, want a snapshot for the sequence hole", replay)
	}
}

func TestMemoryHubTerminalIsRetainedAndClosesLiveSubscribers(t *testing.T) {
	clock := time.Now().UTC()
	hub := NewMemoryHub(Config{
		MaxEvents:         1,
		MaxBytes:          1,
		TerminalRetention: time.Minute,
		Now:               func() time.Time { return clock },
	})
	_, live, cancel := hub.Subscribe(12, 0)
	defer cancel()
	hub.PublishPrepared(hub.Prepare(12, testEvent("assistant.delta")))
	terminal := hub.Prepare(12, StreamEvent{Kind: RunComplete, Data: []byte(`{"result":"ok"}`)})
	hub.CloseRun(12, terminal)
	if got := receiveEvent(t, live); got.Kind != "assistant.delta" {
		t.Fatalf("first live event kind = %q, want assistant.delta", got.Kind)
	}
	if got := receiveEvent(t, live); got.Kind != RunComplete {
		t.Fatalf("terminal live event kind = %q, want %s", got.Kind, RunComplete)
	}
	assertClosed(t, live)

	// The terminal remains replayable after the transient event was evicted.
	// Asking from seq 1 is within the retained window, so its original kind is
	// preserved.  Asking from seq 0 is a cursor gap and yields a snapshot.
	replay, closed, cancel2 := hub.Subscribe(12, 1)
	defer cancel2()
	if len(replay) != 1 || replay[0].Kind != RunComplete {
		t.Fatalf("terminal replay = %#v, want terminal only", replay)
	}
	assertClosed(t, closed)
	gapReplay, gapClosed, cancel3 := hub.Subscribe(12, 0)
	defer cancel3()
	if len(gapReplay) != 1 || gapReplay[0].Kind != StreamSnapshot {
		t.Fatalf("gap replay = %#v, want stream snapshot", gapReplay)
	}
	assertClosed(t, gapClosed)

	clock = clock.Add(2 * time.Minute)
	hub.PurgeExpired()
	if _, err := hub.Snapshot(12); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("Snapshot after retention error = %v, want ErrRunNotFound", err)
	}
}

func TestMemoryHubCloseRunIsIdempotent(t *testing.T) {
	hub := NewMemoryHub()
	terminal := hub.Prepare(13, StreamEvent{Kind: RunFailed})
	hub.PublishPrepared(terminal)
	hub.CloseRun(13, terminal) // close an already-published terminal event.
	hub.CloseRun(13, terminal)
	replay, live, cancel := hub.Subscribe(13, 0)
	defer cancel()
	if len(replay) != 1 || replay[0].Seq != terminal.Seq {
		t.Fatalf("replay = %#v, want one terminal event", replay)
	}
	assertClosed(t, live)
}

func TestMemoryHubLegacyRuntimeEventAdapter(t *testing.T) {
	hub := NewMemoryHub()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	prepared := hub.PublishLegacy(14, runtimeevent.Event{
		Type:      runtimeevent.AgentStep,
		Payload:   map[string]any{"step": "tool"},
		CreatedAt: createdAt,
	})
	if prepared.Seq != 1 || prepared.Kind != runtimeevent.AgentStep {
		t.Fatalf("legacy prepared event = %#v", prepared)
	}
	projected := ToRuntimeEvent(prepared)
	if projected.Type != runtimeevent.AgentStep || projected.RunID != 14 || !projected.CreatedAt.Equal(createdAt) {
		t.Fatalf("legacy projected event = %#v", projected)
	}
	if projected.Payload["step"] != "tool" {
		t.Fatalf("legacy projected payload = %#v", projected.Payload)
	}
}
