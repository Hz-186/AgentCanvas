package memory

import (
	"context"
	"encoding/json"
	"time"
)

type WorkingMemory struct {
	OwnerID        int64              `json:"owner_id"`
	ConversationID int64              `json:"conversation_id"`
	ActiveTask     *WorkingTask       `json:"active_task,omitempty"`
	RecentFacts    []WorkingFact      `json:"recent_facts,omitempty"`
	AttentionFocus string             `json:"attention_focus,omitempty"`
	ContextSummary string             `json:"context_summary,omitempty"`
	EntityMap      map[string]WMEntity `json:"entity_map,omitempty"`
	RoundNumber    int                `json:"round_number"`
	LastUpdated    time.Time          `json:"last_updated"`
}

type WorkingTask struct {
	Goal        string   `json:"goal,omitempty"`
	Subtasks    []string `json:"subtasks,omitempty"`
	CurrentStep string   `json:"current_step,omitempty"`
	Progress    string   `json:"progress,omitempty"`
}

type WorkingFact struct {
	Fact       string  `json:"fact"`
	Confidence float64 `json:"confidence"`
}

type WMEntity struct {
	Role       string `json:"role,omitempty"`
	SkillLevel string `json:"skill_level,omitempty"`
}

func (wm *WorkingMemory) ToContextBlock() string {
	parts := make([]string, 0, 4)
	if wm.ActiveTask != nil && wm.ActiveTask.Goal != "" {
		parts = append(parts, "CURRENT TASK: "+wm.ActiveTask.Goal)
		if wm.ActiveTask.CurrentStep != "" {
			parts = append(parts, "Current step: "+wm.ActiveTask.CurrentStep)
		}
		if wm.ActiveTask.Progress != "" {
			parts = append(parts, "Progress: "+wm.ActiveTask.Progress)
		}
	}
	if wm.AttentionFocus != "" {
		parts = append(parts, "FOCUS: "+wm.AttentionFocus)
	}
	if len(wm.RecentFacts) > 0 {
		facts := make([]string, 0, len(wm.RecentFacts))
		for _, f := range wm.RecentFacts {
			if f.Confidence >= 0.7 {
				facts = append(facts, "- "+f.Fact)
			}
		}
		if len(facts) > 0 {
			parts = append(parts, "KEY FACTS:\n"+stringsJoin(facts, "\n"))
		}
	}
	if wm.ContextSummary != "" {
		parts = append(parts, "RECENT SUMMARY: "+wm.ContextSummary)
	}
	if len(parts) == 0 {
		return ""
	}
	return stringsJoin(parts, "\n\n")
}

func stringsJoin(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}

func (wm *WorkingMemory) IsEmpty() bool {
	return (wm.ActiveTask == nil || wm.ActiveTask.Goal == "") &&
		len(wm.RecentFacts) == 0 &&
		wm.AttentionFocus == "" &&
		wm.ContextSummary == ""
}

type WorkingMemoryRepository interface {
	Get(ctx context.Context, ownerID, conversationID int64) (*WorkingMemory, error)
	Save(ctx context.Context, wm *WorkingMemory) error
	Delete(ctx context.Context, ownerID, conversationID int64) error
}

type WorkingMemoryEvent struct {
	StepType      string          `json:"step_type"`
	Content       string          `json:"content,omitempty"`
	ToolName      string          `json:"tool_name,omitempty"`
	ArgumentsJSON json.RawMessage  `json:"arguments_json,omitempty"`
	OutputJSON    json.RawMessage  `json:"output_json,omitempty"`
}
