package event

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunStreamEnvelopeDoesNotExposeLegacySidecars(t *testing.T) {
	event := RunStreamEvent{Version: RunStreamVersion, RunID: 7, Seq: 1, Kind: AssistantDelta, CreatedAt: time.Unix(0, 0).UTC(),
		Data: json.RawMessage(`{"segment_id":"a1","text":"hi"}`), Type: "legacy", Payload: map[string]any{"reasoning": "secret"}}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, `"type"`) || strings.Contains(text, `"payload"`) || strings.Contains(text, "secret") {
		t.Fatalf("legacy sidecar leaked into v1 envelope: %s", text)
	}
	if strings.Contains(text, "conversation_id") {
		t.Fatalf("nil conversation_id must be omitted: %s", text)
	}
}

func TestRunStreamPayloadFixturesMarshalForEveryKind(t *testing.T) {
	fixtures := map[string]any{
		AssistantStart:   TextPayload{SegmentID: "a1"},
		AssistantDelta:   TextPayload{SegmentID: "a1", Text: "hello"},
		AssistantEnd:     TextPayload{SegmentID: "a1"},
		ReasoningStart:   TextPayload{SegmentID: "r1"},
		ReasoningDelta:   TextPayload{SegmentID: "r1", Text: "thinking"},
		ReasoningEnd:     TextPayload{SegmentID: "r1"},
		StatusUpdate:     StatusPayload{Message: "working", Level: "info"},
		ToolStart:        ToolPayload{CallID: "c1", SegmentID: "t1", Name: "search", Status: "running"},
		ToolProgress:     ToolPayload{CallID: "c1", SegmentID: "t1", Name: "search", Status: "running"},
		ToolComplete:     ToolPayload{CallID: "c1", SegmentID: "t1", Name: "search", Status: "succeeded"},
		ToolError:        ToolPayload{CallID: "c1", SegmentID: "t1", Name: "search", Status: "failed", ErrorCode: "timeout"},
		ApprovalRequired: ApprovalPayload{RequestID: 1, CallID: "c1", ToolName: "write", Reason: "risk"},
		UsageUpdate:      UsagePayload{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		StreamSnapshot:   TerminalSnapshotPayload{Run: json.RawMessage(`{"id":7}`), Usage: UsagePayload{}},
		RunComplete:      TerminalSnapshotPayload{Run: json.RawMessage(`{"id":7}`), Usage: UsagePayload{}},
		RunFailed:        TerminalSnapshotPayload{Run: json.RawMessage(`{"id":7}`), Usage: UsagePayload{}},
		RunPaused:        TerminalSnapshotPayload{Run: json.RawMessage(`{"id":7}`), Usage: UsagePayload{}},
		RunWaiting:       TerminalSnapshotPayload{Run: json.RawMessage(`{"id":7}`), Usage: UsagePayload{}},
		RunCancelled:     TerminalSnapshotPayload{Run: json.RawMessage(`{"id":7}`), Usage: UsagePayload{}},
	}
	for kind, fixture := range fixtures {
		t.Run(kind, func(t *testing.T) {
			data, err := json.Marshal(fixture)
			if err != nil || !json.Valid(data) || string(data) == "{}" {
				t.Fatalf("fixture marshal failed: data=%s err=%v", data, err)
			}
		})
	}
}
