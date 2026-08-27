// Agent Run SSE 事件类型定义。

import type {
	AgentTurn,
	Message,
	Run,
} from './api';

// Agent Run 流式事件，后端 data 是整个 Event 对象。
export interface RuntimeEvent {
  type: string;
  run_id: number;
  payload?: Record<string, unknown>;
  created_at: string;
}

// handler 层额外的框架事件
export interface RunDoneEvent {
  run: Run;
  output: Record<string, unknown>;
}

export const RUNTIME_EVENT_TYPES = [
  'agent_started',
  'agent_step',
  'agent_finished',
  'agent_failed',
  'retrieval_started',
  'retrieval_finished',
  'llm_started',
  'llm_delta',
  'llm_finished',
  'message_created',
  'tool_started',
  'tool_finished',
  'tool_failed',
  'workspace.created',
  'workspace.ready',
  'workspace.failed',
  'workspace.status_changed',
  'workspace.preserved',
  'workspace.cleaned',
  'git.status_changed',
  'git.commit_created',
] as const;

export const RUN_STREAM_VERSION = 1 as const;

export interface RunStreamEnvelope<K extends string = string, P = unknown> {
	version: typeof RUN_STREAM_VERSION;
	run_id: number;
	conversation_id?: number;
	seq: number;
	kind: K;
	created_at: string;
	data: P;
}

export interface TextPayload {
	segment_id: string;
	text?: string;
}

export interface StatusPayload {
	message: string;
	level: 'info' | 'warning' | 'error';
	degraded?: boolean;
}

export interface ToolPayload {
	call_id: string;
	segment_id: string;
	name: string;
	status: string;
	output?: unknown;
	error_code?: string;
	truncated?: boolean;
}

export interface ApprovalOptionPayload {
	id: string;
	label: string;
	description?: string;
}

export interface ApprovalPayload {
	request_id: number;
	call_id: string;
	tool_name: string;
	reason: string;
	is_blocking?: boolean;
	options?: ApprovalOptionPayload[];
}

export interface RequestUserInputPayload {
	call_id: string;
	tool_name: string;
	reason: string;
	is_blocking: false;
	questions?: Array<Record<string, unknown>>;
}

export interface UsagePayload {
	prompt_tokens: number;
	completion_tokens: number;
	total_tokens: number;
}

export interface WorkspaceUpdatePayload {
	workspace_id: number;
	run_id: number;
	project_id?: number;
	repository_root: string;
	workspace_path: string;
	branch_name: string;
	base_sha: string;
	head_sha: string;
	kind?: 'shared' | 'worktree' | string;
	dirty: boolean;
	has_unpushed_commits: boolean;
	status?: string;
	locked?: boolean;
	lock_reason?: string;
	cleanup_reason?: string;
	error_message?: string;
}

export interface TerminalSnapshotPayload {
	run: Run;
	turn?: AgentTurn;
	message?: Message;
	usage: UsagePayload;
}

export type RunStreamEvent =
	| RunStreamEnvelope<'assistant.start' | 'assistant.delta' | 'assistant.end', TextPayload>
	| RunStreamEnvelope<'reasoning.start' | 'reasoning.delta' | 'reasoning.end', TextPayload>
	| RunStreamEnvelope<'status.update', StatusPayload>
	| RunStreamEnvelope<'workspace.update', WorkspaceUpdatePayload>
	| RunStreamEnvelope<'tool.start' | 'tool.progress' | 'tool.complete' | 'tool.error', ToolPayload>
	| RunStreamEnvelope<'approval.required', ApprovalPayload>
	| RunStreamEnvelope<'request_user_input', RequestUserInputPayload>
	| RunStreamEnvelope<'usage.update', UsagePayload>
	| RunStreamEnvelope<'stream.snapshot' | 'run.complete' | 'run.failed' | 'run.paused' | 'run.waiting' | 'run.cancelled', TerminalSnapshotPayload>;
