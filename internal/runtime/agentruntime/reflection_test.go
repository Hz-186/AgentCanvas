package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
	runtimeagent "agentcanvas/internal/runtime/agent"
	runtimeevent "agentcanvas/internal/runtime/event"
)

// terminalReflectionFakeWriter records enqueue calls and returns a fixed
// error, mirroring the memory.TerminalReflectionWriter surface the runtime
// consumes.
type terminalReflectionFakeWriter struct {
	calls int
	last  memory.TerminalReflectionRequest
	err   error
}

func (w *terminalReflectionFakeWriter) EnqueueTerminalReflection(_ context.Context, req memory.TerminalReflectionRequest) error {
	w.calls++
	w.last = req
	return w.err
}

func terminalReflectionFixture() (runtimeagent.InlineReflection, *runtimeagent.RunResult) {
	item := runtimeagent.InlineReflection{
		Action:           "adjust",
		RootCause:        "stale cache entry was served instead of a fresh lookup",
		CorrectiveAction: "invalidate the cache before retrying the lookup",
		Lesson:           "always verify cache freshness before relying on cached tool output",
		Applicability:    "any run that reads cache-backed retrieval tools",
	}
	result := &runtimeagent.RunResult{
		StopReason: runtimeagent.StopReasonFinalAnswer,
		Reflection: runtimeagent.ReflectionTrace{Inline: []runtimeagent.InlineReflection{item}},
	}
	return item, result
}

// capturePackageLogs redirects the package-level slog default logger into a
// buffer for the duration of the test and restores it afterwards.
func capturePackageLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buffer
}

func decodeLogRecords(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	records := make([]map[string]any, 0)
	for _, line := range bytes.Split(buffer.Bytes(), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("log line is not valid JSON: %q (%v)", line, err)
		}
		records = append(records, record)
	}
	return records
}

