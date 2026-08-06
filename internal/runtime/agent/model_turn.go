package agent

import (
	"context"
	"errors"
	"fmt"

	"agentcanvas/internal/infrastructure/llm"
)

// ModelEventEmitter receives provider-neutral model events. It is separate
// from StepEmitter because streamed deltas are transient UI transport, while
// RunStep is a durable execution trace.
type ModelEventEmitter func(context.Context, llm.ModelStreamEvent) error

var ErrEmptyModelResponse = errors.New("model returned an empty response")

func (r *Runner) executeModelTurn(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if streaming, ok := r.LLM.(llm.ToolStreamingClient); ok {
		state := modelTurnStreamState{}
		response, err := streaming.StreamChatWithTools(ctx, cfg, req, func(event llm.ModelStreamEvent) error {
			return state.forward(ctx, r, event)
		})
		if errors.Is(err, llm.ErrToolStreamingUnsupported) && !state.emittedAny && state.emitterErr == nil {
			return r.executeNonStreamingModelTurn(ctx, cfg, req)
		}
		if err != nil {
			if state.emitterErr == nil && state.terminal == "" {
				_ = state.forward(ctx, r, llm.ModelStreamEvent{Kind: llm.ModelError, Err: err})
			}
			return nil, err
		}
		if state.terminal == llm.ModelError {
			if state.terminalErr != nil {
				return nil, state.terminalErr
			}
			return nil, fmt.Errorf("model stream ended with an error event")
		}
		if response == nil {
			if state.terminal == "" {
				_ = state.forward(ctx, r, llm.ModelStreamEvent{Kind: llm.ModelError, Err: ErrEmptyModelResponse})
			}
			return nil, ErrEmptyModelResponse
		}
		if state.terminal == "" {
			if err := state.forward(ctx, r, llm.ModelStreamEvent{Kind: llm.ModelDone}); err != nil {
				return nil, err
			}
		}
		return response, nil
	}
	return r.executeNonStreamingModelTurn(ctx, cfg, req)
}

func (r *Runner) executeNonStreamingModelTurn(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (*llm.ToolChatResponse, error) {
	response, err := r.LLM.ChatWithTools(ctx, cfg, req)
	if err != nil {
		_ = r.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelError, Err: err})
		return nil, err
	}
	if response == nil {
		_ = r.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelError, Err: ErrEmptyModelResponse})
		return nil, ErrEmptyModelResponse
	}
	if err := r.emitAccumulatedModelEvents(ctx, response); err != nil {
		return nil, err
	}
	return response, nil
}

type modelTurnStreamState struct {
	emittedAny  bool
	terminal    llm.ModelStreamKind
	terminalErr error
	emitterErr  error
}

func (s *modelTurnStreamState) forward(ctx context.Context, runner *Runner, event llm.ModelStreamEvent) error {
	if s.terminal != "" {
		// Provider adapters own terminal detection. Suppress duplicate terminal
		// markers so one model turn has exactly one terminal event.
		if event.Kind == llm.ModelDone || event.Kind == llm.ModelError {
			return nil
		}
		return fmt.Errorf("model stream emitted %s after terminal event %s", event.Kind, s.terminal)
	}
	if err := runner.emitModelEvent(ctx, event); err != nil {
		s.emitterErr = err
		return err
	}
	s.emittedAny = true
	switch event.Kind {
	case llm.ModelDone:
		s.terminal = llm.ModelDone
	case llm.ModelError:
		s.terminal = llm.ModelError
		s.terminalErr = event.Err
	}
	return nil
}

func (r *Runner) emitModelEvent(ctx context.Context, event llm.ModelStreamEvent) error {
	if r.OnModelEvent == nil {
		return nil
	}
	return r.OnModelEvent(ctx, event)
}

// emitAccumulatedModelEvents keeps non-streaming clients compatible with the
// same semantic callback contract. These callbacks are not real-time, but
// consumers never need a second event vocabulary during migration.
func (r *Runner) emitAccumulatedModelEvents(ctx context.Context, response *llm.ToolChatResponse) error {
	if response == nil {
		return ErrEmptyModelResponse
	}
	if response.Message.Content != "" {
		for _, event := range []llm.ModelStreamEvent{
			{Kind: llm.ModelTextStart},
			{Kind: llm.ModelTextDelta, Text: response.Message.Content},
			{Kind: llm.ModelTextEnd},
		} {
			if err := r.emitModelEvent(ctx, event); err != nil {
				return err
			}
		}
	}
	for index, call := range response.Message.ToolCalls {
		for _, event := range []llm.ModelStreamEvent{
			{Kind: llm.ModelToolCallStart, Index: index, CallID: call.ID, Name: call.Name},
			{Kind: llm.ModelToolCallDelta, Index: index, CallID: call.ID, Name: call.Name, ArgumentDelta: string(call.Arguments)},
			{Kind: llm.ModelToolCallEnd, Index: index, CallID: call.ID, Name: call.Name, Arguments: call.Arguments},
		} {
			if err := r.emitModelEvent(ctx, event); err != nil {
				return err
			}
		}
	}
	if response.Usage.PromptTokens != 0 || response.Usage.CompletionTokens != 0 || response.Usage.TotalTokens != 0 {
		if err := r.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelUsage, Usage: response.Usage}); err != nil {
			return err
		}
	}
	return r.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelDone})
}
