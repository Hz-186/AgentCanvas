import { describe, expect, it } from 'vitest';
import type { FlowDSL } from '../../../../types/flow';
import { DEFAULT_REFLECTION_POLICY, defaultConfig } from '../../config';
import { reflectionIds } from '../../debug/AgentTraceTimeline';
import { canvasDSLKey, fromDSL, runtimeDSLKey } from './useDslBridge';

function dsl(config: Record<string, unknown>): FlowDSL {
  return {
    schema_version: 'v1',
    flow_id: 'workflow-1',
    nodes: [{ id: 'agent', type: 'agent_loop', name: 'Agent', config }],
    edges: [],
  };
}

describe('DSL bridge', () => {
  it('tracks canvas positions separately from runtime changes', () => {
    const first = dsl({ mode: 'react', _ui: { x: 120, y: 170 } });
    const moved = dsl({ mode: 'react', _ui: { x: 420, y: 260 } });

    expect(runtimeDSLKey(first)).toBe(runtimeDSLKey(moved));
    expect(canvasDSLKey(first)).not.toBe(canvasDSLKey(moved));
  });

  it('normalizes legacy reflect mode into ReAct with reflection enabled', () => {
    const canvas = fromDSL(dsl({ mode: 'reflect', _ui: { x: 12, y: 34, agent_mode: 'reasoning_action' } }));
    const config = canvas.nodes[0].data.config as Record<string, unknown>;

    expect(config.mode).toBe('react');
    expect(config.reflection_enabled).toBe(true);
    expect(config._ui).toEqual({ x: 12, y: 34 });
  });

  it('normalizes legacy supervisor mode while preserving delegation targets', () => {
    const canvas = fromDSL(dsl({ mode: 'supervisor', call_workflow_ids: [7] }));
    const config = canvas.nodes[0].data.config as Record<string, unknown>;

    expect(config.mode).toBe('react');
    expect(config.call_workflow_ids).toEqual([7]);
  });

  it('enables persistent Reflexion by default for new agent loops', () => {
    const config = defaultConfig('agent_loop') as Record<string, unknown>;

    expect(config.reflection_enabled).toBe(true);
    expect(config.reflection_policy_json).toEqual(DEFAULT_REFLECTION_POLICY);
  });

  it('extracts recalled reflection ids from arrays, JSON strings, and envelopes', () => {
    expect(reflectionIds([3, '5', 0, 'bad'])).toEqual([3, 5]);
    expect(reflectionIds('[7, 9]')).toEqual([7, 9]);
    expect(reflectionIds({ reflection_ids: [11] })).toEqual([11]);
    expect(reflectionIds('{bad')).toEqual([]);
  });
});
