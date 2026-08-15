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
	parent_id?: number | null;
	conflict_flag?: boolean;
	scope_type: 'user' | 'agent' | 'conversation';
	scope_id: number;
	status: 'active' | 'superseded' | 'revoked';
	supersedes_id?: number | null;
	memory_type: string;
	memory_level?: 'working' | 'short_term' | 'long_term';
  title: string;
  content: string;
  importance: number;
  source: string;
  metadata_json?: unknown;
	last_used_at: string | null;
	last_decay_at?: string | null;
	access_count?: number;
  expires_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface MemoryRecallDetail {
	memory_id: number;
	source: string;
	scope_type: string;
	scope_id: number;
	score: number;
	reason: string;
	token_cost: number;
}

export interface MemoryRecallLog {
	id: number;
	owner_id: number;
	agent_id: number;
	conversation_id: number;
	run_id: number;
	query: string;
	candidate_json: Record<string, number>;
	injected_json: MemoryRecallDetail[];
	token_cost: number;
	feedback: '' | 'helpful' | 'irrelevant' | 'incorrect';
	created_at: string;
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
  transport: 'streamable_http' | 'stdio';
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
  query_plan?: QueryPlan;
  clarification?: RetrievalClarification;
  diagnostics?: Record<string, unknown>;
  trace?: Array<Record<string, unknown>>;
}

export interface QueryHardConstraint {
  kind: string;
  value: string;
}

export interface QueryPlan {
  original_query: string;
  normalized_query: string;
  resolved_query?: string;
  precise_query?: string;
  hard_constraints?: QueryHardConstraint[];
  paraphrases?: string[];
  synonym_queries?: string[];
  subqueries?: string[];
  unresolved_references?: string[];
  needs_clarification: boolean;
  rewrite_invoked: boolean;
  confidence: number;
  clarification_question?: string;
}

export interface RetrievalClarification {
  required: boolean;
  question: string;
  unresolved_references?: string[];
}

