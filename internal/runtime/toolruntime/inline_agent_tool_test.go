package toolruntime

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeInlineAgentCaller struct {
	req InlineAgentCallRequest
}

func (f *fakeInlineAgentCaller) CallInlineAgent(_ context.Context, req InlineAgentCallRequest) (*InlineAgentCallResult, error) {
	f.req = req
	return &InlineAgentCallResult{RunID: 9, Status: "succeeded", Output: map[string]any{"final_answer": "ok"}}, nil
}

func TestInlineAgentToolInheritsDefaultAgentPolicy(t *testing.T) {
	caller := &fakeInlineAgentCaller{}
	tool := InlineAgentTool{Caller: caller, Default: DefaultAgentConfig{ProviderID: 2, Model: "model", AllowedToolIDs: []int64{1, 2}, AllowedMCPServerIDs: []int64{3}, MaxIterations: 10, MaxToolCalls: 20, MaxExecutionTimeMS: 30000, MaxParallelChildren: 4, MaxDepth: 2, RequireApprovalForRisk: []string{RiskHigh}, MaxToolTimeoutMS: 5000, MaxToolOutputBytes: 1024, AllowedHosts: []string{"api.example.com"}}}
	_, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, WorkflowID: 2, RunID: 3}, json.RawMessage(`{"name":"researcher","system_prompt":"research carefully","task":"inspect code","tool_ids":[1],"mcp_server_ids":[3],"max_iterations":50}`))
	if err != nil {
		t.Fatal(err)
	}
	definition := caller.req.Definition
	if definition.ProviderID != 2 || definition.Model != "model" || definition.MaxIterations != 10 {
		t.Fatalf("default_agent limits were not inherited: %+v", definition)
	}
	if definition.MaxDepth != 2 || caller.req.MaxDepth != 2 {
		t.Fatalf("delegation depth was not inherited: definition=%+v request=%+v", definition, caller.req)
	}
	if len(definition.RequireApprovalForRisk) != 1 || definition.MaxToolTimeoutMS != 5000 || definition.MaxToolOutputBytes != 1024 || len(definition.AllowedHosts) != 1 {
		t.Fatalf("tool policy was not inherited: %+v", definition)
	}
}

func TestInlineAgentToolRejectsUnauthorizedResources(t *testing.T) {
	tool := InlineAgentTool{Caller: &fakeInlineAgentCaller{}, Default: DefaultAgentConfig{AllowedToolIDs: []int64{1}}}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, WorkflowID: 2, RunID: 3}, json.RawMessage(`{"name":"worker","system_prompt":"work","task":"task","tool_ids":[99]}`))
	if err == nil || result == nil || !result.IsError {
		t.Fatalf("expected unauthorized resource rejection, result=%+v err=%v", result, err)
	}
}

func TestInlineAgentToolRejectsUnauthorizedModel(t *testing.T) {
	tool := InlineAgentTool{Caller: &fakeInlineAgentCaller{}, Default: DefaultAgentConfig{Model: "allowed-model"}}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, WorkflowID: 2, RunID: 3}, json.RawMessage(`{"name":"worker","system_prompt":"work","task":"task","model":"other-model"}`))
	if err == nil || result == nil || !result.IsError {
		t.Fatalf("expected unauthorized model rejection, result=%+v err=%v", result, err)
	}
}
