package mysql

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/domain/workflow"
)

type fakeReflectionRunEvents struct{ items []workflow.RunEvent }

func (f *fakeReflectionRunEvents) Create(_ context.Context, item *workflow.RunEvent) error {
	f.items = append(f.items, *item)
	return nil
}
func (f *fakeReflectionRunEvents) ListByRun(context.Context, int64, int64) ([]workflow.RunEvent, error) {
	return f.items, nil
}

func TestReflectionEventSinkProjectsToRunEventStream(t *testing.T) {
	events := &fakeReflectionRunEvents{}
	sink := NewReflectionEventSink(events)
	if err := sink.PublishReflectionEvent(context.Background(), reflection.Event{Type: "reflection.stored", OwnerID: 1,
		WorkflowID: 2, RunID: 3, NodeID: "agent", Payload: map[string]any{"reflection_id": int64(7)}}); err != nil {
		t.Fatal(err)
	}
	if len(events.items) != 1 || events.items[0].EventType != "reflection.stored" || events.items[0].NodeType != "reflection" {
		t.Fatalf("unexpected projection: %+v", events.items)
	}
	var payload map[string]any
	if err := json.Unmarshal(events.items[0].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["workflow_id"] != float64(2) || payload["reflection_id"] != float64(7) {
		t.Fatalf("projection metadata missing: %+v", payload)
	}
}
