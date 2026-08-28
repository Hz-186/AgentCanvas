package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/pkg/logger"
)

// ModelEventEmitter receives provider-neutral model events. It is separate
// from StepEmitter because streamed deltas are transient UI transport, while
// RunStep is a durable execution trace.
type ModelEventEmitter func(context.Context, llm.ModelStreamEvent) error

var ErrEmptyModelResponse = errors.New("model returned an empty response")

func (r *Runner) executeModelTurn(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) (response *llm.ToolChatResponse, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	started := r.now()
	r.logLLMRequest(ctx, cfg, req)
	// The completion diagnostic observes the final named returns, so every
	// return path (streaming, capability fallback, non-streaming) emits
	// exactly one llm.completed without touching the pass-through values.
	defer func() {
		r.logLLMCompleted(ctx, cfg, req, response, err, started)
	}()
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
		response.Message.Content, response.ProposedPlan = llm.NormalizeProposedPlan(response.Message.Content)
		if response.Usage.TotalTokens == 0 && response.Usage.PromptTokens == 0 && response.Usage.CompletionTokens == 0 && state.hasUsage {
			response.Usage = state.lastUsage
		}
		if response.Usage.PromptTokens != 0 || response.Usage.CompletionTokens != 0 || response.Usage.TotalTokens != 0 {
			if err := r.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelUsage, Usage: response.Usage}); err != nil {
				return nil, err
			}
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
	response.Message.Content, response.ProposedPlan = llm.NormalizeProposedPlan(response.Message.Content)
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
	lastUsage   llm.Usage
	hasUsage    bool
	parser      llm.ProposedPlanStreamParser
	textOpen    bool
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
	var err error
	switch event.Kind {
	case llm.ModelUsage:
		s.lastUsage, s.hasUsage = event.Usage, true
		err = nil
	case llm.ModelTextStart:
		// Delay the visible text start until a non-plan line is available.
	case llm.ModelTextDelta:
		for _, parsed := range s.parser.Push(event.Text) {
			if parsed.Kind == llm.ModelTextDelta {
				if !s.textOpen {
					err = runner.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelTextStart})
					s.textOpen = true
				}
				if err == nil {
					err = runner.emitModelEvent(ctx, parsed)
				}
			} else if err == nil {
				err = runner.emitModelEvent(ctx, parsed)
			}
			if err != nil {
				break
			}
		}
	case llm.ModelTextEnd:
		err = s.emitParserFinish(ctx, runner)
	case llm.ModelDone:
		err = s.emitParserFinish(ctx, runner)
		if err == nil {
			err = runner.emitModelEvent(ctx, event)
		}
	case llm.ModelError:
		err = runner.emitModelEvent(ctx, event)
	default:
		err = runner.emitModelEvent(ctx, event)
	}
	if err != nil {
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

func (s *modelTurnStreamState) emitParserFinish(ctx context.Context, runner *Runner) error {
	for _, parsed := range s.parser.Finish() {
		if parsed.Kind == llm.ModelTextDelta {
			if !s.textOpen {
				if err := runner.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelTextStart}); err != nil {
					return err
				}
				s.textOpen = true
			}
			if err := runner.emitModelEvent(ctx, parsed); err != nil {
				return err
			}
			continue
		}
		if err := runner.emitModelEvent(ctx, parsed); err != nil {
			return err
		}
	}
	if s.textOpen {
		s.textOpen = false
		return runner.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelTextEnd})
	}
	return nil
}

func (r *Runner) emitModelEvent(ctx context.Context, event llm.ModelStreamEvent) error {
	if r.OnModelEvent == nil {
		return nil
	}
	return r.OnModelEvent(ctx, event)
}

// logLLMRequest emits the bounded, metadata-only llm.request diagnostic. The
// provider API key never enters diagnostics.
func (r *Runner) logLLMRequest(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest) {
	r.diagnosticsLogger().Log(ctx, slog.LevelInfo, "llm.request",
		"event", "llm.request", "phase", "llm", "result", "ok", "latency_ms", 0,
		"provider", cfg.ProviderType, "model", req.Model)
}

// logLLMCompleted emits the bounded, metadata-only llm.completed diagnostic
// with usage token counts only. The original response/error pass through
// unchanged; on failure the error TYPE is reported, never the error text.
func (r *Runner) logLLMCompleted(ctx context.Context, cfg llm.ChatProviderConfig, req llm.ToolChatRequest, response *llm.ToolChatResponse, callErr error, started time.Time) {
	latencyMS := int(r.now().Sub(started).Milliseconds())
	if callErr != nil {
		r.diagnosticsLogger().Log(ctx, slog.LevelError, "llm.completed",
			"event", "llm.completed", "phase", "llm", "result", "error",
			"provider", cfg.ProviderType, "model", req.Model,
			"latency_ms", latencyMS, "error_class", logger.ErrorClass(callErr))
		return
	}
	attrs := []any{"event", "llm.completed", "phase", "llm", "result", "ok",
		"provider", cfg.ProviderType, "model", req.Model, "latency_ms", latencyMS}
	if response != nil {
		attrs = append(attrs, "usage", map[string]int{
			"prompt_tokens":     response.Usage.PromptTokens,
			"completion_tokens": response.Usage.CompletionTokens,
			"total_tokens":      response.Usage.TotalTokens,
		})
	}
	r.diagnosticsLogger().Log(ctx, slog.LevelInfo, "llm.completed", attrs...)
}

// emitAccumulatedModelEvents keeps non-streaming clients compatible with the
// same semantic callback contract. These callbacks are not real-time, but
// consumers never need a second event vocabulary during migration.
func (r *Runner) emitAccumulatedModelEvents(ctx context.Context, response *llm.ToolChatResponse) error {
	if response == nil {
		return ErrEmptyModelResponse
	}
	if response.Message.Content != "" || response.ProposedPlan != "" {
		parser := &llm.ProposedPlanStreamParser{}
		events := append(parser.Push(response.Message.Content), parser.Finish()...)
		textOpen := false
		for _, event := range events {
			if event.Kind == llm.ModelTextDelta && !textOpen {
				if err := r.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelTextStart}); err != nil {
					return err
				}
				textOpen = true
			}
			if err := r.emitModelEvent(ctx, event); err != nil {
				return err
			}
		}
		if textOpen {
			if err := r.emitModelEvent(ctx, llm.ModelStreamEvent{Kind: llm.ModelTextEnd}); err != nil {
				return err
			}
		}
		if response.ProposedPlan != "" {
			for _, event := range []llm.ModelStreamEvent{
				{Kind: llm.ModelProposedPlanStart},
				{Kind: llm.ModelProposedPlanDelta, Text: response.ProposedPlan},
				{Kind: llm.ModelProposedPlanEnd},
			} {
				if err := r.emitModelEvent(ctx, event); err != nil {
					return err
				}
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
