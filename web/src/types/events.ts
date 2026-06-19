// SSE 事件类型定义，对应后端 chat stream 与 agent run stream。

import type {
  ChatResponse,
  Conversation,
  Message,
  MessageReference,
  Run,
} from './api';

// —— RAG Chat 流式事件 ——
export type ChatStreamEvent =
  | { type: 'conversation'; data: Conversation }
  | { type: 'user_message'; data: Message }
  | { type: 'retrieval'; data: { references: MessageReference[]; latency_ms: number } }
  | { type: 'delta'; data: { content: string } }
  | { type: 'done'; data: ChatResponse }
  | { type: 'error'; data: { message: string } };

// —— Agent Run 流式事件 ——
// 后端 run stream 的 data 是整个 Event 对象。
export interface RuntimeEvent {
  type: string;
  run_id: number;
  node_id?: string;
  node_type?: string;
  payload?: Record<string, unknown>;
  created_at: string;
}

// handler 层额外的框架事件
export interface RunDoneEvent {
  run: Run;
  output: Record<string, unknown>;
}

export const RUNTIME_EVENT_TYPES = [
  'workflow_started',
  'node_started',
  'node_finished',
  'node_failed',
  'retrieval_started',
  'retrieval_finished',
  'llm_started',
  'llm_delta',
  'llm_finished',
  'message_created',
  'workflow_finished',
  'workflow_failed',
] as const;
