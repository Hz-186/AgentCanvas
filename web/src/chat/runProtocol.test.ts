import { describe, expect, it } from 'vitest';
import { parseRunStreamEvent, RunStreamProtocolError } from './runProtocol';

const base = { version: 1, run_id: 7, seq: 1, created_at: '2026-08-05T00:00:00Z' };

describe('parseRunStreamEvent', () => {
  it.each([
    ['assistant.start', { segment_id: 'a1' }],
    ['assistant.delta', { segment_id: 'a1', text: 'hello' }],
    ['assistant.end', { segment_id: 'a1' }],
    ['reasoning.start', { segment_id: 'r1' }],
    ['reasoning.delta', { segment_id: 'r1', text: 'thinking' }],
    ['reasoning.end', { segment_id: 'r1' }],
    ['status.update', { message: 'working', level: 'info' }],
    ['tool.start', { call_id: 'c1', segment_id: 't1', name: 'search', status: 'running' }],
    ['tool.progress', { call_id: 'c1', segment_id: 't1', name: 'search', status: 'running' }],
    ['tool.complete', { call_id: 'c1', segment_id: 't1', name: 'search', status: 'succeeded' }],
    ['tool.error', { call_id: 'c1', segment_id: 't1', name: 'search', status: 'failed' }],
    ['approval.required', { request_id: 1, call_id: 'c1', tool_name: 'write', reason: 'risk' }],
    ['usage.update', { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 }],
    ['workspace.update', { workspace_id: 9, run_id: 7, repository_root: '/repo', workspace_path: '/repo/.worktrees/7-task', branch_name: 'demo/7-task', base_sha: 'abc', head_sha: 'def', dirty: true, has_unpushed_commits: false }],
    ['stream.snapshot', { run: { id: 7 }, usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }],
    ['run.complete', { run: { id: 7 }, usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }],
    ['run.failed', { run: { id: 7 }, usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }],
    ['run.paused', { run: { id: 7 }, usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }],
    ['run.waiting', { run: { id: 7 }, usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }],
    ['run.cancelled', { run: { id: 7 }, usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 } }],
  ])('accepts the %s fixture', (kind, data) => {
    expect(parseRunStreamEvent({ ...base, kind, data }).kind).toBe(kind);
  });

  it('rejects unknown kinds and incomplete payloads', () => {
    expect(() => parseRunStreamEvent({ ...base, kind: 'unknown', data: {} })).toThrow(RunStreamProtocolError);
    expect(() => parseRunStreamEvent({ ...base, kind: 'tool.start', data: { call_id: 'c1' } })).toThrow('segment_id');
    expect(() => parseRunStreamEvent({ ...base, kind: 'workspace.update', data: { workspace_id: 9, run_id: 7 } })).toThrow('repository_root');
    expect(() => parseRunStreamEvent('{bad json')).toThrow('valid JSON');
  });
});
