package eventhub

import (
	"encoding/json"

	runtimeevent "agentcanvas/internal/runtime/event"
)

// FromRuntimeEvent adapts the pre-v1 runtime event shape to a stream envelope.
// It is intentionally a pure conversion so the old runEventEmitter can be
// dual-published without changing its existing durable audit payload.
func FromRuntimeEvent(input runtimeevent.Event) StreamEvent {
	event := StreamEvent{
		Version:   RunStreamVersion,
		RunID:     input.RunID,
		Kind:      input.Type,
		Type:      input.Type,
		CreatedAt: input.CreatedAt,
		Payload:   input.Payload,
	}
	if input.Payload != nil {
		if payload, err := json.Marshal(input.Payload); err == nil {
			event.Data = payload
		}
	}
	return event
}

// ToRuntimeEvent projects a stream envelope back to the legacy event shape.
// Data is decoded into Payload when it contains a JSON object; otherwise the
// original legacy Payload field is retained.
func ToRuntimeEvent(event StreamEvent) runtimeevent.Event {
	payload := event.Payload
	if len(event.Data) > 0 {
		var decoded map[string]any
		if err := json.Unmarshal(event.Data, &decoded); err == nil {
			payload = decoded
		}
	}
	kind := event.Kind
	if kind == "" {
		kind = event.Type
	}
	return runtimeevent.Event{
		Type:      kind,
		RunID:     event.RunID,
		Payload:   payload,
		CreatedAt: event.CreatedAt,
	}
}

// PrepareLegacy is a migration convenience for code that still emits the
// original runtime/event.Event type.
func (h *MemoryHub) PrepareLegacy(runID int64, input runtimeevent.Event) StreamEvent {
	return h.Prepare(runID, FromRuntimeEvent(input))
}

// PublishLegacy publishes a legacy event after assigning its in-memory seq.
// The returned envelope is useful when callers need to include the seq in an
// SSE id or audit log.
func (h *MemoryHub) PublishLegacy(runID int64, input runtimeevent.Event) StreamEvent {
	prepared := h.PrepareLegacy(runID, input)
	h.PublishPrepared(prepared)
	return prepared
}
