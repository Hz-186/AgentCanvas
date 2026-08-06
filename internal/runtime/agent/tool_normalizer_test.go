package agent

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

type normalizerTestTool struct {
	name   string
	schema json.RawMessage
}

func (t normalizerTestTool) Name() string                { return t.name }
func (t normalizerTestTool) Description() string         { return "test" }
func (t normalizerTestTool) Parameters() json.RawMessage { return t.schema }
func (t normalizerTestTool) Execute(context.Context, toolruntime.ToolRunContext, json.RawMessage) (*toolruntime.ToolResult, error) {
	return &toolruntime.ToolResult{ContentText: "executed"}, nil
}

func TestToolCallNormalizerCanonicalizesAliasAndGeneratesUniqueIDs(t *testing.T) {
	normalizer, err := NewToolCallNormalizer([]toolruntime.RuntimeTool{normalizerTestTool{name: "read_file", schema: json.RawMessage(`{"type":"object"}`)}}, map[string]string{"read": "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	items := normalizer.NormalizeBatch([]llm.ToolCall{{Name: " read ", Arguments: json.RawMessage(`{}`)}, {ID: "same", Name: "read", Arguments: json.RawMessage(`{}`)}, {ID: "same", Name: "read", Arguments: json.RawMessage(`{}`)}})
	if len(items) != 3 || items[0].Call.Name != "read_file" || items[0].Call.ID == "" {
		t.Fatalf("unexpected normalized calls: %+v", items)
	}
	if items[1].Call.ID != "same" || items[2].Call.ID != "same_2" {
		t.Fatalf("duplicate IDs were not repaired: %q/%q", items[1].Call.ID, items[2].Call.ID)
	}
}

func TestToolCallNormalizerRejectsInvalidArgumentsBeforeExecute(t *testing.T) {
	normalizer, err := NewToolCallNormalizer([]toolruntime.RuntimeTool{normalizerTestTool{name: "write", schema: json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}},"additionalProperties":false}`)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	item := normalizer.NormalizeBatch([]llm.ToolCall{{Name: "write", Arguments: json.RawMessage(`{"extra":true}`)}})[0]
	if item.Issue == nil || item.Issue.Code != ToolCallIssueInvalidArguments {
		t.Fatalf("expected invalid args, got %+v", item.Issue)
	}
	if item.Call.ID == "" {
		t.Fatal("invalid calls still need a stable call id")
	}
}

func TestToolBatchPlannerAndExecutorKeepOrder(t *testing.T) {
	tools := []toolruntime.RuntimeTool{normalizerTestTool{name: "read", schema: json.RawMessage(`{"type":"object"}`)}, normalizerTestTool{name: "write", schema: json.RawMessage(`{"type":"object"}`)}}
	normalizer, err := NewToolCallNormalizer(tools, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := normalizer.NormalizeBatch([]llm.ToolCall{{Name: "read", Arguments: json.RawMessage(`{}`)}, {Name: "write", Arguments: json.RawMessage(`{}`)}, {Name: "read", Arguments: json.RawMessage(`{}`)}})
	calls[1].Metadata.SideEffect = toolruntime.SideEffectWrite
	segments := PlanToolBatch(calls, nil)
	if len(segments) != 3 || segments[0].Parallel || segments[1].Parallel || segments[2].Parallel {
		t.Fatalf("single-item segments must be serial: %+v", segments)
	}
	results := ExecuteToolBatch(context.Background(), segments, 2, func(_ context.Context, item ToolBatchItem) (*toolruntime.ToolResult, error) {
		return &toolruntime.ToolResult{ContentText: item.Call.Call.Name}, nil
	})
	if len(results) != 3 || results[0].Index != 0 || results[1].Index != 1 || results[2].Index != 2 {
		t.Fatalf("results lost source order: %+v", results)
	}
}

func TestToolBatchPlannerParallelizesDelegationsButIsolatesExternalActions(t *testing.T) {
	calls := []NormalizedToolCall{
		{Call: llm.ToolCall{Name: "delegate_a"}, Metadata: toolruntime.ToolMetadata{SideEffect: toolruntime.SideEffectExternalAction, ExecutionClass: toolruntime.ExecutionDelegation}},
		{Call: llm.ToolCall{Name: "delegate_b"}, Metadata: toolruntime.ToolMetadata{SideEffect: toolruntime.SideEffectExternalAction, ExecutionClass: toolruntime.ExecutionDelegation}},
		{Call: llm.ToolCall{Name: "http_a"}, Metadata: toolruntime.ToolMetadata{SideEffect: toolruntime.SideEffectExternalAction, ExecutionClass: toolruntime.ExecutionSerial}},
		{Call: llm.ToolCall{Name: "http_b"}, Metadata: toolruntime.ToolMetadata{SideEffect: toolruntime.SideEffectExternalAction, ExecutionClass: toolruntime.ExecutionSerial}},
	}

	segments := PlanToolBatch(calls, nil)
	if len(segments) != 3 {
		t.Fatalf("expected one delegation segment and two isolated external actions, got %+v", segments)
	}
	if !segments[0].Parallel || len(segments[0].Items) != 2 {
		t.Fatalf("delegations should share a parallel segment: %+v", segments[0])
	}
	if segments[1].Parallel || segments[2].Parallel || len(segments[1].Items) != 1 || len(segments[2].Items) != 1 {
		t.Fatalf("ordinary external actions must remain isolated: %+v", segments[1:])
	}
}
