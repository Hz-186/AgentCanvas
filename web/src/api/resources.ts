import { api, request } from './client';
import { streamGet, type SSEMessage } from './sse';
import type {
  AgentReflection,
  ReflectionStatus,
  ApprovalRequest,
  AgentDocument,
  ApiToken,
  ApiTokenCreated,
  AuditLog,
	Conversation,
  CreateProviderRequest,
  DocumentChunk,
  IngestionJob,
  KnowledgeBase,
  Message,
  MessageSearchResult,
  MCPServer,
  MCPToolCache,
  ModelProvider,
  ProviderCatalog,
  RetrievalResponse,
  Run,
  RunEvent,
  RunTrace,
  RunStep,
  Memory,
	MemoryRecallLog,
  ToolDefinition,
  ToolPack,
  ToolPackItem,
  ToolPolicy,
  Skill,
  CreateSkillRequest,
  UpdateSkillRequest,
  SkillValidationResult,
  UpdateProviderRequest,
  UploadDocumentResponse,
  ResourceSummaryKind,
  ResourceSummaryPage,
  Agent,
  AgentEditableSettings,
  AgentTurn,
  AgentTurnAccepted,
  ImprovementReview,
  ChangeProposal,
  Project,
  ProjectFolder,
  Workspace,
  GitStatus,
  GitWorktree,
} from '../types/api';

export const agentApi = {
  list: () => api.get<Agent[]>('/agents'),
  get: (id: number) => api.get<Agent>(`/agents/${id}`),
  create: (body: { name: string; description?: string; avatar_url?: string; settings: AgentEditableSettings }) => api.post<Agent>('/agents', body),
  update: (id: number, body: { name?: string; description?: string; avatar_url?: string }) => api.patch<Agent>(`/agents/${id}`, body),
  updateSettings: (id: number, settings: AgentEditableSettings) => api.patch<Agent>(`/agents/${id}/settings`, settings),
  remove: (id: number) => api.delete<{ success: boolean }>(`/agents/${id}`),
  createConversation: (id: number, title?: string, mode: Conversation['agent_mode'] = 'react', projectId?: number, workspaceMode: Conversation['workspace_mode'] = 'shared') => api.post<Conversation>(`/agents/${id}/conversations`, { title, mode, project_id: projectId, workspace_mode: workspaceMode }),
  listConversations: (id: number) => api.get<Conversation[]>(`/agents/${id}/conversations`),
  listMessages: (id: number, conversationId: number) => api.get<Message[]>(`/agents/${id}/conversations/${conversationId}/messages`),
  updateConversationMode: (id: number, conversationId: number, mode: NonNullable<Conversation['agent_mode']>) => api.patch<Conversation>(`/agents/${id}/conversations/${conversationId}/mode`, { mode }),
  removeConversation: (id: number, conversationId: number) => api.delete<{ success: boolean }>(`/agents/${id}/conversations/${conversationId}`),
  forkConversation: (id: number, conversationId: number) => api.post<Conversation>(`/agents/${id}/conversations/${conversationId}/fork`),
  upgradeConversation: (id: number, conversationId: number) => api.post<Conversation>(`/agents/${id}/conversations/${conversationId}/upgrade`),
  startTurn: (id: number, conversationId: number, content: string, idempotencyKey: string) => api.post<AgentTurnAccepted>(`/agents/${id}/conversations/${conversationId}/turns`, { content }, { headers: { 'Idempotency-Key': idempotencyKey } }),
  getTurn: (id: number) => api.get<AgentTurn>(`/agent-turns/${id}`),
  getLatestTurn: (id: number, conversationId: number) => api.get<AgentTurn>(`/agents/${id}/conversations/${conversationId}/turns/latest`),
  streamRunEventsV1: (runId: number, lastEventId: string | undefined, handlers: { onMessage: (msg: SSEMessage) => void; onError?: (err: Error) => void; signal?: AbortSignal }) =>
    streamGet(`/runs/${runId}/events/stream/v1`, { lastEventId, ...handlers }),
  searchSessions: (id: number, query: string, limit = 10) => api.get<MessageSearchResult[]>(`/agents/${id}/session-search`, { q: query, limit }),
  listImprovementReviews: (id: number) => api.get<ImprovementReview[]>(`/agents/${id}/improvement-reviews`),
  listChangeProposals: (id: number, status?: ChangeProposal['status']) => api.get<ChangeProposal[]>(`/agents/${id}/change-proposals`, status ? { status } : undefined),
  approveChangeProposal: (id: number, note?: string) => api.post<ChangeProposal>(`/agent-change-proposals/${id}/approve`, { note }),
  rejectChangeProposal: (id: number, note?: string) => api.post<ChangeProposal>(`/agent-change-proposals/${id}/reject`, { note }),
  listReflections: (id: number, status?: ReflectionStatus) =>
    api.get<AgentReflection[]>(`/agents/${id}/reflections`, status ? { status } : undefined),
  setReflectionStatus: (
    id: number,
    reflectionId: number,
    status: Extract<ReflectionStatus, 'active' | 'validated' | 'disputed' | 'archived'>,
  ) => api.patch<{ success: boolean }>(`/agents/${id}/reflections/${reflectionId}`, { status }),
};

