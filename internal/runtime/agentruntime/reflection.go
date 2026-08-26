package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"agentcanvas/internal/domain"
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

func reflectionLessonIDs(items []reflection.RecalledLesson) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		if item.ID > 0 {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (n runtimeCore) finalizeReflection(ctx context.Context, rc *RunContext, cfg agentRuntimeConfig, loaded *LoadedProvider, task string, result *runtimeagent.RunResult, policy reflection.Policy) {
	if n.Reflections == nil || rc == nil || loaded == nil || result == nil || !policy.Active() {
		return
	}
	if result.StopReason == runtimeagent.StopReasonWaitingHuman || result.StopReason == runtimeagent.StopReasonPaused {
		return
	}
	outcome := result.StopReason
	if result.StopReason == runtimeagent.StopReasonFinalAnswer {
		outcome = "succeeded"
	}
	n.Reflections.ResolveRun(ctx, rc.OwnerID, rc.RunID, outcome)
	if !policy.TerminalAsync {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"task": task, "stop_reason": result.StopReason, "final_answer": result.FinalAnswer,
		"steps": runtimeagent.CompactSteps(result.Steps, 4096), "reflection_trace": result.Reflection,
		"reflection_policy": policy,
	})
	providerID, model := loaded.ProviderID, loaded.Model
	if policy.ProviderID > 0 {
		providerID = policy.ProviderID
	}
	if strings.TrimSpace(policy.Model) != "" {
		model = strings.TrimSpace(policy.Model)
	}
	_ = n.Reflections.Enqueue(ctx, &reflection.Job{BaseModel: domain.BaseModel{OwnerID: rc.OwnerID}, AgentID: rc.AgentID, RunID: rc.RunID,
		ProviderID: providerID, Model: model, Mode: agentMode(cfg.Mode), Task: task,
		PayloadJSON: payload, Status: reflection.JobPending, MaxAttempts: 3})
}
