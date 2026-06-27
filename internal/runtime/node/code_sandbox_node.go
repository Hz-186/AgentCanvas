package node

import (
	"context"
	"encoding/json"
	"fmt"

	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/sandbox"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type CodeSandboxNode struct {
	Runner sandbox.Runner
}

type codeSandboxConfig struct {
	Language       string `json:"language"`
	Code           string `json:"code"`
	TimeoutMS      int    `json:"timeout_ms"`
	MaxOutputBytes int    `json:"max_output_bytes"`
	NetworkEnabled bool   `json:"network_enabled"`
	MemoryLimitMB  int    `json:"memory_limit_mb"`
}

func (CodeSandboxNode) Type() string { return "code_sandbox" }

func (CodeSandboxNode) Validate(config json.RawMessage) error {
	var cfg codeSandboxConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid code_sandbox config", agenterrors.ErrInvalidInput)
	}
	if cfg.Language != "" && cfg.Language != "python" {
		return fmt.Errorf("%w: code_sandbox only supports python", agenterrors.ErrInvalidInput)
	}
	if cfg.TimeoutMS < 0 || cfg.TimeoutMS > 30000 {
		return fmt.Errorf("%w: code_sandbox timeout_ms must be <= 30000", agenterrors.ErrInvalidInput)
	}
	if cfg.MaxOutputBytes < 0 || cfg.MaxOutputBytes > 1024*1024 {
		return fmt.Errorf("%w: code_sandbox max_output_bytes must be <= 1048576", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n CodeSandboxNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Runner == nil {
		return nil, fmt.Errorf("code_sandbox runner is not configured")
	}
	var cfg codeSandboxConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	code := engine.ResolveTemplate(cfg.Code, rc)
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.SandboxStarted,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload:  map[string]any{"language": cfg.Language},
	})
	result, err := n.Runner.Execute(ctx, sandbox.ExecuteRequest{
		Language:       cfg.Language,
		Code:           code,
		TimeoutMS:      cfg.TimeoutMS,
		MaxOutputBytes: cfg.MaxOutputBytes,
		NetworkEnabled: cfg.NetworkEnabled,
		MemoryLimitMB:  cfg.MemoryLimitMB,
	})
	if err != nil {
		emitRuntimeEvent(ctx, rc, runtimeevent.Event{
			Type:     runtimeevent.SandboxFailed,
			RunID:    rc.RunID,
			NodeID:   rc.CurrentNodeID,
			NodeType: n.Type(),
			Payload:  map[string]any{"error": err.Error()},
		})
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{
		Type:     runtimeevent.SandboxFinished,
		RunID:    rc.RunID,
		NodeID:   rc.CurrentNodeID,
		NodeType: n.Type(),
		Payload: map[string]any{
			"exit_code":  result.ExitCode,
			"latency_ms": result.LatencyMS,
			"timed_out":  result.TimedOut,
		},
	})
	return engine.NodeOutput{
		"language":         result.Language,
		"stdout":           result.Stdout,
		"stderr":           result.Stderr,
		"exit_code":        result.ExitCode,
		"timed_out":        result.TimedOut,
		"output_truncated": result.OutputTruncated,
		"latency_ms":       result.LatencyMS,
		"content":          result.Stdout,
	}, nil
}
