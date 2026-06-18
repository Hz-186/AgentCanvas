package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type MessageNode struct {
	Writer MessageWriter
}

type messageConfig struct {
	Content      string `json:"content"`
	WithCitation bool   `json:"with_citation"`
}

func (MessageNode) Type() string { return "message" }

func (MessageNode) Validate(config json.RawMessage) error {
	var cfg messageConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid message config", agenterrors.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.Content) == "" {
		return fmt.Errorf("%w: message content is required", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n MessageNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	var cfg messageConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	content := engine.ResolveTemplate(cfg.Content, rc)
	var messageID int64
	if n.Writer != nil && strings.TrimSpace(content) != "" {
		id, err := n.Writer.WriteAssistantMessage(ctx, rc.OwnerID, rc.ConversationID, rc.RunID, content, 0)
		if err != nil {
			return nil, err
		}
		messageID = id
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MessageCreated, RunID: rc.RunID, NodeType: n.Type(), Payload: map[string]any{"message_id": messageID}})
	return engine.NodeOutput{"content": content, "message_id": messageID}, nil
}
