package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/runtime/engine"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type PromptNode struct{}

type promptConfig struct {
	Template string `json:"template"`
}

func (PromptNode) Type() string { return "prompt" }

func (PromptNode) Validate(config json.RawMessage) error {
	var cfg promptConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid prompt config", agenterrors.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.Template) == "" {
		return fmt.Errorf("%w: prompt template is required", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (PromptNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	var cfg promptConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	return engine.NodeOutput{"prompt": engine.ResolveTemplate(cfg.Template, rc)}, nil
}
