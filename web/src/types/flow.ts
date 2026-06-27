// Flow DSL 类型，与后端 internal/domain/flow/dsl.go 对应。

export type NodeType =
  | 'begin'
  | 'knowledge_retrieval'
  | 'prompt'
  | 'llm'
  | 'agent_loop'
  | 'message'
  | 'memory_read'
  | 'memory_write'
  | 'http_tool'
  | 'switch'
  | 'json_output'
  | 'guardrail';

export interface BeginConfig {
  input_schema?: Record<string, string>;
}

export interface RetrievalConfig {
  kb_ids: number[];
  top_k?: number;
  mode?: string;
  query?: string;
}

export interface PromptConfig {
  template: string;
}

export interface LLMConfig {
  provider_id: number;
  model?: string;
  temperature?: number;
  stream?: boolean;
}

export interface AgentLoopConfig {
  provider_id: number;
  model?: string;
  system_prompt?: string;
  task_template?: string;
  tool_ids?: number[];
  knowledge_ids?: number[];
  knowledge_top_k?: number;
  knowledge_mode?: 'keyword' | 'vector' | 'hybrid';
  max_iterations?: number;
  max_tool_calls?: number;
  max_execution_time_ms?: number;
  temperature?: number;
  return_intermediate_steps?: boolean;
  output_mode?: 'final_answer' | 'full';
}

export interface MessageConfig {
  content: string;
  with_citation?: boolean;
}

export interface MemoryReadConfig {
  memory_types: string[];
  limit?: number;
}

export interface MemoryWriteConfig {
  memory_id?: number;
  memory_type: string;
  title?: string;
  content: string;
  importance?: number;
  reason?: string;
  source?: string;
}

export interface HTTPToolConfig {
  tool_id: number;
  input: Record<string, unknown>;
}

export interface SwitchConfig {
  conditions: Array<{ expr: string; target: string }>;
}

export interface JSONOutputConfig {
  value: string;
  schema?: Record<string, unknown>;
}

export interface GuardrailConfig {
  source: string;
  max_length?: number;
  banned_terms?: string[];
  require_citation?: boolean;
  require_json?: boolean;
  schema?: Record<string, unknown>;
}

export type NodeConfig =
  | BeginConfig
  | RetrievalConfig
  | PromptConfig
  | LLMConfig
  | AgentLoopConfig
  | MessageConfig
  | MemoryReadConfig
  | MemoryWriteConfig
  | HTTPToolConfig
  | SwitchConfig
  | JSONOutputConfig
  | GuardrailConfig
  | Record<string, unknown>;

export interface DSLNode {
  id: string;
  type: NodeType;
  name: string;
  config: NodeConfig;
}

export interface DSLEdge {
  from: string;
  to: string;
}

export interface FlowDSL {
  schema_version: string;
  flow_id: string;
  nodes: DSLNode[];
  edges: DSLEdge[];
}

// 画布上节点的额外可视化数据（位置），保存进 DSL 的 node.config._ui 中以便回放。
export interface NodeUIPosition {
  x: number;
  y: number;
}
