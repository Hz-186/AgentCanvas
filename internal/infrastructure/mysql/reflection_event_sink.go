package mysql

import (
	"context"
	"encoding/json"
	"time"

	"agentcanvas/internal/domain"
	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/reflection"
)

// ReflectionEventSink projects reflection lifecycle events into the Agent run
// event stream. The reflection domain only depends on EventSink,
// so a future event store can replace this projection without changing runtime code.
type ReflectionEventSink struct {
	events agentdomain.RunEventRepository
}

func NewReflectionEventSink(events agentdomain.RunEventRepository) *ReflectionEventSink {
	return &ReflectionEventSink{events: events}
}

func (s *ReflectionEventSink) PublishReflectionEvent(ctx context.Context, event reflection.Event) error {
	if s == nil || s.events == nil || event.OwnerID <= 0 || event.RunID <= 0 {
		return nil
	}
	payload := make(map[string]any, len(event.Payload)+2)
	for key, value := range event.Payload {
		payload[key] = value
	}
	payload["agent_id"] = event.AgentID
	if !event.OccurredAt.IsZero() {
		payload["occurred_at"] = event.OccurredAt
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	createdAt := event.OccurredAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return s.events.Create(ctx, &agentdomain.RunEvent{ImmutableModel: domain.ImmutableModel{OwnerID: event.OwnerID, CreatedAt: createdAt}, RunID: event.RunID,
		EventType: event.Type, PayloadJSON: payloadJSON})
}

var _ reflection.EventSink = (*ReflectionEventSink)(nil)