export const projectApi = {
  list: (includeArchived = false) => api.get<Project[]>('/projects', includeArchived ? { include_archived: 'true' } : undefined),
  get: (id: number) => api.get<Project>(`/projects/${id}`),
  create: (body: { name: string; slug?: string; description?: string; primary_path: string; initialize_git?: boolean }) => api.post<Project>('/projects', body),
  update: (id: number, body: Partial<Pick<Project, 'name' | 'description' | 'icon' | 'color'>>) => api.patch<Project>(`/projects/${id}`, body),
  remove: (id: number) => api.delete<{ success: boolean }>(`/projects/${id}`),
  folders: (id: number) => api.get<ProjectFolder[]>(`/projects/${id}/folders`),
  addFolder: (id: number, body: { path: string; label?: string; is_primary?: boolean }) => api.post<ProjectFolder>(`/projects/${id}/folders`, body),
  removeFolder: (id: number, folderId: number) => api.delete<{ success: boolean }>(`/projects/${id}/folders/${folderId}`),
  status: (id: number) => api.get<GitStatus>(`/projects/${id}/git/status`),
  branches: (id: number) => api.get<string[]>(`/projects/${id}/git/branches`),
  worktrees: (id: number) => api.get<GitWorktree[]>(`/projects/${id}/git/worktrees`),
};

export const resourceSummaryApi = {
  list: (kind: ResourceSummaryKind, params?: { limit?: number; cursor?: string }) =>
    api.get<ResourceSummaryPage>(`/resource-summaries/${kind}`, params),
};

export const runApi = {
  getRun: (id: number) => api.get<Run>(`/runs/${id}`),
  listRunEvents: (id: number) => api.get<RunEvent[]>(`/runs/${id}/events`),
  listChildRuns: (id: number) => api.get<Run[]>(`/runs/${id}/children`),
  listRunSteps: (id: number) => api.get<RunStep[]>(`/runs/${id}/steps`),
  getRunTrace: (id: number) => api.get<RunTrace>(`/runs/${id}/trace`),
  cancelRun: (id: number) => api.post<Run>(`/runs/${id}/cancel`),
  resumeRun: (id: number) => api.post<Run>(`/runs/${id}/resume`),
  workspace: (id: number) => api.get<Workspace>(`/runs/${id}/workspace`),
  gitStatus: (id: number) => api.get<GitStatus>(`/runs/${id}/git/status`),
  gitDiff: (id: number, staged = false) => api.get<{ diff: string }>(`/runs/${id}/git/diff`, staged ? { staged: 'true' } : undefined),
  gitLog: (id: number, limit = 20) => api.get<{ log: string }>(`/runs/${id}/git/log`, { limit }),
  gitCommit: (id: number, message: string, paths?: string[]) => api.post<{ hash: string; message: string; paths: string[] }>(`/runs/${id}/git/commit`, { message, paths }),
  cleanupWorkspace: (id: number, force = false) => request<Workspace>(`/workspaces/${id}/cleanup`, { method: 'POST', query: force ? { force: 'true' } : undefined }),
  refreshWorkspace: (id: number) => api.post<Workspace>(`/workspaces/${id}/refresh`),
  feedbackReflection: (runId: number, reflectionId: number, verdict: 'helpful' | 'harmful', note?: string) =>
    api.post<{ success: boolean }>(`/runs/${runId}/reflections/${reflectionId}/feedback`, { verdict, note }),
  listApprovalRequests: (status?: 'pending' | 'approved' | 'rejected') =>
    api.get<ApprovalRequest[]>('/approval-requests', status ? { status } : undefined),
  approveRequest: (id: number, note?: string) => api.post<ApprovalRequest>(`/approval-requests/${id}/approve`, { note }),
  rejectRequest: (id: number, note?: string) => api.post<ApprovalRequest>(`/approval-requests/${id}/reject`, { note }),
};

