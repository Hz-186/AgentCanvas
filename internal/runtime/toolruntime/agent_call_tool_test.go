package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type fakeIndependentAgentCaller struct{ request AgentCallRequest }

func (f *fakeIndependentAgentCaller) CallAgent(_ context.Context, req AgentCallRequest) (*AgentCallResult, error) {
	f.request = req
	return &AgentCallResult{RunID: 99, AgentID: req.AgentID, Status: "succeeded"}, nil
}

func TestAgentCallToolEnforcesReleaseAllowlist(t *testing.T) {
	caller := &fakeIndependentAgentCaller{}
	tool := AgentCallTool{Caller: caller, AllowedAgentIDs: []int64{12}, MaxDepth: 3}
	_, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, AgentID: 8, RunID: 10}, json.RawMessage(`{"agent_id":13,"task":"research"}`))
	if !errors.Is(err, agenterrors.ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, AgentID: 8, RunID: 10}, json.RawMessage(`{"agent_id":12,"task":"research"}`))
	if err != nil {
		t.Fatal(err)
	}
	if caller.request.CallerAgentID != 8 || caller.request.AgentID != 12 || result == nil {
		t.Fatalf("unexpected delegated request: %+v", caller.request)
	}
}
