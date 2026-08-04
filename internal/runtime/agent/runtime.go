package agent

import (
	"context"
	"time"

	"agentcanvas/internal/infrastructure/llm"
)

// Runtime is the Agent execution contract for the loop, tool batches,
// checkpoints, approvals and resume.
type Runtime interface {
	Execute(context.Context, RunRequest) (*RunResult, error)
	Resume(context.Context, ResumeRequest) (*RunResult, error)
}

type RuntimeOptions struct {
	LLM        llm.ToolCallingClient
	OnStep     StepEmitter
	Now        func() time.Time
	ProviderID int64
	ModelName  string
}

type runtimeModule struct{ runner *Runner }

func NewRuntime(options RuntimeOptions) Runtime {
	runner := &Runner{LLM: options.LLM, OnStep: options.OnStep, Now: options.Now, ProviderID: options.ProviderID, ModelName: options.ModelName}
	return &runtimeModule{runner: runner}
}

func (m *runtimeModule) Execute(ctx context.Context, req RunRequest) (*RunResult, error) {
	req.EnforceContextPrecedence = true
	return m.runner.Run(ctx, req)
}

func (m *runtimeModule) Resume(ctx context.Context, req ResumeRequest) (*RunResult, error) {
	runRequest, err := BuildResumeRequest(req)
	if err != nil {
		return nil, err
	}
	runRequest.EnforceContextPrecedence = true
	return m.runner.Run(ctx, *runRequest)
}
