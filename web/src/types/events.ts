// Agent Run SSE 事件类型定义。

import type {
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
