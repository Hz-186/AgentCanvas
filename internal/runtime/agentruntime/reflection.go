package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
	runtimeagent "agentcanvas/internal/runtime/agent"
	runtimeevent "agentcanvas/internal/runtime/event"
)

func effectiveReflectionPolicy(cfg agentRuntimeConfig) (reflection.Policy, error) {
	policy := reflection.DefaultPolicy()
	raw := bytes.TrimSpace(cfg.ReflectionPolicyJSON)
	if len(raw) > 0 && string(raw) != "{}" && string(raw) != "null" {
		if err := json.Unmarshal(raw, &policy); err != nil {
			return reflection.Policy{}, err
		}
	}
	if err := policy.Validate(); err != nil {
		return reflection.Policy{}, err
	}
	policy = policy.Normalize()
	return policy, nil
}

// finalizeReflection is the terminal reflection extraction producer. Runs that
// finish with inline reflection evidence enqueue an ordinary memory write job
// with source reflection; runs without inline evidence produce nothing. The
// legacy reflection analyzer, job queue and recall log resolve are retired.
//
// A failed enqueue never fails the run: the failure is made observable via a
// structured warning log and one best-effort AgentStep error event, which are
// independent channels emitted after the warn log is already written.
func (n runtimeCore) finalizeReflection(ctx context.Context, rc *RunContext, task string, result *runtimeagent.RunResult, policy reflection.Policy) {
	if n.TerminalReflectionWriter == nil || rc == nil || result == nil || !policy.Active() {
		return
	}
	if result.StopReason == runtimeagent.StopReasonWaitingHuman || result.StopReason == runtimeagent.StopReasonPaused {
		return
	}
	if !policy.TerminalAsync {
		return
	}
	content := terminalReflectionContent(result.Reflection.Inline)
	if strings.TrimSpace(content) == "" {
		return
	}
	evidence, _ := json.Marshal(map[string]any{
		"task":              task,
		"stop_reason":       result.StopReason,
		"final_answer":      result.FinalAnswer,
		"steps":             runtimeagent.CompactSteps(result.Steps, 4096),
		"reflection_trace":  result.Reflection,
		"reflection_policy": policy,
	})
	// An enqueue failure must never fail the run: the write is a best-effort
	// extraction producer. The warn log and the AgentStep error event are two
	// independent best-effort observability channels; the log is emitted
	// first, so a warning record exists even if event emission fails.
	if err := n.TerminalReflectionWriter.EnqueueTerminalReflection(ctx, memory.TerminalReflectionRequest{
		OwnerID:      rc.OwnerID,
		AgentID:      rc.AgentID,
		RunID:        rc.RunID,
		Task:         task,
		Content:      content,
		EvidenceJSON: evidence,
	}); err != nil {
		slog.Warn("terminal reflection enqueue failed",
			"owner_id", rc.OwnerID,
			"agent_id", rc.AgentID,
			"run_id", rc.RunID,
			"error", err.Error(),
		)
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.AgentStep, RunID: rc.RunID,
			Payload: map[string]any{"type": runtimeagent.StepTypeError, "error": "enqueue terminal reflection: " + err.Error()}})
	}
}

// terminalReflectionContent assembles the durable lesson text from inline
// reflection entries. Each entry contributes up to four labeled sections in a
// stable order — root cause, corrective action, lesson, applicability — with
// empty sections skipped and entries whose four sections are all empty
// dropped entirely. Entries stay separated by blank lines. It is the only
// place inline runtime signals may become memory evidence.
func terminalReflectionContent(items []runtimeagent.InlineReflection) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		sections := make([]string, 0, 4)
		if rootCause := strings.TrimSpace(item.RootCause); rootCause != "" {
			sections = append(sections, "Root cause: "+rootCause)
		}
		if corrective := strings.TrimSpace(item.CorrectiveAction); corrective != "" {
			sections = append(sections, "Corrective action: "+corrective)
		}
		if lesson := strings.TrimSpace(item.Lesson); lesson != "" {
			sections = append(sections, "Lesson: "+lesson)
		}
		if applicability := strings.TrimSpace(item.Applicability); applicability != "" {
			sections = append(sections, "Applicability: "+applicability)
		}
		if len(sections) == 0 {
			continue
		}
		parts = append(parts, strings.Join(sections, "\n"))
	}
	return strings.Join(parts, "\n\n")
}