// —— 会话 / 消息 ——
export interface Conversation {
  id: number;
  owner_id: number;
  title: string;
  source: string;
  agent_id?: number | null;
  agent_release_id?: number | null;
  project_id?: number | null;
  workspace_mode?: 'shared' | 'worktree';
  agent_mode?: 'react' | 'plan_execute';
  parent_conversation_id?: number | null;
  last_message_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface AgentEditableSettings {
  provider_id: number;
  model: string;
  system_prompt: string;
  knowledge_ids: number[];
  python_tool_names?: string[];
  temperature?: number;
}

export interface Agent {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  avatar_url: string;
  status: 'draft' | 'active' | 'archived';
  settings: AgentEditableSettings;
  current_release_id?: number | null;
  created_at: string;
  updated_at: string;
}

export interface AgentTurn {
  id: number;
  owner_id: number;
  agent_id: number;
  agent_release_id: number;
  conversation_id: number;
  run_id?: number | null;
  user_message_id: number;
  assistant_message_id?: number | null;
  idempotency_key: string;
  status: 'queued' | 'retry_wait' | 'running' | 'waiting_human' | 'paused' | 'succeeded' | 'failed' | 'cancelled';
  input_json?: unknown;
  output_json?: unknown;
  error_message: string;
  started_at?: string | null;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface AgentTurnAccepted {
  turn: AgentTurn;
  run: Run;
  user_message: Message;
}

export interface ImprovementReview {
  id: number;
  owner_id: number;
  agent_id: number;
  agent_release_id: number;
  conversation_id: number;
  turn_id: number;
  run_id: number;
  provider_id: number;
  model: string;
  status: 'pending' | 'running' | 'completed' | 'failed';
  attempt_count: number;
  error_message?: string;
  created_at: string;
  completed_at?: string | null;
}

export interface ChangeProposal {
  id: number;
  owner_id: number;
  agent_id: number;
  review_id: number;
  turn_id: number;
  run_id: number;
  kind: 'memory' | 'reflection' | 'skill' | 'rule';
  title: string;
  content: string;
  payload_json?: unknown;
  evidence_json?: unknown;
  diff_json?: unknown;
  confidence: number;
  checksum: string;
  security_status: string;
  security_reason?: string;
  status: 'pending' | 'approved' | 'rejected' | 'applied' | 'rejected_security';
  decision_note?: string;
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

export interface MessageSearchResult {
  message_id: number;
  agent_id: number;
  conversation_id: number;
  role: string;
  content: string;
  score: number;
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

export type ResourceSummaryKind = 'skills' | 'memories' | 'http-tools' | 'knowledge-bases';

export interface ResourceSummary {
  id: number;
  name: string;
  description?: string;
  status?: number;
  resource_type?: string;
  updated_at: string;
  current_version_id?: number | null;
  document_count?: number;
  chunk_count?: number;
}

export interface ResourceSummaryPage {
  items: ResourceSummary[];
  next_cursor?: string;
  has_more: boolean;
}

export type ReflectionStatus =
  | 'candidate'
  | 'active'
  | 'validated'
  | 'disputed'
  | 'superseded'
  | 'archived';

export type ReflectionKind = 'error_lesson' | 'important_strategy';
export type ReflectionScope = 'agent' | 'global';

export interface AgentReflection {
  id: number;
  owner_id: number;
  agent_id: number;
  source_run_id: number;
  supersedes_id?: number | null;
  scope: ReflectionScope;
  kind: ReflectionKind;
  status: ReflectionStatus;
  mode: string;
  trigger_type: string;
  task_fingerprint: string;
  task_summary: string;
  root_cause_category: string;
  root_cause: string;
  corrective_action: string;
  lesson: string;
  applicability: string;
  evidence_json?: unknown;
  tags_json?: unknown;
  importance: number;
  confidence: number;
  content_hash: string;
  recall_count: number;
  successful_use_count: number;
  harmful_count: number;
  last_recalled_at?: string | null;
  expires_at?: string | null;
  created_at: string;
  updated_at: string;
}

export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'waiting_human' | 'paused' | 'resuming' | 'timeout';

export interface Run {
  id: number;
  owner_id: number;
  agent_id: number;
  agent_release_id?: number | null;
  conversation_id?: number | null;
  workspace_id?: number | null;
  workspace?: Workspace | null;
  parent_run_id?: number | null;
  run_type: "turn" | "subagent";
  delegation_depth: number;
  definition_hash?: string;
  rule_hash?: string;
  status: RunStatus;
  input_json: string;
  output_json: string;
  error_message: string;
  total_tokens: number;
  latency_ms: number;
  started_at: string;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

export type WorkspaceKind = 'shared' | 'worktree';
export type WorkspaceStatus = 'creating' | 'ready' | 'failed' | 'preserved' | 'cleaned';
export interface ProjectFolder { id: number; owner_id: number; project_id: number; path: string; label: string; is_primary: boolean; added_at: string; }
export interface Project { id: number; owner_id: number; slug: string; name: string; description: string; icon: string; color: string; primary_path: string; archived: boolean; folders: ProjectFolder[]; created_at: string; updated_at: string; }
export interface Workspace { id: number; owner_id: number; project_id: number; run_id: number; parent_workspace_id?: number | null; kind: WorkspaceKind; repository_root: string; workspace_path: string; branch_name: string; base_ref: string; base_sha: string; head_sha: string; status: WorkspaceStatus; dirty: boolean; unpushed: boolean; locked: boolean; lock_reason: string; cleanup_reason: string; error_message: string; last_checked_at?: string | null; cleaned_at?: string | null; created_at: string; updated_at: string; }
export interface GitStatus { root: string; branch: string; head: string; dirty: boolean; unpushed: boolean; staged?: string[]; changed?: string[]; untracked?: string[]; }
export interface GitWorktree { path: string; branch?: string; head?: string; detached: boolean; bare: boolean; locked: boolean; lock_reason?: string; prunable?: boolean; }

export interface ApprovalRequest {
  id: number;
  owner_id: number;
  run_id: number;
  tool_call_id: string;
  interaction_id?: string;
  tool_name: string;
  risk_level: string;
  reason: string;
  request_json?: unknown;
  options?: ApprovalOption[];
  status: "pending" | "approved" | "rejected";
  decision_note: string;
  decided_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface ApprovalOption {
  id: string;
  label: string;
  description?: string;
}

export interface RunEvent {
  id: number;
  owner_id: number;
  run_id: number;
  event_type: string;
  payload_json: string;
  created_at: string;
}

export interface RunStep {
  id: number;
  owner_id: number;
  run_id: number;
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
  provider_id: number;
  model: string;
  created_at: string;
}

export interface RunTrace {
  run: Run;
  events: RunEvent[];
  steps: RunStep[];
  children: Run[];
}
