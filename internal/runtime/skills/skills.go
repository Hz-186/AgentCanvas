package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"agentcanvas/internal/runtime/toolruntime"
)

type Descriptor struct {
	Name                   string          `json:"name"`
	Description            string          `json:"description"`
	InputSchema            json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema           json.RawMessage `json:"output_schema,omitempty"`
	RiskLevel              string          `json:"risk_level,omitempty"`
	SideEffect             string          `json:"side_effect,omitempty"`
	RequiresModel          bool            `json:"requires_model"`
	Deterministic          bool            `json:"deterministic"`
	DisableModelInvocation bool            `json:"disable_model_invocation"`
}

type Invocation struct {
	Name      string          `json:"name"`
	InputJSON json.RawMessage `json:"input_json,omitempty"`
}

type Result struct {
	OutputJSON     json.RawMessage `json:"output_json,omitempty"`
	OutputText     string          `json:"output_text,omitempty"`
	ModelInvoked   bool            `json:"model_invoked"`
	ModelTokenCost int             `json:"model_token_cost"`
	Deterministic  bool            `json:"deterministic"`
}

type Skill interface {
	Descriptor() Descriptor
	Invoke(ctx context.Context, input json.RawMessage) (*Result, error)
}

type RuntimeToolSkill struct {
	Tool       toolruntime.RuntimeTool
	RunContext toolruntime.ToolRunContext
}

func NewRuntimeToolSkill(tool toolruntime.RuntimeTool, rc toolruntime.ToolRunContext) RuntimeToolSkill {
	return RuntimeToolSkill{Tool: tool, RunContext: rc}
}

func (s RuntimeToolSkill) Descriptor() Descriptor {
	if s.Tool == nil {
		return Descriptor{}
	}
	metadata := toolruntime.MetadataOf(s.Tool)
	return Descriptor{
		Name:          s.Tool.Name(),
		Description:   s.Tool.Description(),
		InputSchema:   s.Tool.Parameters(),
		RiskLevel:     metadata.RiskLevel,
		SideEffect:    metadata.SideEffect,
		RequiresModel: true,
		Deterministic: false,
	}
}

func (s RuntimeToolSkill) Invoke(ctx context.Context, input json.RawMessage) (*Result, error) {
	if s.Tool == nil {
		return nil, fmt.Errorf("runtime tool skill has no tool")
	}
	result, err := s.Tool.Execute(ctx, s.RunContext, input)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &toolruntime.ToolResult{}
	}
	return &Result{OutputJSON: result.ContentJSON, OutputText: result.ContentText, ModelInvoked: true, Deterministic: false}, nil
}

type DeterministicFunc func(ctx context.Context, input json.RawMessage) (any, error)

type DeterministicSkill struct {
	Desc Descriptor
	Run  DeterministicFunc
}

func (s DeterministicSkill) Descriptor() Descriptor {
	desc := s.Desc
	desc.RequiresModel = false
	desc.Deterministic = true
	desc.DisableModelInvocation = true
	return desc
}

func (s DeterministicSkill) Invoke(ctx context.Context, input json.RawMessage) (*Result, error) {
	if s.Run == nil {
		return nil, fmt.Errorf("deterministic skill %s has no runner", s.Desc.Name)
	}
	out, err := s.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &Result{OutputJSON: data, OutputText: string(data), ModelInvoked: false, ModelTokenCost: 0, Deterministic: true}, nil
}

type Registry struct {
	items map[string]Skill
}

func NewRegistry(items ...Skill) *Registry {
	r := &Registry{items: map[string]Skill{}}
	for _, item := range items {
		r.Register(item)
	}
	return r
}

func (r *Registry) Register(skill Skill) {
	if r == nil || skill == nil {
		return
	}
	name := skill.Descriptor().Name
	if name == "" {
		return
	}
	r.items[name] = skill
}

func (r *Registry) Invoke(ctx context.Context, invocation Invocation) (*Result, error) {
	if r == nil {
		return nil, fmt.Errorf("skill registry is not configured")
	}
	skill := r.items[invocation.Name]
	if skill == nil {
		return nil, fmt.Errorf("skill %s is not registered", invocation.Name)
	}
	return skill.Invoke(ctx, invocation.InputJSON)
}
