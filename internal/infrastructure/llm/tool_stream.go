package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrToolStreamingUnsupported allows a wrapper or provider adapter to opt out
// of streaming for one configuration without losing the existing
// ChatWithTools compatibility path. Implementations must return it before
// emitting any stream events.
var ErrToolStreamingUnsupported = errors.New("tool streaming is not supported")

// ModelStreamKind is the provider-independent vocabulary emitted by a
// ToolStreamingClient.  Consumers should switch on these semantic events
// instead of provider-specific JSON field names.
type ModelStreamKind string

const (
	ModelTextStart      ModelStreamKind = "text.start"
	ModelTextDelta      ModelStreamKind = "text.delta"
	ModelTextEnd        ModelStreamKind = "text.end"
	ModelReasoningStart ModelStreamKind = "reasoning.start"
	ModelReasoningDelta ModelStreamKind = "reasoning.delta"
	ModelReasoningEnd   ModelStreamKind = "reasoning.end"
	ModelToolCallStart  ModelStreamKind = "tool_call.start"
	ModelToolCallDelta  ModelStreamKind = "tool_call.delta"
	ModelToolCallEnd    ModelStreamKind = "tool_call.end"
	ModelUsage          ModelStreamKind = "usage"
	ModelDone           ModelStreamKind = "done"
	ModelError          ModelStreamKind = "error"
)

// ModelStreamEvent is the provider-neutral representation of a streamed
// model response.  ArgumentDelta intentionally contains an arbitrary JSON
// fragment; only ToolCallEnd events carry validated, complete Arguments.
type ModelStreamEvent struct {
	Kind          ModelStreamKind `json:"kind"`
	Index         int             `json:"index,omitempty"`
	CallID        string          `json:"call_id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Text          string          `json:"text,omitempty"`
	ArgumentDelta string          `json:"argument_delta,omitempty"`
	Arguments     json.RawMessage `json:"arguments,omitempty"`
	Usage         Usage           `json:"usage,omitempty"`
	Err           error           `json:"-"`
}

// ToolStreamingClient exposes a provider response before the complete model
// turn has arrived.  Implementations must emit the first delta as soon as it
// is decoded from the HTTP body, rather than waiting for EOF.
type ToolStreamingClient interface {
	StreamChatWithTools(
		ctx context.Context,
		cfg ChatProviderConfig,
		req ToolChatRequest,
		onEvent func(ModelStreamEvent) error,
	) (*ToolChatResponse, error)
}