func warnRecords(records []map[string]any) []map[string]any {
	filtered := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if level, _ := record["level"].(string); level == "WARN" {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func TestTerminalReflectionContent(t *testing.T) {
	t.Run("shouldPersistRootCauseAndApplicability", func(t *testing.T) {
		item, _ := terminalReflectionFixture()

		content := terminalReflectionContent([]runtimeagent.InlineReflection{item})

		sections := []string{
			"Root cause: " + item.RootCause,
			"Corrective action: " + item.CorrectiveAction,
			"Lesson: " + item.Lesson,
			"Applicability: " + item.Applicability,
		}
		positions := make([]int, 0, len(sections))
		for _, section := range sections {
			position := strings.Index(content, section)
			if position < 0 {
				t.Fatalf("content = %q, want section %q", content, section)
			}
			positions = append(positions, position)
		}
		for i := 1; i < len(positions); i++ {
			if positions[i] < positions[i-1] {
				t.Fatalf("content = %q, want stable section order root cause, corrective action, lesson, applicability", content)
			}
		}
	})

	t.Run("shouldSkipBlankSectionsAndEmptyEntries", func(t *testing.T) {
		partial := runtimeagent.InlineReflection{Lesson: "only the lesson survives"}
		blank := runtimeagent.InlineReflection{Action: "adjust"}

		content := terminalReflectionContent([]runtimeagent.InlineReflection{blank, partial})

		if content != "Lesson: only the lesson survives" {
			t.Fatalf("content = %q, want only the non-empty lesson section of the non-empty entry", content)
		}
	})
}

func TestTerminalReflectionEnqueue(t *testing.T) {
	newFailureFixture := func(t *testing.T) (*terminalReflectionFakeWriter, runtimeCore, *RunContext, *runtimeagent.RunResult) {
		t.Helper()
		writer := &terminalReflectionFakeWriter{err: errors.New("write pipeline unavailable")}
		core := runtimeCore{coreRepositories: coreRepositories{TerminalReflectionWriter: writer}}
		rc := &RunContext{OwnerID: 11, AgentID: 22, RunID: 33, Events: &capturedRuntimeEvents{}}
		_, result := terminalReflectionFixture()
		return writer, core, rc, result
	}

	t.Run("shouldLogWarningWithRunContextOnFailure", func(t *testing.T) {
		writer, core, rc, result := newFailureFixture(t)
		buffer := capturePackageLogs(t)
		// No event emitter is attached: the log channel must stand alone.
		rc.Events = nil

		core.finalizeReflection(context.Background(), rc, "fixture task", result, reflection.DefaultPolicy())

		if writer.calls != 1 {
			t.Fatalf("enqueue calls = %d, want 1", writer.calls)
		}
		warns := warnRecords(decodeLogRecords(t, buffer))
		if len(warns) != 1 {
			t.Fatalf("warn records = %d (%s), want exactly 1", len(warns), buffer.String())
		}
		record := warns[0]
		if runID, _ := record["run_id"].(float64); runID != 33 {
			t.Fatalf("warn record run_id = %v, want 33", record["run_id"])
		}
		if agentID, _ := record["agent_id"].(float64); agentID != 22 {
			t.Fatalf("warn record agent_id = %v, want 22", record["agent_id"])
		}
	})

	t.Run("shouldEmitAgentStepWarningEventOnFailure", func(t *testing.T) {
		writer, core, rc, result := newFailureFixture(t)
		events := rc.Events.(*capturedRuntimeEvents)

		core.finalizeReflection(context.Background(), rc, "fixture task", result, reflection.DefaultPolicy())

		if writer.calls != 1 {
			t.Fatalf("enqueue calls = %d, want 1", writer.calls)
		}
		if len(events.events) != 1 {
			t.Fatalf("emitted %d events, want exactly 1 warning event: %#v", len(events.events), events.events)
		}
		event := events.events[0]
		if event.Type != runtimeevent.AgentStep || event.RunID != 33 {
			t.Fatalf("warning event = %#v, want agent_step event for run 33", event)
		}
		if stepType, _ := event.Payload["type"].(string); stepType != runtimeagent.StepTypeError {
			t.Fatalf("warning payload type = %v, want %q", event.Payload["type"], runtimeagent.StepTypeError)
		}
		message, _ := event.Payload["error"].(string)
		if !strings.Contains(message, "terminal reflection") || !strings.Contains(message, "enqueue") {
			t.Fatalf("warning payload error = %q, want it to name the terminal reflection enqueue failure", message)
		}
	})

	t.Run("shouldKeepRunSuccessfulOnEnqueueError", func(t *testing.T) {
		writer, core, rc, result := newFailureFixture(t)
		_ = capturePackageLogs(t)

		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("finalizeReflection panicked on enqueue error: %v", recovered)
				}
			}()
			core.finalizeReflection(context.Background(), rc, "fixture task", result, reflection.DefaultPolicy())
		}()

		if writer.calls != 1 {
			t.Fatalf("enqueue calls = %d, want the error path to be reached exactly once", writer.calls)
		}
		// finalizeReflection has no error return: the enqueue failure can
		// only surface through the warn log and the AgentStep event, never
		// through the run result.
	})

	t.Run("shouldStayQuietOnSuccessfulEnqueue", func(t *testing.T) {
		writer := &terminalReflectionFakeWriter{}
		core := runtimeCore{coreRepositories: coreRepositories{TerminalReflectionWriter: writer}}
		events := &capturedRuntimeEvents{}
		rc := &RunContext{OwnerID: 11, AgentID: 22, RunID: 33, Events: events}
		buffer := capturePackageLogs(t)
		_, result := terminalReflectionFixture()

		core.finalizeReflection(context.Background(), rc, "fixture task", result, reflection.DefaultPolicy())

		if writer.calls != 1 {
			t.Fatalf("enqueue calls = %d, want 1", writer.calls)
		}
		if warns := warnRecords(decodeLogRecords(t, buffer)); len(warns) != 0 {
			t.Fatalf("warn records = %d (%s), want none on successful enqueue", len(warns), buffer.String())
		}
		if len(events.events) != 0 {
			t.Fatalf("emitted %d events, want none on successful enqueue: %#v", len(events.events), events.events)
		}
	})

	t.Run("shouldStillSkipWaitingHumanAndPaused", func(t *testing.T) {
		for _, stopReason := range []string{runtimeagent.StopReasonWaitingHuman, runtimeagent.StopReasonPaused} {
			writer := &terminalReflectionFakeWriter{err: errors.New("must not be reached")}
			core := runtimeCore{coreRepositories: coreRepositories{TerminalReflectionWriter: writer}}
			rc := &RunContext{OwnerID: 11, AgentID: 22, RunID: 33}
			_, result := terminalReflectionFixture()
			result.StopReason = stopReason

			core.finalizeReflection(context.Background(), rc, "fixture task", result, reflection.DefaultPolicy())

			if writer.calls != 0 {
				t.Errorf("stop reason %q: enqueue calls = %d, want 0", stopReason, writer.calls)
			}
		}
	})
}
