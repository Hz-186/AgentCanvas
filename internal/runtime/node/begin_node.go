package node

import (
	"context"
	"encoding/json"

	"agentcanvas/internal/runtime/engine"
)

type BeginNode struct{}

func (BeginNode) Type() string { return "begin" }

func (BeginNode) Validate(config json.RawMessage) error { return nil }

func (BeginNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	output := engine.NodeOutput{}
	for key, value := range rc.Input {
		output[key] = value
	}
	return output, nil
}
