package memory_usecase

import (
	"encoding/json"
	"strings"
	"testing"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
)

// evidenceRendererSecret is the raw secret substring used across the renderer
// scenarios. No rendered unit field may ever contain it.
const evidenceRendererSecret = "abcd1234efgh"

// textRow builds a persisted text message row as the message sink writes it.
func textRow(id int64, role, content string, runID int64) conversation.Message {
	return conversation.Message{
		ImmutableModel: domain.ImmutableModel{ID: id, OwnerID: 7},
		ConversationID: 3,
		Role:           role,
		Content:        content,
		RunID:          &runID,
		ContentType:    conversation.ContentTypeText,
	}
}

// callRow builds a function_call row: empty content, identity and arguments in
// metadata_json (the three-key shape written by the sink).
func callRow(id int64, toolCallID, toolName, arguments string, runID int64) conversation.Message {
	metadata, err := json.Marshal(map[string]any{
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"arguments":    json.RawMessage(arguments),
	})
	if err != nil {
		panic(err)
	}
	return conversation.Message{
		ImmutableModel: domain.ImmutableModel{ID: id, OwnerID: 7},
		ConversationID: 3,
		Role:           conversation.RoleAssistant,
		Content:        "",
		RunID:          &runID,
		ContentType:    conversation.ContentTypeFunctionCall,
		MetadataJSON:   metadata,
	}
}

// outputRow builds a function_call_output row. A nil isError reproduces legacy
// rows that carry only tool_call_id/tool_name; a non-nil pointer reproduces
// Task 1 enriched rows that write is_error and error_code deterministically.
func outputRow(id int64, toolCallID, toolName, content string, runID int64, isError *bool, errorCode string) conversation.Message {
	meta := map[string]any{
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
	}
	if isError != nil {
		meta["is_error"] = *isError
		meta["error_code"] = errorCode
	}
	metadata, err := json.Marshal(meta)
	if err != nil {
		panic(err)
	}
	return conversation.Message{
		ImmutableModel: domain.ImmutableModel{ID: id, OwnerID: 7},
		ConversationID: 3,
		Role:           conversation.RoleTool,
		Content:        content,
		RunID:          &runID,
		ContentType:    conversation.ContentTypeFunctionCallOutput,
		MetadataJSON:   metadata,
	}
}

func boolPtr(value bool) *bool { return &value }

// assertTriState locks the unknown/success/failure enum as mutually exclusive
// on exchange units: every rendered exchange must land on exactly one value.
func assertTriState(t *testing.T, unit EvidenceUnit) {
	t.Helper()
	switch unit.ErrorState {
	case EvidenceErrorStateUnknown, EvidenceErrorStateSuccess, EvidenceErrorStateFailure:
	default:
		t.Fatalf("exchange unit %d carries out-of-enum error state %q", unit.MessageID, unit.ErrorState)
	}
}

// assertNoRawSecret proves redaction covers every rendered text surface.
func assertNoRawSecret(t *testing.T, units []EvidenceUnit) {
	t.Helper()
	for _, unit := range units {
		for _, field := range []string{unit.Content, unit.Arguments, unit.Output} {
			if strings.Contains(field, evidenceRendererSecret) {
				t.Fatalf("unit %d leaks raw secret in rendered field: %q", unit.MessageID, field)
			}
		}
	}
}

