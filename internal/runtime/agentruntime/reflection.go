package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/reflection"
	runtimeagent "agentcanvas/internal/runtime/agent"
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

func reflectionAffectsExecution(policy reflection.Policy) bool {
	policy = policy.Normalize()
	return policy.Enabled && policy.RuntimeMode == reflection.RuntimeActive
}

// finalizeReflection is the terminal reflection extraction producer. Runs that
// finish with inline reflection evidence enqueue an ordinary memory write job
// with source reflection; runs without inline evidence produce nothing. The
// legacy reflection analyzer, job queue and recall log resolve are retired.
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
	_ = n.TerminalReflectionWriter.EnqueueTerminalReflection(ctx, memory.TerminalReflectionRequest{
		OwnerID:      rc.OwnerID,
		AgentID:      rc.AgentID,
		RunID:        rc.RunID,
		Task:         task,
		Content:      content,
		EvidenceJSON: evidence,
	})
}

// terminalReflectionContent assembles the durable lesson text from inline
// reflection entries. It is the only place inline runtime signals may become
// memory evidence.
func terminalReflectionContent(items []runtimeagent.InlineReflection) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Lesson)
		if corrective := strings.TrimSpace(item.CorrectiveAction); corrective != "" {
			if text != "" {
				text += "\nCorrective action: " + corrective
			} else {
				text = "Corrective action: " + corrective
			}
		}
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}