export const knowledgeApi = {
  list: () => api.get<KnowledgeBase[]>('/knowledge-bases'),
  get: (id: number) => api.get<KnowledgeBase>(`/knowledge-bases/${id}`),
  create: (body: {
    name: string;
    description?: string;
    retrieval_mode?: string;
    embedding_provider_id?: number;
    embedding_model?: string;
    embedding_dimensions?: number;
    embedding_metric?: 'COSINE' | 'IP' | 'L2';
    hybrid_weight?: number;
    rerank_enabled?: boolean;
    rerank_provider_id?: number;
    rerank_model?: string;
    chunk_method?: string;
    chunk_size?: number;
    chunk_overlap?: number;
  }) =>
    api.post<KnowledgeBase>('/knowledge-bases', body),
  update: (
    id: number,
    body: {
      name?: string;
      description?: string;
      retrieval_mode?: string;
      embedding_provider_id?: number;
      embedding_model?: string;
      embedding_dimensions?: number;
      embedding_metric?: 'COSINE' | 'IP' | 'L2';
      hybrid_weight?: number;
      rerank_enabled?: boolean;
      rerank_provider_id?: number;
      rerank_model?: string;
      chunk_method?: string;
      chunk_size?: number;
      chunk_overlap?: number;
      status?: number;
    },
  ) => api.patch<KnowledgeBase>(`/knowledge-bases/${id}`, body),
  remove: (id: number) => api.delete<{ success: boolean }>(`/knowledge-bases/${id}`),
  reindex: (id: number) => api.post<{ job_count: number }>(`/knowledge-bases/${id}/reindex`),
  listDocuments: (id: number) => api.get<AgentDocument[]>(`/knowledge-bases/${id}/documents`),
  uploadDocument: (kbId: number, file: File, name?: string) => {
    const form = new FormData();
    form.set('file', file);
    if (name) form.set('name', name);
    return api.upload<UploadDocumentResponse>(`/knowledge-bases/${kbId}/documents`, form);
  },
  getIngestionJob: (id: number) => api.get<IngestionJob>(`/ingestion-jobs/${id}`),
  listChunks: (documentId: number) => api.get<DocumentChunk[]>(`/documents/${documentId}/chunks`),
  setDocumentEnabled: (documentId: number, enabled: boolean) =>
    api.patch<AgentDocument>(`/documents/${documentId}`, { enabled }),
  deleteDocument: (documentId: number) => api.delete<{ success: boolean }>(`/documents/${documentId}`),
  search: (kbId: number, body: { query: string; top_k?: number; mode?: string }) =>
    api.post<RetrievalResponse>(`/knowledge-bases/${kbId}/search`, body),
};

