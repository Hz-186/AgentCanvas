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
  enabled: boolean;
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
  enabled?: boolean;
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
	capabilities?: {
		chat: boolean;
		tool_calling: boolean;
		streaming: boolean;
		embedding: boolean;
	};
}

// —— API Token ——
export interface ApiToken {
  id: number;
  owner_id: number;
  name: string;
  token_prefix: string;
  scopes: string;
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
  detail_json: Record<string, unknown>;
  ip_address: string;
  user_agent: string;
  created_at: string;
}

// —— Memory / Tool ——
export interface Memory {
  id: number;
  owner_id: number;
	source_conversation_id: number | null;
	conflict_with_id?: number | null;
	has_conflict?: boolean;
	source_project_id?: number | null;
	scope_type: 'user' | 'agent' | 'conversation' | 'project';
	scope_id: number;
	status: 'active' | 'superseded' | 'revoked';
	supersedes_id?: number | null;
	/** Legacy SQL provenance; Codex file memory has no cognitive taxonomy. */
	memory_type?: string;
	retention_tier?: 'short_term' | 'long_term';
  title: string;
  content: string;
  importance: number;
  source: string;
  metadata_json?: Record<string, unknown>;
	last_recalled_at: string | null;
	last_decay_at?: string | null;
	recall_count?: number;
	promotion_count?: number;
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
  enabled: boolean;
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
  tool_pack_id: number;
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
  env_json?: never;
  enabled: boolean;
  discovery_error: string;
  tools_discovered_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface MCPServerRequest {
  name?: string;
  transport?: 'streamable_http' | 'stdio';
  endpoint_url?: string;
  command?: string;
  args_json?: string[];
  env_json?: Record<string, string>;
  enabled?: boolean;
}

export interface MCPToolCache {
  id: number;
  owner_id: number;
  mcp_server_id: number;
  tool_name: string;
  description: string;
  input_schema_json?: unknown;
  created_at: string;
}

export interface Skill {
  id: number;
  owner_id: number;
  name: string;
  description: string;
  skill_type: 'instruction' | 'bundle';
  source_type: 'inline' | 'local_path';
  entry_file: string;
  content_markdown?: string;
  bundle_path?: string;
  tags_json?: string[] | unknown;
  enabled: boolean;
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
  content_markdown?: string;
  bundle_path?: string;
  tags?: string[];
  enabled?: boolean;
}

export interface UpdateSkillRequest {
  name?: string;
  description?: string;
  skill_type?: 'instruction' | 'bundle';
  source_type?: 'inline' | 'local_path';
  entry_file?: string;
  content_markdown?: string;
  bundle_path?: string;
  tags?: string[];
  enabled?: boolean;
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
  embedding_metric?: 'COSINE' | 'IP' | 'L2';
  vector_weight: number;
  rerank_enabled: boolean;
  rerank_provider_id: number | null;
  rerank_model: string;
  chunk_method: string;
  chunk_size: number;
  chunk_overlap: number;
  enabled: boolean;
  document_count: number;
  chunk_count: number;
  created_at: string;
  updated_at: string;
}

export type IngestionStatus =
  | 'pending'
  | 'parsing'
  | 'chunking'
  | 'indexing'
  | 'completed'
  | 'failed';

export interface AgentDocument {
  id: number;
  owner_id: number;
  knowledge_base_id: number;
  name: string;
  original_filename: string;
  file_type: string;
  mime_type: string;
  file_size_bytes: number;
  storage_object_key: string;
  content_hash: string;
  active_generation_id?: string;
  ingestion_status: IngestionStatus;
  ingestion_error?: string;
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
  knowledge_base_id: number;
  document_id: number;
  chunk_index: number;
  content: string;
  content_hash: string;
  token_count: number;
  char_count: number;
  page_number: number | null;
  section_title: string;
  metadata_json: Record<string, unknown>;
  created_at: string;
}

export interface UploadDocumentResponse {
  document: AgentDocument;
  job?: IngestionJob;
}

export interface IngestionJob {
  id: number;
  owner_id: number;
  knowledge_base_id: number;
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
  knowledge_base_id: number;
  score: number;
  keyword_score: number;
  vector_score: number;
  final_score: number;
  content: string;
  highlight: string;
  document_name: string;
  page_number: number | null;
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
  knowledge_base_ids: number[];
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

export type MessageContentType = 'text' | 'function_call' | 'function_call_output' | 'reasoning' | 'system_echo';

export interface Message {
  id: number;
  owner_id: number;
  conversation_id: number;
  role: MessageRole;
  content: string;
  content_type?: MessageContentType;
  tool_call_id?: string;
  tool_name?: string;
  run_id?: number | null;
  token_count: number;
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

export type ResourceSummaryKind = 'skills' | 'memories' | 'http-tools' | 'knowledge-bases';

export interface ResourceSummary {
  id: number;
  name: string;
  description?: string;
  enabled: boolean;
  resource_type?: string;
  updated_at: string;
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
export interface ProjectFolder { id: number; owner_id: number; project_id: number; path: string; label: string; is_repository_root: boolean; added_at: string; }
export interface Project { id: number; owner_id: number; slug: string; name: string; description: string; repository_root: string; archived: boolean; folders: ProjectFolder[]; created_at: string; updated_at: string; }
export interface Workspace { id: number; owner_id: number; project_id: number; run_id: number; parent_workspace_id?: number | null; kind: WorkspaceKind; repository_root: string; workspace_path: string; branch_name: string; base_ref: string; base_sha: string; head_sha: string; status: WorkspaceStatus; dirty: boolean; has_unpushed_commits: boolean; locked: boolean; lock_reason: string; cleanup_reason: string; error_message: string; last_checked_at?: string | null; cleaned_at?: string | null; created_at: string; updated_at: string; }
export interface GitStatus { root: string; branch: string; head: string; dirty: boolean; has_unpushed_commits: boolean; staged?: string[]; changed?: string[]; untracked?: string[]; }
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
