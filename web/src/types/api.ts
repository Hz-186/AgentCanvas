// 后端实体的 TypeScript 类型定义，字段名与 Go JSON tag 严格对应。

export interface ApiEnvelope<T> {
  code: number;
  message: string;
  data: T;
}

// —— 认证 ——
export type LoginType = 'password' | 'github' | 'mixed';

export interface User {
  id: number;
  username: string;
  email: string | null;
  avatar_url: string;
  login_type: LoginType;
  status: number;
  last_login_at: string | null;
  created_at: string;
}

export interface TokenPair {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_at: string;
}

export interface AuthResponse {
  user: User;
  tokens: TokenPair;
}

// —— Provider ——
export type ProviderType =
  | 'openai_compatible'
  | 'deepseek'
  | 'qwen'
  | 'ollama'
  | 'azure_openai'
  | 'local';

export interface ModelProvider {
  id: number;
  owner_id: number;
  name: string;
  provider_type: ProviderType;
  base_url: string;
  api_key_mask: string;
  default_chat_model: string;
  default_embedding_model: string;
  status: number;
  last_test_status: string;
  last_test_error?: string;
  last_test_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreateProviderRequest {
  name: string;
  provider_type: ProviderType;
  base_url?: string;
  api_key?: string;
  default_chat_model?: string;
  default_embedding_model?: string;
}

export interface UpdateProviderRequest {
  name?: string;
  provider_type?: ProviderType;
  base_url?: string;
  api_key?: string;
  default_chat_model?: string;
  default_embedding_model?: string;
  status?: number;
}

// —— API Token ——
export interface ApiToken {
  id: number;
  owner_id: number;
  name: string;
  token_prefix: string;
  scopes: string;
  last_used_at: string | null;
  expires_at: string | null;
  revoked_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface ApiTokenCreated {
  id: number;
  name: string;
  token: string;
  token_prefix: string;
  scopes: string[];
  expires_at: string | null;
  created_at: string;
}

// —— 审计日志 ——
export interface AuditLog {
  id: number;
  owner_id: number;
  actor_id: number;
  action: string;
  resource_type: string;
  resource_id: string;
  detail_json: string;
  ip_address: string;
  user_agent: string;
  created_at: string;
}

// —— 知识库 ——
export interface KnowledgeBase {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  retrieval_backend: string;
  retrieval_mode: string;
  chunk_method: string;
  chunk_size: number;
  chunk_overlap: number;
  status: number;
  document_count: number;
  chunk_count: number;
  created_at: string;
  updated_at: string;
}

export type ParserStatus =
  | 'pending'
  | 'parsing'
  | 'chunking'
  | 'indexing'
  | 'completed'
  | 'failed';

export interface AgentDocument {
  id: number;
  owner_id: number;
  kb_id: number;
  name: string;
  original_filename: string;
  file_type: string;
  mime_type: string;
  file_size: number;
  object_key: string;
  content_hash: string;
  parser_status: ParserStatus;
  parser_error?: string;
  chunk_count: number;
  token_count: number;
  indexed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface DocumentChunk {
  id: number;
  owner_id: number;
  kb_id: number;
  document_id: number;
  chunk_index: number;
  content: string;
  content_hash: string;
  token_count: number;
  char_count: number;
  page_no: number | null;
  section_title: string;
  es_index: string;
  es_doc_id: string;
  metadata_json: string;
  created_at: string;
  updated_at: string;
}

export interface UploadDocumentResponse {
  document: AgentDocument;
  job_id?: number;
}

export interface RetrievalResult {
  chunk_id: number;
  document_id: number;
  kb_id: number;
  score: number;
  content: string;
  highlight: string;
  document_name: string;
  page_no: number | null;
  metadata: Record<string, unknown>;
}

export interface RetrievalResponse {
  results: RetrievalResult[];
  latency_ms: number;
}

// —— 会话 / 消息 ——
export interface Conversation {
  id: number;
  owner_id: number;
  title: string;
  source: string;
  agent_id?: number | null;
  last_message_at: string | null;
  created_at: string;
  updated_at: string;
}

export type MessageRole = 'user' | 'assistant' | 'system' | 'tool';

export interface Message {
  id: number;
  owner_id: number;
  conversation_id: number;
  role: MessageRole;
  content: string;
  content_type: string;
  run_id?: number | null;
  token_count: number;
  metadata_json?: string;
  created_at: string;
}

export interface MessageReference {
  id: number;
  owner_id: number;
  message_id: number;
  kb_id: number;
  document_id: number;
  chunk_id: number;
  ref_index: number;
  score: number;
  quote_text: string;
  page_no?: number | null;
  metadata_json?: string;
  created_at: string;
}

export interface ChatRequest {
  provider_id: number;
  kb_ids: number[];
  question: string;
  conversation_id?: number;
  model?: string;
  top_k?: number;
}

export interface ChatResponse {
  conversation: Conversation;
  user_message: Message;
  assistant_message: Message;
  references: MessageReference[];
  usage?: Record<string, unknown>;
}

// —— Agent / Flow / Run ——
export interface Agent {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  avatar_url: string;
  current_version_id: number | null;
  status: number;
  created_at: string;
  updated_at: string;
}

export interface CreateAgentRequest {
  name: string;
  description?: string;
  avatar_url?: string;
}

export interface UpdateAgentRequest {
  name?: string;
  description?: string;
  avatar_url?: string;
  status?: number;
}

export interface FlowVersion {
  id: number;
  owner_id: number;
  agent_id: number;
  version_no: number;
  dsl_json: string;
  description: string;
  is_draft: boolean;
  is_published: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateFlowVersionRequest {
  dsl_json: string;
  description?: string;
}

export type RunStatus = 'running' | 'succeeded' | 'failed' | 'cancelled';

export interface Run {
  id: number;
  owner_id: number;
  agent_id: number;
  flow_version_id: number;
  conversation_id: number | null;
  status: RunStatus;
  input_json: string;
  output_json: string;
  error_message: string;
  total_tokens: number;
  latency_ms: number;
  started_at: string;
  finished_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface RunEvent {
  id: number;
  owner_id: number;
  run_id: number;
  event_type: string;
  node_id: string;
  node_type: string;
  payload_json: string;
  created_at: string;
}

export interface NodeLog {
  id: number;
  owner_id: number;
  run_id: number;
  node_id: string;
  node_type: string;
  status: string;
  input_json: string;
  output_json: string;
  error_message: string;
  token_count: number;
  latency_ms: number;
  started_at: string;
  finished_at: string | null;
  created_at: string;
}

export interface RunAgentRequest {
  flow_version_id?: number;
  conversation_id?: number | null;
  input: Record<string, unknown>;
}
