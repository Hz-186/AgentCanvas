// Flow DSL 类型，与后端 internal/domain/flow/dsl.go 对应。

export type NodeType =
  | 'begin'
  | 'knowledge_retrieval'
  | 'prompt'
  | 'llm'
  | 'agent_loop'
  | 'agent_call'
  | 'workflow_call'
  | 'team_call'
  | 'code_sandbox'
  | 'message'
  | 'memory_read'
  | 'memory_write'
  | 'http_tool'
  | 'mcp_tool'
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
  mode?: 'react' | 'plan_execute' | 'reflect' | 'supervisor';
  provider_id?: number;
  model?: string;
  system_prompt?: string;
  task_template?: string;
  tool_ids?: number[];
  skill_ids?: number[];
  skill_loading_mode?: 'metadata_only' | 'search';
  knowledge_ids?: number[];
  knowledge_top_k?: number;
  knowledge_mode?: 'keyword' | 'vector' | 'hybrid';
  call_workflow_ids?: number[];
  mcp_server_ids?: number[];
  max_workflow_call_depth?: number;
  code_execution_enabled?: boolean;
  memory_enabled?: boolean;
  max_iterations?: number;
  max_tool_calls?: number;
  max_execution_time_ms?: number;
	allow_inline_agents?: boolean;
	max_parallel_sub_agents?: number;
  max_input_chars?: number;
	max_input_tokens?: number;
	context_window_tokens?: number;
	reserved_output_tokens?: number;
	context_safety_margin_tokens?: number;
	max_rule_tokens?: number;
  temperature?: number;
  reflection_enabled?: boolean;
  require_approval_for_risk?: string[];
  max_tool_timeout_ms?: number;
  max_tool_output_bytes?: number;
  allowed_hosts?: string[];
  tool_policy_json?: Record<string, unknown>;
  memory_policy_json?: Record<string, unknown>;
  context_policy_json?: Record<string, unknown>;
  output_schema_json?: Record<string, unknown>;
  return_intermediate_steps?: boolean;
  output_mode?: 'final_answer' | 'full';
}

export interface MessageConfig {
  content: string;
  with_citation?: boolean;
}

export interface AgentCallConfig {
  workflow_id: number;
  flow_version_id?: number;
  input?: Record<string, unknown>;
  max_depth?: number;
}

export interface TeamCallConfig {
  team_id: number;
  input?: Record<string, unknown>;
  max_depth?: number;
}

export interface CodeSandboxConfig {
  language?: 'python';
  code: string;
  timeout_ms?: number;
  max_output_bytes?: number;
  network_enabled?: boolean;
  memory_limit_mb?: number;
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

export interface MCPToolConfig {
  server_id: number;
  tool_name: string;
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
  | AgentCallConfig
  | TeamCallConfig
  | CodeSandboxConfig
  | MessageConfig
  | MemoryReadConfig
  | MemoryWriteConfig
  | HTTPToolConfig
  | MCPToolConfig
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
