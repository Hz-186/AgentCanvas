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

// —— Provider Catalog (内置供应商目录) ——
export interface CatalogModel {
  name: string;
  model_type: string;
  max_tokens?: number;
}

export interface ProviderCatalog {
  key: string;
  name: string;
  provider_type: ProviderType;
  base_url: string;
  doc_url?: string;
  rank: number;
  models: CatalogModel[];
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

// —— Memory / Tool ——
export interface Memory {
  id: number;
  owner_id: number;
  conversation_id: number | null;
  memory_type: string;
  title: string;
  content: string;
  importance: number;
  source: string;
  metadata_json?: unknown;
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface ToolDefinition {
  id: number;
  owner_id: number;
  name: string;
  tool_type: string;
  description: string;
  config_json: Record<string, unknown>;
  input_schema_json?: Record<string, unknown>;
  output_schema_json?: Record<string, unknown>;
  status: number;
  created_at: string;
  updated_at: string;
}

export interface ToolPolicy {
  id: number;
  owner_id: number;
  name: string;
  require_approval_for_risk?: string[];
  max_timeout_ms: number;
  max_output_bytes: number;
  allowed_hosts?: string[];
  credential_scope?: string;
  created_at: string;
  updated_at: string;
}

export interface ToolPack {
  id: number;
  owner_id: number;
  name: string;
  description?: string;
  created_at: string;
  updated_at: string;
}

export interface ToolPackItem {
  id: number;
  owner_id: number;
  pack_id: number;
  tool_id: number;
  created_at: string;
}

export interface MCPServer {
  id: number;
  owner_id: number;
  name: string;
  transport: 'sse' | 'stdio';
  endpoint_url: string;
  command: string;
  args_json?: string[] | unknown;
  env_json?: Record<string, string> | unknown;
  status: number;
  last_error: string;
  discovered_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface MCPToolCache {
  id: number;
  owner_id: number;
  server_id: number;
  tool_name: string;
  description: string;
  parameters_json?: unknown;
  schema_hash: string;
  cached_at: string;
  created_at: string;
  updated_at: string;
}

export interface Skill {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  skill_type: 'instruction' | 'bundle';
  source_type: 'inline' | 'local_path';
  entry_file: string;
  content_md?: string;
  bundle_path?: string;
  tags_json?: string[] | unknown;
  status: number;
  version: number;
  checksum: string;
  last_validated_at?: string | null;
  last_validation_error?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateSkillRequest {
  name: string;
  description: string;
  skill_type?: 'instruction' | 'bundle';
  source_type?: 'inline' | 'local_path';
  entry_file?: string;
  content_md?: string;
  bundle_path?: string;
  tags?: string[];
  status?: number;
}

export interface UpdateSkillRequest {
  name?: string;
  description?: string;
  skill_type?: 'instruction' | 'bundle';
  source_type?: 'inline' | 'local_path';
  entry_file?: string;
  content_md?: string;
  bundle_path?: string;
  tags?: string[];
  status?: number;
}

export interface SkillValidationResult {
  valid: boolean;
  error?: string;
  checksum?: string;
  validated_at: string;
  skill: Skill;
}

// —— 知识库 ——
export interface KnowledgeBase {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  retrieval_backend: string;
  retrieval_mode: string;
  embedding_provider_id: number | null;
  embedding_model: string;
  embedding_dimensions: number;
  hybrid_weight: number;
  rerank_enabled: boolean;
  rerank_provider_id: number | null;
  rerank_model: string;
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
  enabled: boolean;
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
  job?: IngestionJob;
}

export interface IngestionJob {
  id: number;
  owner_id: number;
  kb_id: number;
  document_id: number;
  job_type: string;
  status: string;
  error_message: string;
  attempt_count: number;
  max_attempts: number;
  started_at: string | null;
  finished_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface RetrievalResult {
  chunk_id: number;
  document_id: number;
  kb_id: number;
  score: number;
  keyword_score: number;
  vector_score: number;
  final_score: number;
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
  dialog_id?: number | null;
  title: string;
  source: string;
  workflow_id?: number | null;
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

export interface Dialog {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  provider_id: number;
  model: string;
  system_prompt: string;
  prologue: string;
  kb_ids: number[];
  top_k: number;
  retrieval_mode: string;
  history_round_limit: number;
  status: number;
  created_at: string;
  updated_at: string;
}

export interface ChatResponse {
  conversation: Conversation;
  user_message: Message;
  assistant_message: Message;
  references: MessageReference[];
  usage?: Record<string, unknown>;
}

// —— Workflow / Flow / Run ——
export interface Workflow {
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

export interface WorkflowProfile {
  id: number;
  owner_id: number;
  workflow_id: number;
  role: string;
  goal: string;
  backstory: string;
  system_prompt: string;
  default_provider_id: number | null;
  default_model: string;
  max_iterations: number;
  max_execution_time_ms: number;
  memory_enabled: boolean;
  planning_enabled: boolean;
  allow_delegation: boolean;
  allow_code_execution: boolean;
  default_tool_pack_ids?: number[];
  default_tool_ids?: number[];
  default_skill_ids?: number[];
  default_mcp_server_ids?: number[];
  default_knowledge_ids?: number[];
  default_knowledge_top_k?: number;
  default_knowledge_mode?: 'keyword' | 'vector' | 'hybrid';
  default_call_workflow_ids?: number[];
  default_max_workflow_call_depth?: number;
  output_schema_json?: unknown;
  tool_policy_json?: unknown;
  memory_policy_json?: unknown;
  context_policy_json?: unknown;
  risk_level?: 'low' | 'medium' | 'high';
  mode?: 'react' | 'plan_execute' | 'reflect' | 'supervisor';
  created_at: string;
  updated_at: string;
}

export interface CreateWorkflowRequest {
  name: string;
  description?: string;
  avatar_url?: string;
}

export interface UpdateWorkflowRequest {
  name?: string;
  description?: string;
  avatar_url?: string;
  status?: number;
}

export type UpdateWorkflowProfileRequest = Partial<Pick<
  WorkflowProfile,
  | 'role'
  | 'goal'
  | 'backstory'
  | 'system_prompt'
  | 'default_provider_id'
  | 'default_model'
  | 'max_iterations'
  | 'max_execution_time_ms'
  | 'memory_enabled'
  | 'planning_enabled'
  | 'allow_delegation'
  | 'allow_code_execution'
  | 'default_tool_pack_ids'
  | 'default_tool_ids'
  | 'default_skill_ids'
  | 'default_mcp_server_ids'
  | 'default_knowledge_ids'
  | 'default_knowledge_top_k'
  | 'default_knowledge_mode'
  | 'default_call_workflow_ids'
  | 'default_max_workflow_call_depth'
  | 'output_schema_json'
  | 'tool_policy_json'
  | 'memory_policy_json'
  | 'context_policy_json'
  | 'risk_level'
  | 'mode'
>>;

export interface EvalDataset {
  id: number;
  owner_id: number;
  workflow_id: number;
  name: string;
  description: string;
  status: number;
  created_at: string;
  updated_at: string;
}

export interface EvalCase {
  id: number;
  owner_id: number;
  dataset_id: number;
  name: string;
  input_json: unknown;
  expected_json?: unknown;
  tags_json?: unknown;
  required_tools_json?: unknown;
  created_at: string;
  updated_at: string;
}

export interface EvalRun {
  id: number;
  owner_id: number;
  workflow_id: number;
  dataset_id: number;
  flow_version_id: number;
  status: 'running' | 'completed' | 'failed';
  total_cases: number;
  passed_cases: number;
  failed_cases: number;
  success_rate: number;
  summary_json?: unknown;
  error_message: string;
  started_at: string;
  finished_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface EvalTrendPoint {
  eval_run_id: number;
  flow_version_id: number;
  status: 'running' | 'completed' | 'failed';
  total_cases: number;
  passed_cases: number;
  failed_cases: number;
  success_rate: number;
  metrics: Record<string, unknown>;
  started_at: string;
  finished_at: string | null;
}

export interface EvalTrend {
  dataset_id: number;
  workflow_id: number;
  points: EvalTrendPoint[];
  latest?: EvalTrendPoint;
  best?: EvalTrendPoint;
  delta: Record<string, unknown>;
  trend_summary: Record<string, unknown>;
}

export interface EvalResult {
  id: number;
  owner_id: number;
  eval_run_id: number;
  eval_case_id: number;
  workflow_run_id?: number | null;
  status: 'passed' | 'failed';
  score: number;
  reason: string;
  output_json?: unknown;
  metrics_json?: unknown;
  error_message: string;
  latency_ms: number;
  created_at: string;
}

export interface CreateEvalDatasetRequest {
  name: string;
  description?: string;
}

export interface CreateEvalCaseRequest {
  name: string;
  input_json: Record<string, unknown>;
  expected_json?: unknown;
  tags_json?: unknown;
  required_tools_json?: unknown;
}

export interface RunEvalDatasetRequest {
  flow_version_id?: number;
}

export interface RunEvalDatasetResponse {
  eval_run: EvalRun;
  results: EvalResult[];
}

export interface FlowVersion {
  id: number;
  owner_id: number;
  workflow_id: number;
  version_no: number;
  dsl_json: unknown;
  description: string;
  is_draft: boolean;
  is_published: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateFlowVersionRequest {
  dsl_json: unknown;
  description?: string;
}

export type RunStatus = 'running' | 'succeeded' | 'failed' | 'cancelled' | 'waiting_human' | 'paused' | 'resuming' | 'timeout';

export interface Run {
  id: number;
  owner_id: number;
  workflow_id: number;
  flow_version_id: number;
  conversation_id: number | null;
  parent_run_id?: number | null;
  caller_node_id?: string;
  call_depth?: number;
  call_chain_json?: unknown;
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

export interface ApprovalRequest {
  id: number;
  owner_id: number;
  workflow_id: number;
  run_id: number;
  node_id: string;
  tool_call_id: string;
  tool_name: string;
  risk_level: string;
  reason: string;
  request_json?: unknown;
  status: 'pending' | 'approved' | 'rejected';
  decision_note: string;
  decided_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface WorkflowTeam {
  id: number;
  owner_id: number;
  name: string;
  supervisor_workflow_id: number;
  handoff_strategy: 'supervisor' | 'handoff';
  max_depth: number;
  created_at: string;
  updated_at: string;
}

export interface WorkflowTeamMember {
  id: number;
  owner_id: number;
  team_id: number;
  workflow_id: number;
  role: string;
  created_at: string;
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

export interface RunStep {
  id: number;
  owner_id: number;
  run_id: number;
  node_id: string;
  step_index: number;
  step_type: string;
  role: string;
  content: string;
  tool_call_id: string;
  tool_name: string;
  arguments_json?: unknown;
  output_json?: unknown;
  compressed?: boolean;
  error_message: string;
  token_count: number;
  latency_ms: number;
  created_at: string;
}

export interface MemoryWriteLog {
  id: number;
  owner_id: number;
  memory_id: number;
  run_id: number;
  source_message_id: number;
  action: string;
  before_json?: unknown;
  after_json?: unknown;
  reason: string;
  created_at: string;
}

export interface ToolInvocation {
  id: number;
  owner_id: number;
  run_id: number;
  node_id: string;
  tool_id: number;
  tool_name: string;
  tool_type: string;
  input_json?: unknown;
  output_json?: unknown;
  status: string;
  error_message: string;
  latency_ms: number;
  created_at: string;
}

export interface RunTrace {
  run: Run;
  events: RunEvent[];
  node_logs: NodeLog[];
  steps: RunStep[];
  child_runs: Run[];
  memory_write_logs: MemoryWriteLog[];
  tool_invocations: ToolInvocation[];
  replay_summary: Record<string, unknown>;
}

export interface RunWorkflowRequest {
  flow_version_id?: number;
  conversation_id?: number | null;
  input: Record<string, unknown>;
}
