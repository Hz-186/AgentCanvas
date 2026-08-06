import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { agentApi } from '../api/resources';
import type { RunStreamEvent } from '../types/events';
import { useRunStream } from './useRunStream';

type StreamHandlers = Parameters<typeof agentApi.streamRunEventsV1>[2];

const event = (
  runId: number,
  seq: number,
  kind: RunStreamEvent['kind'],
  data: unknown,
): RunStreamEvent => ({
  version: 1,
  run_id: runId,
  seq,
  kind,
  created_at: new Date(0).toISOString(),
  data,
} as RunStreamEvent);

function emit(handlers: StreamHandlers, value: RunStreamEvent): void {
  handlers.onMessage({
    id: String(value.seq),
    event: value.kind,
    data: JSON.stringify(value),
  });
}

function neverSettles(): Promise<void> {
  return new Promise(() => undefined);
}

describe('useRunStream', () => {
  let animationFrames: Map<number, FrameRequestCallback>;
  let nextAnimationFrameId: number;

  beforeEach(() => {
    vi.useFakeTimers();
    animationFrames = new Map();
    nextAnimationFrameId = 1;
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      const id = nextAnimationFrameId;
      nextAnimationFrameId += 1;
      animationFrames.set(id, callback);
      return id;
    });
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((id) => {
      animationFrames.delete(id);
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  function flushAnimationFrame(): void {
    const callbacks = [...animationFrames.values()];
    animationFrames.clear();
    callbacks.forEach((callback) => callback(performance.now()));
  }

  async function advanceToReconnect(): Promise<void> {
    await act(async () => {
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(250);
    });
  }

  it('batches v1 events through one animation frame before reducing them', () => {
    const stream = vi.spyOn(agentApi, 'streamRunEventsV1').mockImplementation(neverSettles);
    const { result } = renderHook(() => useRunStream({ runId: 7, generation: 1 }));
    const handlers = stream.mock.calls[0][2];

    act(() => {
      emit(handlers, event(7, 1, 'assistant.start', { segment_id: 'answer' }));
      emit(handlers, event(7, 2, 'assistant.delta', { segment_id: 'answer', text: 'hello' }));
    });

    expect(animationFrames).toHaveLength(1);
    expect(result.current.state.lastSeq).toBe(0);
    expect(result.current.state.segments).toEqual([]);

    act(flushAnimationFrame);

    expect(result.current.state.lastSeq).toBe(2);
    expect(result.current.state.segments).toEqual([
      expect.objectContaining({ id: 'answer', kind: 'assistant', text: 'hello' }),
    ]);
  });

  it('preserves assistant, tool, and assistant segments in stream order', () => {
    const stream = vi.spyOn(agentApi, 'streamRunEventsV1').mockImplementation(neverSettles);
    const { result } = renderHook(() => useRunStream({ runId: 7, generation: 1 }));
    const handlers = stream.mock.calls[0][2];

    act(() => {
      emit(handlers, event(7, 1, 'assistant.start', { segment_id: 'before' }));
      emit(handlers, event(7, 2, 'assistant.delta', { segment_id: 'before', text: 'before ' }));
      emit(handlers, event(7, 3, 'tool.start', {
        call_id: 'call-1',
        segment_id: 'tool-1',
        name: 'search',
        status: 'running',
      }));
      emit(handlers, event(7, 4, 'tool.complete', {
        call_id: 'call-1',
        segment_id: 'tool-1',
        name: 'search',
        status: 'succeeded',
        output: 'result',
      }));
      emit(handlers, event(7, 5, 'assistant.start', { segment_id: 'after' }));
      emit(handlers, event(7, 6, 'assistant.delta', { segment_id: 'after', text: 'after' }));
    });
    act(flushAnimationFrame);

    expect(result.current.state.segments.map(({ kind, text }) => ({ kind, text }))).toEqual([
      { kind: 'assistant', text: 'before ' },
      { kind: 'tool', text: 'result' },
      { kind: 'assistant', text: 'after' },
    ]);
  });

  it('ignores another run without letting its sequence pollute state or the reconnect cursor', async () => {
    const parked = neverSettles();
    const stream = vi.spyOn(agentApi, 'streamRunEventsV1')
      .mockImplementationOnce(async (_runId, _lastEventId, handlers) => {
        emit(handlers, event(99, 100, 'assistant.delta', { segment_id: 'foreign', text: 'leak' }));
        emit(handlers, event(7, 3, 'assistant.delta', { segment_id: 'own', text: 'kept' }));
      })
      .mockImplementation(() => parked);
    const { result } = renderHook(() => useRunStream({ runId: 7, generation: 1 }));

    act(flushAnimationFrame);
    expect(result.current.state.lastSeq).toBe(3);
    expect(result.current.state.segments).toEqual([
      expect.objectContaining({ id: 'own', text: 'kept' }),
    ]);

    await advanceToReconnect();

    expect(stream).toHaveBeenCalledTimes(2);
    expect(stream.mock.calls[1].slice(0, 2)).toEqual([7, '3']);
  });

  it('reconnects with the latest accepted sequence as Last-Event-ID', async () => {
    const parked = neverSettles();
    const stream = vi.spyOn(agentApi, 'streamRunEventsV1')
      .mockImplementationOnce(async (_runId, _lastEventId, handlers) => {
        emit(handlers, event(7, 2, 'assistant.delta', { segment_id: 'answer', text: 'first' }));
        emit(handlers, event(7, 8, 'assistant.delta', { segment_id: 'answer', text: 'latest' }));
      })
      .mockImplementation(() => parked);

    renderHook(() => useRunStream({ runId: 7, generation: 1 }));
    await advanceToReconnect();

    expect(stream).toHaveBeenCalledTimes(2);
    expect(stream.mock.calls[1].slice(0, 2)).toEqual([7, '8']);
  });

  it('does not reconnect after a terminal stream.snapshot', async () => {
    const parked = neverSettles();
    const stream = vi.spyOn(agentApi, 'streamRunEventsV1')
      .mockImplementationOnce(async (_runId, _lastEventId, handlers) => {
        emit(handlers, event(7, 9, 'stream.snapshot', {
          run: { id: 7, status: 'succeeded' },
          message: { id: 11, role: 'assistant', content: 'final' },
          usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
        }));
        emit(handlers, event(7, 10, 'assistant.delta', { segment_id: 'late', text: 'late' }));
      })
      .mockImplementation(() => parked);
    const { result } = renderHook(() => useRunStream({ runId: 7, generation: 1 }));

    act(flushAnimationFrame);
    expect(result.current.state.lifecycle).toBe('succeeded');
    expect(result.current.state.lastSeq).toBe(9);
    expect(result.current.state.segments).toEqual([
      expect.objectContaining({ id: 'terminal:11', text: 'final' }),
    ]);

    await advanceToReconnect();

    expect(stream).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['runId', { runId: 7, generation: 1 }, { runId: 8, generation: 1 }],
    ['generation', { runId: 7, generation: 1 }, { runId: 7, generation: 2 }],
  ])('aborts the old connection when %s changes', (_label, initial, next) => {
    const stream = vi.spyOn(agentApi, 'streamRunEventsV1').mockImplementation(neverSettles);
    const { rerender } = renderHook(
      ({ runId, generation }) => useRunStream({ runId, generation }),
      { initialProps: initial },
    );
    const oldSignal = stream.mock.calls[0][2].signal;

    rerender(next);

    expect(oldSignal?.aborted).toBe(true);
    expect(stream).toHaveBeenCalledTimes(2);
    expect(stream.mock.calls[1][0]).toBe(next.runId);
    expect(stream.mock.calls[1][2].signal?.aborted).toBe(false);
  });
});
