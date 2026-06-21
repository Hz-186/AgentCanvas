package node

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type JSONOutputNode struct{}

type jsonOutputConfig struct {
	Value  string          `json:"value"`
	Schema json.RawMessage `json:"schema"`
}

func (JSONOutputNode) Type() string { return "json_output" }

func (JSONOutputNode) Validate(config json.RawMessage) error {
	var cfg jsonOutputConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid json_output config", agenterrors.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.Value) == "" {
		return fmt.Errorf("%w: json_output value is required", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (JSONOutputNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	var cfg jsonOutputConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(engine.ResolveTemplate(cfg.Value, rc))
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("%w: json_output value is not valid json", agenterrors.ErrInvalidInput)
	}
	if err := validateSimpleJSONSchema(cfg.Schema, parsed); err != nil {
		return nil, fmt.Errorf("%w: %v", agenterrors.ErrInvalidInput, err)
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.JSONOutputValidated, RunID: rc.RunID})
	return engine.NodeOutput{"json": parsed, "raw": raw}, nil
}

type GuardrailNode struct{}

type guardrailConfig struct {
	Source          string          `json:"source"`
	MaxLength       int             `json:"max_length"`
	BannedTerms     []string        `json:"banned_terms"`
	RequireCitation bool            `json:"require_citation"`
	RequireJSON     bool            `json:"require_json"`
	Schema          json.RawMessage `json:"schema"`
}

var apiKeyPattern = regexp.MustCompile(`(?i)(sk-[a-z0-9_\-]{12,}|api[_-]?key\s*[:=]\s*['"]?[a-z0-9_\-]{12,})`)

func (GuardrailNode) Type() string { return "guardrail" }

func (GuardrailNode) Validate(config json.RawMessage) error {
	var cfg guardrailConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid guardrail config", agenterrors.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.Source) == "" {
		return fmt.Errorf("%w: guardrail source is required", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxLength < 0 {
		return fmt.Errorf("%w: guardrail max_length is invalid", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (GuardrailNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	var cfg guardrailConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	content := engine.ResolveTemplate(cfg.Source, rc)
	violations := make([]string, 0)
	if cfg.MaxLength > 0 && len([]rune(content)) > cfg.MaxLength {
		violations = append(violations, "output_too_long")
	}
	lower := strings.ToLower(content)
	for _, term := range cfg.BannedTerms {
		term = strings.TrimSpace(strings.ToLower(term))
		if term != "" && strings.Contains(lower, term) {
			violations = append(violations, "banned_term:"+term)
		}
	}
	if strings.Contains(lower, "system prompt") || strings.Contains(lower, "系统提示词") {
		violations = append(violations, "system_prompt_leak")
	}
	if apiKeyPattern.MatchString(content) {
		violations = append(violations, "secret_leak")
	}
	if cfg.RequireCitation && !strings.Contains(content, "[") {
		violations = append(violations, "citation_required")
	}
	var parsed any
	if cfg.RequireJSON || len(cfg.Schema) > 0 {
		if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &parsed); err != nil {
			violations = append(violations, "invalid_json")
		} else if err := validateSimpleJSONSchema(cfg.Schema, parsed); err != nil {
			violations = append(violations, "json_schema:"+err.Error())
		}
	}
	if len(violations) > 0 {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.GuardrailBlocked, RunID: rc.RunID, Payload: map[string]any{"violations": violations}})
		return nil, fmt.Errorf("%w: guardrail blocked output: %s", agenterrors.ErrInvalidInput, strings.Join(violations, ", "))
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.GuardrailPassed, RunID: rc.RunID})
	return engine.NodeOutput{"content": content, "passed": true}, nil
}
