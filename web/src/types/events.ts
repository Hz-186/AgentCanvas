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
	options?: ApprovalOptionPayload[];
}

export interface UsagePayload {
	prompt_tokens: number;
	completion_tokens: number;
	total_tokens: number;
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
	| RunStreamEnvelope<'tool.start' | 'tool.progress' | 'tool.complete' | 'tool.error', ToolPayload>
	| RunStreamEnvelope<'approval.required', ApprovalPayload>
	| RunStreamEnvelope<'usage.update', UsagePayload>
	| RunStreamEnvelope<'stream.snapshot' | 'run.complete' | 'run.failed' | 'run.paused' | 'run.waiting' | 'run.cancelled', TerminalSnapshotPayload>;
