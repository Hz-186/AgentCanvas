package toolruntime

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeSubagentDispatcher struct {
	req SubagentRequest
}

func (f *fakeSubagentDispatcher) RunSubagent(_ context.Context, req SubagentRequest) (*SubagentResult, error) {
	f.req = req
	return &SubagentResult{RunID: 9, Status: "succeeded", Output: map[string]any{"final_answer": "ok"}}, nil
}

func TestSubagentToolInheritsParentPolicy(t *testing.T) {
	dispatcher := &fakeSubagentDispatcher{}
	tool := SubagentTool{Dispatcher: dispatcher, Default: DefaultSubagentConfig{ProviderID: 2, Model: "model", AllowedToolIDs: []int64{1, 2}, AllowedMCPServerIDs: []int64{3}, MaxIterations: 10, MaxToolCalls: 20, MaxExecutionTimeMS: 30000, MaxParallelChildren: 4, MaxDepth: 2, RequireApprovalForRisk: []string{RiskHigh}, MaxToolTimeoutMS: 5000, MaxToolOutputBytes: 1024, AllowedHosts: []string{"api.example.com"}}}
	_, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, AgentID: 2, RunID: 3}, json.RawMessage(`{"name":"researcher","system_prompt":"research carefully","task":"inspect code","tool_ids":[1],"mcp_server_ids":[3],"max_iterations":50}`))
	if err != nil {
		t.Fatal(err)
	}
	definition := dispatcher.req.Definition
	if definition.ProviderID != 2 || definition.Model != "model" || definition.MaxIterations != 10 {
		t.Fatalf("parent limits were not inherited: %+v", definition)
	}
	if definition.MaxDepth != 2 || dispatcher.req.MaxDepth != 2 {
		t.Fatalf("delegation depth was not inherited: definition=%+v request=%+v", definition, dispatcher.req)
	}
	if len(definition.RequireApprovalForRisk) != 1 || definition.MaxToolTimeoutMS != 5000 || definition.MaxToolOutputBytes != 1024 || len(definition.AllowedHosts) != 1 {
		t.Fatalf("tool policy was not inherited: %+v", definition)
	}
}

func TestSubagentToolRejectsUnauthorizedResources(t *testing.T) {
	tool := SubagentTool{Dispatcher: &fakeSubagentDispatcher{}, Default: DefaultSubagentConfig{AllowedToolIDs: []int64{1}}}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, AgentID: 2, RunID: 3}, json.RawMessage(`{"name":"worker","system_prompt":"work","task":"task","tool_ids":[99]}`))
	if err == nil || result == nil || !result.IsError {
		t.Fatalf("expected unauthorized resource rejection, result=%+v err=%v", result, err)
	}
}

func TestSubagentToolRejectsUnauthorizedModel(t *testing.T) {
	tool := SubagentTool{Dispatcher: &fakeSubagentDispatcher{}, Default: DefaultSubagentConfig{Model: "allowed-model"}}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, AgentID: 2, RunID: 3}, json.RawMessage(`{"name":"worker","system_prompt":"work","task":"task","model":"other-model"}`))
	if err == nil || result == nil || !result.IsError {
		t.Fatalf("expected unauthorized model rejection, result=%+v err=%v", result, err)
	}
}

func TestSubagentToolAssignsDefaults(t *testing.T) {
	dispatcher := &fakeSubagentDispatcher{}
	tool := SubagentTool{Dispatcher: dispatcher, Default: DefaultSubagentConfig{Model: "model", AllowedToolIDs: []int64{1, 2}, AllowedSkillIDs: []int64{3}}}
	if _, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1}, json.RawMessage(`{"task":"find the regression"}`)); err != nil {
		t.Fatal(err)
	}
	if dispatcher.req.Definition.Name != "subagent" || dispatcher.req.Definition.SystemPrompt == "" {
		t.Fatalf("expected runtime-assigned defaults: %+v", dispatcher.req.Definition)
	}
	if len(dispatcher.req.Definition.ToolIDs) != 2 || len(dispatcher.req.Definition.SkillIDs) != 1 {
		t.Fatalf("expected child to inherit parent resources: %+v", dispatcher.req.Definition)
	}
}
