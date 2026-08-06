import { useEffect, useReducer, useState } from 'react';
import { agentApi } from '../api/resources';
import { isTerminalRunEvent, parseRunStreamEvent } from '../chat/runProtocol';
import { emptyRunState, runReducer } from '../chat/runState';
import type { RunStreamEvent } from '../types/events';

export interface UseRunStreamOptions {
  runId: number | null;
  generation: number;
  enabled?: boolean;
}

export function useRunStream({ runId, generation, enabled = true }: UseRunStreamOptions) {
  const [state, dispatch] = useReducer(runReducer, emptyRunState(runId, generation));
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    dispatch({ type: 'reset', runId, generation });
    setError(null);
    if (!enabled || runId == null) return;
    const controller = new AbortController();
    let lastSeq = 0;
    let terminal = false;
    let reconnects = 0;
    let frame = 0;
    const queue: RunStreamEvent[] = [];
    const flush = () => {
      frame = 0;
      for (const event of queue.splice(0)) dispatch({ type: 'event', event, generation });
    };
    const enqueue = (event: RunStreamEvent) => {
      queue.push(event);
      if (!frame) frame = window.requestAnimationFrame(flush);
    };
    const connect = async () => {
      while (!controller.signal.aborted && !terminal) {
        let streamError: Error | null = null;
        await agentApi.streamRunEventsV1(runId, lastSeq > 0 ? String(lastSeq) : undefined, {
          signal: controller.signal,
          onMessage: (message) => {
            if (terminal) return;
            try {
              const event = parseRunStreamEvent(message.data);
              if (event.run_id !== runId) return;
              lastSeq = Math.max(lastSeq, event.seq);
              terminal = terminal || isTerminalRunEvent(event);
              reconnects = 0;
              setError(null);
              enqueue(event);
            } catch (cause) {
              streamError = cause instanceof Error ? cause : new Error(String(cause));
            }
          },
          onError: (cause) => { streamError = cause; },
        });
        if (controller.signal.aborted || terminal) break;
        reconnects += 1;
        if (streamError) setError(streamError);
        await new Promise((resolve) => window.setTimeout(resolve, Math.min(250 * reconnects, 2_000)));
      }
      if (!controller.signal.aborted && queue.length) flush();
    };
    void connect();
    return () => {
      controller.abort();
      if (frame) window.cancelAnimationFrame(frame);
      queue.length = 0;
    };
  }, [enabled, generation, runId]);

  return { state, error };
}
