package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// ErrToolStreamingUnsupported allows a wrapper or provider adapter to opt out
// of streaming for one configuration without losing the existing
// ChatWithTools compatibility path. Implementations must return it before
// emitting any stream events.
var ErrToolStreamingUnsupported = errors.New("tool streaming is not supported")

// ErrContextWindowExceeded is returned for provider responses that reject a
// request because its prompt is larger than the model context window.
var ErrContextWindowExceeded = errors.New("context window exceeded")

// ErrRateLimited lets goal accounting stop automatic continuation without
// confusing a provider quota/rate response with an irrecoverable task block.
var ErrRateLimited = errors.New("provider rate limited")

// ModelStreamKind is the provider-independent vocabulary emitted by a
// ToolStreamingClient.  Consumers should switch on these semantic events
// instead of provider-specific JSON field names.
type ModelStreamKind string

const (
	ModelTextStart         ModelStreamKind = "text.start"
	ModelTextDelta         ModelStreamKind = "text.delta"
	ModelTextEnd           ModelStreamKind = "text.end"
	ModelReasoningStart    ModelStreamKind = "reasoning.start"
	ModelReasoningDelta    ModelStreamKind = "reasoning.delta"
	ModelReasoningEnd      ModelStreamKind = "reasoning.end"
	ModelProposedPlanStart ModelStreamKind = "plan.start"
	ModelProposedPlanDelta ModelStreamKind = "plan.delta"
	ModelProposedPlanEnd   ModelStreamKind = "plan.end"
	ModelToolCallStart     ModelStreamKind = "tool_call.start"
	ModelToolCallDelta     ModelStreamKind = "tool_call.delta"
	ModelToolCallEnd       ModelStreamKind = "tool_call.end"
	ModelUsage             ModelStreamKind = "usage"
	ModelDone              ModelStreamKind = "done"
	ModelError             ModelStreamKind = "error"
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

const (
	proposedPlanOpenTag  = "<proposed_plan>"
	proposedPlanCloseTag = "</proposed_plan>"
)

// ProposedPlanStreamParser removes exclusive-line proposed-plan blocks from
// assistant text while exposing the block as its own stream vocabulary. It
// buffers only the current line, so tags split across provider chunks remain
// deterministic without a second transcript parser.
type ProposedPlanStreamParser struct {
	line     strings.Builder
	visible  strings.Builder
	inside   bool
	plan     strings.Builder
	lastPlan string
	sawPlan  bool
}

func (p *ProposedPlanStreamParser) Push(text string) []ModelStreamEvent {
	if p == nil || text == "" {
		return nil
	}
	var events []ModelStreamEvent
	for _, r := range text {
		p.line.WriteRune(r)
		if r != '\n' {
			continue
		}
		events = append(events, p.flushLine()...)
	}
	return events
}

func (p *ProposedPlanStreamParser) Finish() []ModelStreamEvent {
	if p == nil {
		return nil
	}
	var events []ModelStreamEvent
	if p.line.Len() > 0 {
		events = append(events, p.flushLine()...)
	}
	if p.inside {
		p.inside = false
		p.lastPlan = p.plan.String()
		events = append(events, ModelStreamEvent{Kind: ModelProposedPlanEnd})
	}
	return events
}

func (p *ProposedPlanStreamParser) flushLine() []ModelStreamEvent {
	line := p.line.String()
	p.line.Reset()
	trimmed := strings.TrimSuffix(line, "\n")
	trimmed = strings.TrimSuffix(trimmed, "\r")
	if !p.inside && trimmed == proposedPlanOpenTag {
		p.inside = true
		p.sawPlan = true
		p.plan.Reset()
		return []ModelStreamEvent{{Kind: ModelProposedPlanStart}}
	}
	if p.inside && trimmed == proposedPlanCloseTag {
		p.inside = false
		p.lastPlan = p.plan.String()
		return []ModelStreamEvent{{Kind: ModelProposedPlanEnd}}
	}
	if p.inside {
		p.plan.WriteString(line)
		return []ModelStreamEvent{{Kind: ModelProposedPlanDelta, Text: line}}
	}
	p.visible.WriteString(line)
	return []ModelStreamEvent{{Kind: ModelTextDelta, Text: line}}
}

func (p *ProposedPlanStreamParser) VisiblePlan() (string, string) {
	if p == nil {
		return "", ""
	}
	return p.visible.String(), p.lastPlan
}

// NormalizeProposedPlan removes plan blocks from a completed assistant
// message and returns the final block (the last block wins, matching Codex).
func NormalizeProposedPlan(text string) (visible, plan string) {
	parser := &ProposedPlanStreamParser{}
	var visibleBuilder strings.Builder
	for _, event := range append(parser.Push(text), parser.Finish()...) {
		if event.Kind == ModelTextDelta {
			visibleBuilder.WriteString(event.Text)
		}
	}
	return visibleBuilder.String(), parser.lastPlan
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
