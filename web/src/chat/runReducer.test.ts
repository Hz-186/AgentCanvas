import { describe, expect, it } from 'vitest';
import type { RunStreamEvent } from '../types/events';
import { emptyRunState, runReducer } from './runState';

const event = (runId: number, seq: number, kind: RunStreamEvent['kind'], data: unknown): RunStreamEvent => ({
  version: 1,
  run_id: runId,
  seq,
  kind,
  created_at: new Date(0).toISOString(),
  data,
} as RunStreamEvent);

describe('runReducer', () => {
  it('keeps text, tool, and text as ordered segments', () => {
    let state = emptyRunState(7, 1);
    state = runReducer(state, { type: 'event', event: event(7, 1, 'assistant.start', { segment_id: 'a1' }) });
    state = runReducer(state, { type: 'event', event: event(7, 2, 'assistant.delta', { segment_id: 'a1', text: 'before ' }) });
    state = runReducer(state, { type: 'event', event: event(7, 3, 'tool.start', { call_id: 'c1', segment_id: 't1', name: 'search', status: 'running' }) });
    state = runReducer(state, { type: 'event', event: event(7, 4, 'tool.complete', { call_id: 'c1', segment_id: 't1', name: 'search', status: 'succeeded', output: 'result' }) });
    state = runReducer(state, { type: 'event', event: event(7, 5, 'assistant.start', { segment_id: 'a2' }) });
    state = runReducer(state, { type: 'event', event: event(7, 6, 'assistant.delta', { segment_id: 'a2', text: 'after' }) });
    expect(state.segments.map((item) => item.kind)).toEqual(['assistant', 'tool', 'assistant']);
    expect(state.segments[0].text).toBe('before ');
    expect(state.segments[1].text).toBe('result');
    expect(state.segments[2].text).toBe('after');
  });

  it('drops duplicate and out-of-order seq without duplicating text', () => {
    let state = emptyRunState(7, 1);
    const delta = event(7, 2, 'assistant.delta', { segment_id: 'a1', text: 'one' });
    state = runReducer(state, { type: 'event', event: delta });
    state = runReducer(state, { type: 'event', event: delta });
    state = runReducer(state, { type: 'event', event: event(7, 1, 'assistant.delta', { segment_id: 'a1', text: 'old' }) });
    expect(state.lastSeq).toBe(2);
    expect(state.segments[0].text).toBe('one');
  });

  it('stores workspace status updates for the active run', () => {
    let state = emptyRunState(7, 1);
	const workspace = { workspace_id: 9, run_id: 7, repository_root: '/repo', workspace_path: '/repo/.worktrees/7-task', branch_name: 'demo/7-task', base_sha: 'abc', head_sha: 'def', dirty: true, has_unpushed_commits: false };
    state = runReducer(state, { type: 'event', event: event(7, 1, 'workspace.update', workspace) });
    expect(state.workspace).toEqual(workspace);
    expect(state.segments).toContainEqual(expect.objectContaining({ id: 'workspace:1', kind: 'status' }));
  });

	 it('keeps non-blocking request_user_input in the running loop', () => {
		let state = emptyRunState(7, 1);
		state = runReducer(state, { type: 'event', event: event(7, 1, 'request_user_input', {
			call_id: 'call-1', tool_name: 'request_user_input', reason: '补充上下文', is_blocking: false,
		}) });
		expect(state.lifecycle).toBe('running');
		expect(state.approval).toBeNull();
		expect(state.segments[0]).toEqual(expect.objectContaining({ kind: 'status', text: '补充上下文' }));
	 });

  it('isolates runs and lets the terminal snapshot replace the temporary view', () => {
    let state = emptyRunState(7, 2);
    state = runReducer(state, { type: 'event', event: event(8, 1, 'assistant.delta', { segment_id: 'other', text: 'leak' }) });
    state = runReducer(state, { type: 'event', event: event(7, 1, 'assistant.delta', { segment_id: 'a1', text: 'temporary' }) });
    const snapshot = {
      run: { id: 7 } as never,
      message: { id: 11, role: 'assistant', content: 'final' } as never,
      usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
    };
    state = runReducer(state, { type: 'event', event: event(7, 2, 'run.complete', snapshot) });
    state = runReducer(state, { type: 'event', event: event(7, 3, 'assistant.delta', { segment_id: 'late', text: 'late' }) });
    expect(state.segments).toHaveLength(1);
    expect(state.segments[0].text).toBe('final');
    expect(state.terminalSnapshot?.usage.total_tokens).toBe(3);
    expect(state.lifecycle).toBe('succeeded');
  });

  it('clears a waiting snapshot when tool activity resumes on the same run', () => {
    let state = emptyRunState(7, 1);
    state = runReducer(state, { type: 'event', event: event(7, 1, 'approval.required', {
      request_id: 9,
      call_id: 'call-1',
      tool_name: 'search',
      reason: 'confirm',
    }) });
    state = runReducer(state, { type: 'event', event: event(7, 2, 'run.waiting', {
      run: { id: 7, status: 'waiting_human' },
      message: { id: 11, role: 'assistant', content: 'waiting copy' },
      usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
    }) });
    state = runReducer(state, { type: 'event', event: event(7, 3, 'tool.complete', {
      call_id: 'call-1',
      segment_id: 'tool-1',
      name: 'search',
      status: 'succeeded',
      output: 'resumed result',
    }) });

    expect(state.lifecycle).toBe('running');
    expect(state.approval).toBeNull();
    expect(state.terminalSnapshot).toBeNull();
    expect(state.segments).toEqual([
      expect.objectContaining({ id: 'tool-1', kind: 'tool', text: 'resumed result' }),
    ]);
  });
});
