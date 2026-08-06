package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolStreamAccumulator rebuilds the final assistant message from streamed
// deltas.  A provider can split a function name or its JSON arguments across
// any number of chunks, and can interleave multiple tool indexes; this type
// keeps those concerns out of the stream parser and its consumers.
type ToolStreamAccumulator struct {
	content   strings.Builder
	usage     Usage
	toolCalls map[int]*streamToolCall
	toolOrder []int
}

type streamToolCall struct {
	id        string
	typ       string
	name      string
	arguments strings.Builder
}

// NewToolStreamAccumulator returns an empty stream accumulator.
func NewToolStreamAccumulator() *ToolStreamAccumulator {
	return &ToolStreamAccumulator{toolCalls: make(map[int]*streamToolCall)}
}

// AddText appends an assistant text fragment.
func (a *ToolStreamAccumulator) AddText(text string) {
	if a == nil || text == "" {
		return
	}
	a.content.WriteString(text)
}

// AddUsage records the latest usage report.  OpenAI-compatible providers
// normally send one usage-only chunk at the end, but accepting every report
// makes this safe for providers that report usage incrementally.
func (a *ToolStreamAccumulator) AddUsage(usage Usage) {
	if a == nil {
		return
	}
	a.usage = usage
}

// AddToolCallDelta appends one streamed function-call fragment.  Name and ID
// are assigned when present (rather than concatenated), because some
// providers repeat the complete function name in more than one chunk.
func (a *ToolStreamAccumulator) AddToolCallDelta(index int, callID, name, argumentDelta string) {
	if a == nil {
		return
	}
	if a.toolCalls == nil {
		a.toolCalls = make(map[int]*streamToolCall)
	}
	call, ok := a.toolCalls[index]
	if !ok {
		call = &streamToolCall{typ: "function"}
		a.toolCalls[index] = call
		a.toolOrder = append(a.toolOrder, index)
	}
	if callID != "" {
		call.id = callID
	}
	if name != "" {
		call.name = name
	}
	if argumentDelta != "" {
		call.arguments.WriteString(argumentDelta)
	}
}

// AddToolCall is a convenience alias for AddToolCallDelta when a caller has
// one complete fragment available.  It intentionally has the same merge
// semantics as streamed deltas.
func (a *ToolStreamAccumulator) AddToolCall(index int, callID, name, argumentDelta string) {
	a.AddToolCallDelta(index, callID, name, argumentDelta)
}

// ToolCall returns a validated, complete ToolCall for index.  Empty argument
// streams are represented as an empty JSON object, matching the OpenAI
// function-calling convention.
func (a *ToolStreamAccumulator) ToolCall(index int) (ToolCall, error) {
	if a == nil {
		return ToolCall{}, fmt.Errorf("tool stream accumulator is nil")
	}
	call, ok := a.toolCalls[index]
	if !ok {
		return ToolCall{}, fmt.Errorf("tool call index %d is not present", index)
	}
	arguments, err := validToolArguments(call.arguments.String())
	if err != nil {
		return ToolCall{}, fmt.Errorf("tool call index %d: %w", index, err)
	}
	typ := call.typ
	if typ == "" {
		typ = "function"
	}
	return ToolCall{
		ID:        call.id,
		Type:      typ,
		Name:      call.name,
		Arguments: arguments,
	}, nil
}

// ToolCalls finalizes calls in provider index order. Chunks may arrive
// interleaved, but index is the provider's stable position for a call and is
// therefore the only ordering that remains deterministic across transports.
func (a *ToolStreamAccumulator) ToolCalls() ([]ToolCall, error) {
	if a == nil {
		return nil, fmt.Errorf("tool stream accumulator is nil")
	}
	if len(a.toolOrder) == 0 {
		return nil, nil
	}
	indexes := a.orderedToolIndexes()
	out := make([]ToolCall, 0, len(indexes))
	for _, index := range indexes {
		call, err := a.ToolCall(index)
		if err != nil {
			return nil, err
		}
		out = append(out, call)
	}
	return out, nil
}

func (a *ToolStreamAccumulator) orderedToolIndexes() []int {
	if a == nil || len(a.toolOrder) == 0 {
		return nil
	}
	indexes := append([]int(nil), a.toolOrder...)
	sort.Ints(indexes)
	return indexes
}

// Response finalizes the stream into the same shape returned by
// ChatWithTools.  Reasoning is deliberately not included in the assistant
// message; it is exposed only through ModelReasoning* events.
func (a *ToolStreamAccumulator) Response() (*ToolChatResponse, error) {
	if a == nil {
		return nil, fmt.Errorf("tool stream accumulator is nil")
	}
	calls, err := a.ToolCalls()
	if err != nil {
		return nil, err
	}
	return &ToolChatResponse{
		Message: ChatMessage{
			Role:      "assistant",
			Content:   a.content.String(),
			ToolCalls: calls,
		},
		Usage: a.usage,
	}, nil
}

// Content returns the accumulated assistant text.
func (a *ToolStreamAccumulator) Content() string {
	if a == nil {
		return ""
	}
	return a.content.String()
}

// Usage returns the latest usage report.
func (a *ToolStreamAccumulator) Usage() Usage {
	if a == nil {
		return Usage{}
	}
	return a.usage
}

func validToolArguments(raw string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace([]byte(raw))
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("invalid JSON arguments")
	}
	return json.RawMessage(trimmed), nil
}
