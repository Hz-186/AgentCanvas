// Flow DSL 类型，与后端 internal/domain/flow/dsl.go 对应。

export type NodeType =
  | 'begin'
  | 'knowledge_retrieval'
  | 'prompt'
  | 'llm'
  | 'message';

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

export interface MessageConfig {
  content: string;
  with_citation?: boolean;
}

export type NodeConfig =
  | BeginConfig
  | RetrievalConfig
  | PromptConfig
  | LLMConfig
  | MessageConfig
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
