import { describe, expect, it } from 'vitest';
import { phaseFor } from './RunObservatory';
import type { RuntimeEvent } from '@/types/events';

const event = (type: string): RuntimeEvent => ({ type, run_id: 1, created_at: '2026-07-11T00:00:00Z' });

describe('phaseFor', () => {
  it('uses idle and thinking before the first event arrives', () => {
    expect(phaseFor([], false)).toBe('idle');
    expect(phaseFor([], true)).toBe('thinking');
  });

  it.each([
    ['retrieval_started', 'retrieval'],
    ['tool_started', 'tool'],
    ['approval_required', 'approval'],
    ['workflow_finished', 'complete'],
    ['node_failed', 'error'],
  ])('maps %s to %s', (type, phase) => {
    expect(phaseFor([event(type)], true)).toBe(phase);
  });

  it('keeps unknown live events in the thinking state', () => {
    expect(phaseFor([event('custom_runtime_note')], true)).toBe('thinking');
  });
});