export const settingsApi = {
  providers: {
    catalog: () => api.get<ProviderCatalog[]>('/provider-catalog'),
    list: () => api.get<ModelProvider[]>('/model-providers'),
    create: (body: CreateProviderRequest) => api.post<ModelProvider>('/model-providers', body),
    update: (id: number, body: UpdateProviderRequest) => api.patch<ModelProvider>(`/model-providers/${id}`, body),
    remove: (id: number) => api.delete<{ success: boolean }>(`/model-providers/${id}`),
    test: (id: number) => api.post<ModelProvider>(`/model-providers/${id}/test`),
  },
  tokens: {
    list: () => api.get<ApiToken[]>('/api-tokens'),
    create: (body: { name: string; scopes?: string[]; expires_at?: string | null }) =>
      api.post<ApiTokenCreated>('/api-tokens', body),
    remove: (id: number) => api.delete<{ success: boolean }>(`/api-tokens/${id}`),
  },
  audits: {
    list: (limit = 30, offset = 0) => api.get<AuditLog[]>('/audit-logs', { limit, offset }),
  },
  memories: {
	list: (params?: { memory_type?: string; status?: string; scope_type?: string; scope_id?: number; source?: string; conversation_id?: number }) => api.get<Memory[]>('/memories', params),
    create: (body: { memory_type: string; title?: string; content: string; importance?: number; source?: string }) =>
      api.post<Memory>('/memories', body),
	update: (id: number, body: Partial<Pick<Memory, 'memory_type' | 'title' | 'content' | 'importance' | 'source' | 'metadata_json'>>) => api.patch<Memory>(`/memories/${id}`, body),
    remove: (id: number) => api.delete<{ success: boolean }>(`/memories/${id}`),
	listCandidates: (status?: ChangeProposal['status']) => api.get<ChangeProposal[]>('/memory-candidates', status ? { status } : undefined),
	approveCandidate: (id: number, note = '') => api.post<ChangeProposal>(`/memory-candidates/${id}/approve`, { note }),
	rejectCandidate: (id: number, note = '') => api.post<ChangeProposal>(`/memory-candidates/${id}/reject`, { note }),
	listRecallLogs: (memoryId?: number) => api.get<MemoryRecallLog[]>('/memory-recall-logs', memoryId ? { memory_id: memoryId } : undefined),
	setRecallFeedback: (id: number, feedback: MemoryRecallLog['feedback']) => api.post<{ success: boolean }>(`/memory-recall-logs/${id}/feedback`, { feedback }),
  },
  tools: {
    list: () => api.get<ToolDefinition[]>('/tool-definitions'),
    get: (id: number) => api.get<ToolDefinition>(`/tool-definitions/${id}`),
    create: (body: { name: string; tool_type?: string; description?: string; config_json: Record<string, unknown> }) =>
      api.post<ToolDefinition>('/tool-definitions', body),
    update: (id: number, body: Partial<Pick<ToolDefinition, 'name' | 'description' | 'config_json' | 'status'>>) =>
      api.patch<ToolDefinition>(`/tool-definitions/${id}`, body),
    remove: (id: number) => api.delete<{ success: boolean }>(`/tool-definitions/${id}`),
    test: (id: number, input: Record<string, unknown>) => api.post<Record<string, unknown>>(`/tool-definitions/${id}/test`, { input }),
    listPolicies: () => api.get<ToolPolicy[]>('/tool-policies'),
    createPolicy: (body: Partial<ToolPolicy>) => api.post<ToolPolicy>('/tool-policies', body),
    updatePolicy: (id: number, body: Partial<ToolPolicy>) => api.patch<ToolPolicy>(`/tool-policies/${id}`, body),
    removePolicy: (id: number) => api.delete<{ success: boolean }>(`/tool-policies/${id}`),
    listPacks: () => api.get<ToolPack[]>('/tool-packs'),
    createPack: (body: { name: string; description?: string }) => api.post<ToolPack>('/tool-packs', body),
    updatePack: (id: number, body: Partial<ToolPack>) => api.patch<ToolPack>(`/tool-packs/${id}`, body),
    removePack: (id: number) => api.delete<{ success: boolean }>(`/tool-packs/${id}`),
    listPackItems: (packId: number) => api.get<ToolPackItem[]>(`/tool-packs/${packId}/items`),
    addPackItem: (packId: number, toolId: number) => api.post<ToolPackItem>(`/tool-packs/${packId}/items`, { tool_id: toolId }),
    removePackItem: (packId: number, toolId: number) =>
      request<{ success: boolean }>(`/tool-packs/${packId}/items`, { method: 'DELETE', body: { tool_id: toolId } }),
    listMCPServers: () => api.get<MCPServer[]>('/mcp-servers'),
    createMCPServer: (body: { name: string; transport: 'streamable_http' | 'stdio'; endpoint_url?: string; command?: string; args_json?: string[]; env_json?: Record<string, string> }) =>
      api.post<MCPServer>('/mcp-servers', body),
    updateMCPServer: (id: number, body: Partial<MCPServer>) => api.patch<MCPServer>(`/mcp-servers/${id}`, body),
    removeMCPServer: (id: number) => api.delete<{ success: boolean }>(`/mcp-servers/${id}`),
    refreshMCPServer: (id: number) => api.post<{ server: MCPServer; tools: MCPToolCache[] }>(`/mcp-servers/${id}/refresh`),
    listMCPTools: (id: number) => api.get<MCPToolCache[]>(`/mcp-servers/${id}/tools`),
  },
  skills: {
    list: () => api.get<Skill[]>('/skills'),
    get: (id: number) => api.get<Skill>(`/skills/${id}`),
    create: (body: CreateSkillRequest) => api.post<Skill>('/skills', body),
    update: (id: number, body: UpdateSkillRequest) => api.patch<Skill>(`/skills/${id}`, body),
    remove: (id: number) => api.delete<{ success: boolean }>(`/skills/${id}`),
    validate: (id: number) => api.post<SkillValidationResult>(`/skills/${id}/validate`),
  },
};

export function publicRequest<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body, auth: false });
}
