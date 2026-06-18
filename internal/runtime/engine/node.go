package engine

import (
	"context"
	"encoding/json"
)

type Node interface {
	Type() string
	Validate(config json.RawMessage) error
	Run(ctx context.Context, rc *RunContext, input NodeInput, config json.RawMessage) (NodeOutput, error)
}
