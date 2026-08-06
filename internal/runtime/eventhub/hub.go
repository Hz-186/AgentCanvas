// Package eventhub contains the in-process event transport used by a running
// agent.  The hub deliberately owns only the live/replay portion of the
// protocol; durable run events remain the responsibility of the application
// layer.
package eventhub

import (
	"encoding/json"
	"errors"
	"time"

	runtimeevent "agentcanvas/internal/runtime/event"
)

const (
	// RunStreamVersion is the first version of the run stream envelope.
	RunStreamVersion = runtimeevent.RunStreamVersion

	// Stream event kinds which have lifecycle/terminal semantics.  The legacy
	// snake_case names are kept here because older emitters still use them.
	StreamSnapshot = runtimeevent.StreamSnapshot
	RunComplete    = runtimeevent.RunComplete
	RunFailed      = runtimeevent.RunFailed
	RunPaused      = runtimeevent.RunPaused
	RunWaiting     = runtimeevent.RunWaiting
	RunCancelled   = runtimeevent.RunCancelled

	LegacyAgentFinished = "agent_finished"
	LegacyAgentFailed   = "agent_failed"
)

var (
	ErrRunNotFound         = errors.New("event hub run not found")
	ErrSnapshotUnavailable = errors.New("event hub snapshot unavailable")
)

type StreamEvent = runtimeevent.RunStreamEvent
type RunStreamEvent = runtimeevent.RunStreamEvent

// SnapshotProvider creates an authoritative snapshot when a subscriber's
// cursor has fallen behind the in-memory replay window.  Providers should not
// call back into the same hub while running; they are invoked while the run
// lock is held to preserve the replay/live boundary.
type SnapshotProvider func(runID int64) (StreamEvent, error)

// Config controls the memory and lifecycle limits of a MemoryHub.
type Config struct {
	// MaxEvents and MaxBytes are per-run limits.  The first limit reached evicts
	// the oldest transient events.  A terminal event is always retained even if
	// it is larger than MaxBytes.
	MaxEvents int
	MaxBytes  int64

	// TerminalRetention controls how long a closed run remains available for
	// replay.  A non-positive value uses the five-minute protocol default.
	TerminalRetention time.Duration

	// SubscriberBuffer is the size of each live subscriber's channel.  When it
	// fills, that subscriber is disconnected instead of back-pressuring the
	// runner.
	SubscriberBuffer int

	SnapshotProvider SnapshotProvider
	Now              func() time.Time
}

const (
	defaultMaxEvents         = 1024
	defaultMaxBytes          = 1 << 20
	defaultTerminalRetention = 5 * time.Minute
	defaultSubscriberBuffer  = 64
)

func (c Config) withDefaults() Config {
	if c.MaxEvents <= 0 {
		c.MaxEvents = defaultMaxEvents
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = defaultMaxBytes
	}
	if c.TerminalRetention <= 0 {
		c.TerminalRetention = defaultTerminalRetention
	}
	if c.SubscriberBuffer <= 0 {
		c.SubscriberBuffer = defaultSubscriberBuffer
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Hub is the narrow per-run transport contract.  Prepare reserves a sequence
// number but does not make the event visible.  A publisher must call
// PublishPrepared after its durable audit write succeeds.
type Hub interface {
	Prepare(runID int64, event StreamEvent) StreamEvent
	PublishPrepared(event StreamEvent)
	Subscribe(runID int64, afterSeq uint64) (replay []StreamEvent, live <-chan StreamEvent, cancel func())
	Snapshot(runID int64) (StreamEvent, error)
	CloseRun(runID int64, terminal StreamEvent)
}

func isTerminalKind(kind string) bool {
	switch kind {
	case RunComplete, RunFailed, RunPaused, RunWaiting, RunCancelled, LegacyAgentFinished, LegacyAgentFailed:
		return true
	default:
		return false
	}
}

func normalizeEvent(event StreamEvent, runID int64, now time.Time) StreamEvent {
	if event.Version == 0 {
		event.Version = RunStreamVersion
	}
	if event.RunID == 0 {
		event.RunID = runID
	}
	if event.Kind == "" {
		event.Kind = event.Type
	}
	if event.Type == "" {
		event.Type = event.Kind
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now.UTC()
	}
	if event.Data == nil && event.Payload != nil {
		if payload, err := json.Marshal(event.Payload); err == nil {
			event.Data = payload
		}
	}
	return event
}

func cloneEvent(event StreamEvent) StreamEvent {
	if event.Data != nil {
		event.Data = append(json.RawMessage(nil), event.Data...)
	}
	if event.Payload != nil {
		// Payload is legacy-only and is not expected to be mutated by the hub,
		// but cloning the map prevents a producer from racing a subscriber.
		payload := make(map[string]any, len(event.Payload))
		for key, value := range event.Payload {
			payload[key] = value
		}
		event.Payload = payload
	}
	return event
}

func eventSize(event StreamEvent) int64 {
	// Avoid a full JSON marshal for every delta while still accounting for the
	// variable payload.  The fixed portion is deliberately conservative.
	return int64(96 + len(event.Kind) + len(event.Type) + len(event.Data))
}