func TestEvidenceRenderer(t *testing.T) {
	t.Run("shouldRenderTextUnitsWithIdentityAndRedaction", func(t *testing.T) {
		messages := []conversation.Message{
			textRow(11, conversation.RoleUser, `Please configure api_key = "abcd1234efgh" before deploying.`, 1),
		}
		renderer := NewEvidenceRenderer()
		units := renderer.Render(messages)
		if len(units) != 1 {
			t.Fatalf("expected 1 unit, got %d", len(units))
		}
		unit := units[0]
		if unit.Kind != EvidenceUnitText {
			t.Fatalf("expected text unit, got %q", unit.Kind)
		}
		if unit.MessageID != 11 {
			t.Fatalf("expected message id 11, got %d", unit.MessageID)
		}
		if unit.RunID == nil || *unit.RunID != 1 {
			t.Fatalf("expected run id 1, got %v", unit.RunID)
		}
		if unit.Role != conversation.RoleUser {
			t.Fatalf("expected role user, got %q", unit.Role)
		}
		if strings.Contains(unit.Content, evidenceRendererSecret) {
			t.Fatalf("content still carries raw secret: %q", unit.Content)
		}
		if !strings.Contains(unit.Content, "[REDACTED]") {
			t.Fatalf("content does not carry the redaction placeholder: %q", unit.Content)
		}
		if !strings.Contains(unit.Content, "api_key") {
			t.Fatalf("redaction removed the key name instead of the value: %q", unit.Content)
		}
	})

	t.Run("shouldPairCallAndOutputByToolCallID", func(t *testing.T) {
		messages := []conversation.Message{
			callRow(21, "call-1", "search_code", `{"query":"auth"}`, 2),
			outputRow(22, "call-1", "search_code", "found 3 matches", 2, nil, ""),
			// Same tool name in another run with a different tool_call_id: the
			// renderer must pair by exact tool_call_id only, never by name.
			callRow(23, "call-2", "search_code", `{"note":"set api_key = abcd1234efgh first","query":"auth"}`, 3),
			outputRow(24, "call-2", "search_code", "token = abcd1234efgh accepted", 3, nil, ""),
		}
		renderer := NewEvidenceRenderer()
		units := renderer.Render(messages)
		if len(units) != 2 {
			t.Fatalf("expected 2 exchange units, got %d", len(units))
		}
		first, second := units[0], units[1]
		if first.Kind != EvidenceUnitExchange || second.Kind != EvidenceUnitExchange {
			t.Fatalf("expected exchange units, got %q and %q", first.Kind, second.Kind)
		}
		if first.ToolCallID != "call-1" || first.ToolName != "search_code" {
			t.Fatalf("first unit lost call identity: %q/%q", first.ToolCallID, first.ToolName)
		}
		if !strings.Contains(first.Arguments, `"query":"auth"`) {
			t.Fatalf("first unit lost arguments: %q", first.Arguments)
		}
		if first.Output != "found 3 matches" {
			t.Fatalf("first unit paired wrong output: %q", first.Output)
		}
		// Cross-run guard: identical tool names must not cross-pair.
		if second.ToolCallID != "call-2" {
			t.Fatalf("second unit paired across runs: %q", second.ToolCallID)
		}
		if !strings.Contains(second.Output, "accepted") {
			t.Fatalf("second unit lost its own output: %q", second.Output)
		}
		if strings.Contains(first.Output, "accepted") {
			t.Fatalf("first unit absorbed the other run's output: %q", first.Output)
		}
		// Redaction covers the input side: arguments and output are redacted
		// before entering the unit.
		assertNoRawSecret(t, units)
		if !strings.Contains(second.Arguments, "[REDACTED]") {
			t.Fatalf("arguments were not redacted: %q", second.Arguments)
		}
		if !strings.Contains(second.Output, "[REDACTED]") {
			t.Fatalf("output was not redacted: %q", second.Output)
		}
		assertTriState(t, first)
		assertTriState(t, second)
	})

	t.Run("shouldMarkMissingErrorStateAsUnknown", func(t *testing.T) {
		messages := []conversation.Message{
			callRow(31, "call-legacy", "run_command", `{"cmd":"make"}`, 4),
			// Legacy row: no is_error key. Failure-sounding text must not be
			// used to guess the state.
			outputRow(32, "call-legacy", "run_command", "command failed with exit code 1", 4, nil, ""),
		}
		renderer := NewEvidenceRenderer()
		units := renderer.Render(messages)
		if len(units) != 1 {
			t.Fatalf("expected 1 unit, got %d", len(units))
		}
		unit := units[0]
		assertTriState(t, unit)
		if unit.ErrorState != EvidenceErrorStateUnknown {
			t.Fatalf("expected unknown error state, got %q", unit.ErrorState)
		}
		if string(unit.ErrorState) != "unknown" {
			t.Fatalf("unknown error state must serialize as \"unknown\", got %q", unit.ErrorState)
		}
		if unit.ErrorState == EvidenceErrorStateSuccess || unit.ErrorState == EvidenceErrorStateFailure {
			t.Fatalf("legacy row must not collapse to a boolean outcome, got %q", unit.ErrorState)
		}
	})

	t.Run("shouldCountSameArgFailuresAndDetectRecovery", func(t *testing.T) {
		failed := boolPtr(true)
		succeeded := boolPtr(false)
		messages := []conversation.Message{
			callRow(41, "call-a1", "run_tests", `{"suite":"unit"}`, 5),
			outputRow(42, "call-a1", "run_tests", "assertion failed", 5, failed, "assert_failed"),
			callRow(43, "call-a2", "run_tests", `{"suite":"unit"}`, 5),
			outputRow(44, "call-a2", "run_tests", "assertion failed", 5, failed, "assert_failed"),
			callRow(45, "call-a3", "run_tests", `{"suite":"unit"}`, 5),
			outputRow(46, "call-a3", "run_tests", "all green", 5, succeeded, ""),
			// Different arguments: an independent fingerprint that must not
			// inflate the unit-args failure count.
			callRow(47, "call-b1", "run_tests", `{"suite":"integration"}`, 5),
			outputRow(48, "call-b1", "run_tests", "timeout", 5, failed, "timeout"),
			// Same args again after the recovery: a fresh streak.
			callRow(49, "call-a4", "run_tests", `{"suite":"unit"}`, 5),
			outputRow(50, "call-a4", "run_tests", "assertion failed", 5, failed, "assert_failed"),
		}
		renderer := NewEvidenceRenderer()
		units := renderer.Render(messages)
		if len(units) != 5 {
			t.Fatalf("expected 5 exchange units, got %d", len(units))
		}
		first, second, recovered := units[0], units[1], units[2]
		if first.ErrorState != EvidenceErrorStateFailure || second.ErrorState != EvidenceErrorStateFailure {
			t.Fatalf("expected failure states, got %q and %q", first.ErrorState, second.ErrorState)
		}
		if first.ErrorCode != "assert_failed" || second.ErrorCode != "assert_failed" {
			t.Fatalf("expected error codes preserved, got %q and %q", first.ErrorCode, second.ErrorCode)
		}
		if first.FailureCount != 1 || second.FailureCount != 2 {
			t.Fatalf("expected streak counts 1 then 2, got %d and %d", first.FailureCount, second.FailureCount)
		}
		if recovered.ErrorState != EvidenceErrorStateSuccess {
			t.Fatalf("expected success state, got %q", recovered.ErrorState)
		}
		if recovered.FailureCount != 2 {
			t.Fatalf("expected failure_count=2 on the recovering unit, got %d", recovered.FailureCount)
		}
		if !recovered.Recovered {
			t.Fatal("expected recovered=true after two identical failures")
		}
		if !first.Recovered || !second.Recovered {
			t.Fatalf("failure units must reflect the later recovery, got %v and %v", first.Recovered, second.Recovered)
		}
		otherArgs := units[3]
		if otherArgs.FailureCount != 1 || otherArgs.Recovered {
			t.Fatalf("different-args failure must stay independent, got count=%d recovered=%v", otherArgs.FailureCount, otherArgs.Recovered)
		}
		regression := units[4]
		if regression.FailureCount != 1 || regression.Recovered {
			t.Fatalf("post-recovery failure must start a fresh streak, got count=%d recovered=%v", regression.FailureCount, regression.Recovered)
		}
		for _, unit := range units {
			assertTriState(t, unit)
		}
	})

	t.Run("shouldRenderOrphanOutputWithoutPanic", func(t *testing.T) {
		messages := []conversation.Message{
			outputRow(51, "call-orphan", "web_search", "42 results", 6, nil, ""),
		}
		renderer := NewEvidenceRenderer()
		var units []EvidenceUnit
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("renderer panicked on orphan output: %v", recovered)
			}
		}()
		units = renderer.Render(messages)
		if len(units) != 1 {
			t.Fatalf("orphan output must not be dropped, got %d units", len(units))
		}
		unit := units[0]
		if unit.Kind != EvidenceUnitOrphanOutput {
			t.Fatalf("expected orphan output unit, got %q", unit.Kind)
		}
		if unit.ToolCallID != "call-orphan" {
			t.Fatalf("orphan unit lost tool_call_id: %q", unit.ToolCallID)
		}
		if unit.ToolName != "web_search" {
			t.Fatalf("orphan unit lost tool_name: %q", unit.ToolName)
		}
		if unit.Output != "42 results" {
			t.Fatalf("orphan unit lost output content: %q", unit.Output)
		}
		if unit.MessageID != 51 {
			t.Fatalf("orphan unit must anchor on its own row, got message id %d", unit.MessageID)
		}
		assertTriState(t, unit)
	})

	t.Run("shouldExcludeReasoningSystemEchoAndDeveloper", func(t *testing.T) {
		messages := []conversation.Message{
			{
				ImmutableModel: domain.ImmutableModel{ID: 61, OwnerID: 7},
				ConversationID: 3,
				Role:           conversation.RoleAssistant,
				Content:        "thinking about the plan",
				ContentType:    conversation.ContentTypeReasoning,
			},
			{
				ImmutableModel: domain.ImmutableModel{ID: 62, OwnerID: 7},
				ConversationID: 3,
				Role:           conversation.RoleSystem,
				Content:        "echoed system banner",
				ContentType:    conversation.ContentTypeSystemEcho,
			},
			textRow(63, conversation.RoleDeveloper, "developer injected guidance", 7),
			// System-injected content is excluded per the durable evidence spec.
			textRow(64, conversation.RoleSystem, "system injected policy", 7),
		}
		renderer := NewEvidenceRenderer()
		units := renderer.Render(messages)
		if len(units) != 0 {
			t.Fatalf("expected 0 units for reasoning/system_echo/developer rows, got %d", len(units))
		}
	})

	t.Run("shouldPreserveMessageIDOrder", func(t *testing.T) {
		messages := []conversation.Message{
			// Deliberately unordered input rows.
			textRow(74, conversation.RoleAssistant, "final answer", 8),
			outputRow(72, "call-ord", "lookup", "hit", 8, nil, ""),
			callRow(71, "call-ord", "lookup", `{"q":"x"}`, 8),
			textRow(73, conversation.RoleUser, "follow-up", 8),
		}
		renderer := NewEvidenceRenderer()
		units := renderer.Render(messages)
		if len(units) != 3 {
			t.Fatalf("expected 3 units, got %d", len(units))
		}
		wantIDs := []int64{71, 73, 74}
		for index, want := range wantIDs {
			if units[index].MessageID != want {
				t.Fatalf("unit %d anchored on message %d, want %d", index, units[index].MessageID, want)
			}
			if index > 0 && units[index].MessageID <= units[index-1].MessageID {
				t.Fatalf("units not ascending by message id at index %d", index)
			}
		}
	})
}
