import { api, request } from './client';
import { streamPost, type SSEMessage } from './sse';
import type {
  Agent,
  AgentProfile,
  AgentTeam,
  AgentTeamMember,
  ApprovalRequest,
  AgentDocument,
  ApiToken,
  ApiTokenCreated,
  AuditLog,
  ChatRequest,
  ChatResponse,
  Conversation,
  Dialog,
  EvalCase,
  EvalDataset,
  EvalResult,
  EvalRun,
  CreateAgentRequest,
  CreateEvalCaseRequest,
  CreateEvalDatasetRequest,
  CreateFlowVersionRequest,
  CreateProviderRequest,
  DocumentChunk,
  FlowVersion,
  IngestionJob,
  KnowledgeBase,
  Message,
  MCPServer,
  MCPToolCache,
  ModelProvider,
  ProviderCatalog,
  RetrievalResponse,
  Run,
  RunAgentRequest,
  RunEvalDatasetRequest,
  RunEvalDatasetResponse,
  RunEvent,
  RunStep,
  NodeLog,
  Memory,
  MemoryWriteLog,
  ToolDefinition,
  ToolInvocation,
  ToolPack,
  ToolPackItem,
  ToolPolicy,
  UpdateAgentRequest,
  UpdateAgentProfileRequest,
  UpdateProviderRequest,
  UploadDocumentResponse,
} from '../types/api';

export const agentApi = {
  list: () => api.get<Agent[]>('/agents'),
  get: (id: number) => api.get<Agent>(`/agents/${id}`),
  getProfile: (id: number) => api.get<AgentProfile>(`/agents/${id}/profile`),
  updateProfile: (id: number, body: UpdateAgentProfileRequest) => api.patch<AgentProfile>(`/agents/${id}/profile`, body),
  create: (body: CreateAgentRequest) => api.post<Agent>('/agents', body),
  update: (id: number, body: UpdateAgentRequest) => api.patch<Agent>(`/agents/${id}`, body),
  remove: (id: number) => api.delete<{ success: boolean }>(`/agents/${id}`),
  createEvalDataset: (agentId: number, body: CreateEvalDatasetRequest) =>
    api.post<EvalDataset>(`/agents/${agentId}/eval-datasets`, body),
  listEvalDatasets: (agentId: number) => api.get<EvalDataset[]>(`/agents/${agentId}/eval-datasets`),
  createEvalCase: (datasetId: number, body: CreateEvalCaseRequest) =>
    api.post<EvalCase>(`/eval-datasets/${datasetId}/cases`, body),
  listEvalCases: (datasetId: number) => api.get<EvalCase[]>(`/eval-datasets/${datasetId}/cases`),
  runEvalDataset: (datasetId: number, body: RunEvalDatasetRequest = {}) =>
    api.post<RunEvalDatasetResponse>(`/eval-datasets/${datasetId}/runs`, body),
  listEvalRuns: (datasetId: number) => api.get<EvalRun[]>(`/eval-datasets/${datasetId}/runs`),
  listEvalResults: (evalRunId: number) => api.get<EvalResult[]>(`/eval-runs/${evalRunId}/results`),
  createFlowVersion: (agentId: number, body: CreateFlowVersionRequest) =>
    api.post<FlowVersion>(`/agents/${agentId}/flow-versions`, body),
  listFlowVersions: (agentId: number) => api.get<FlowVersion[]>(`/agents/${agentId}/flow-versions`),
  getFlowVersion: (id: number) => api.get<FlowVersion>(`/flow-versions/${id}`),
  publishFlowVersion: (id: number) => api.post<FlowVersion>(`/flow-versions/${id}/publish`),
  validateFlowVersion: (id: number) => api.post<{ valid: boolean }>(`/flow-versions/${id}/validate`),
  run: (agentId: number, body: RunAgentRequest) => api.post<{ run: Run; output: Record<string, unknown> }>(`/agents/${agentId}/runs`, body),
  streamRun: (
    agentId: number,
    body: RunAgentRequest,
    handlers: { onMessage: (msg: SSEMessage) => void; onError?: (err: Error) => void; signal?: AbortSignal },
  ) => streamPost(`/agents/${agentId}/runs/stream`, { body, ...handlers }),
  getRun: (id: number) => api.get<Run>(`/runs/${id}`),
  listRunEvents: (id: number) => api.get<RunEvent[]>(`/runs/${id}/events`),
  listChildRuns: (id: number) => api.get<Run[]>(`/runs/${id}/children`),
  listNodeLogs: (id: number) => api.get<NodeLog[]>(`/runs/${id}/node-logs`),
  listRunSteps: (id: number) => api.get<RunStep[]>(`/runs/${id}/steps`),
  listMemoryWriteLogs: (id: number) => api.get<MemoryWriteLog[]>(`/runs/${id}/memory-write-logs`),
  listToolInvocations: (id: number) => api.get<ToolInvocation[]>(`/runs/${id}/tool-invocations`),
  cancelRun: (id: number) => api.post<Run>(`/runs/${id}/cancel`),
  resumeRun: (id: number) => api.post<Run>(`/runs/${id}/resume`),
  listApprovalRequests: (status?: 'pending' | 'approved' | 'rejected') =>
    api.get<ApprovalRequest[]>('/approval-requests', status ? { status } : undefined),
  approveRequest: (id: number, note?: string) => api.post<ApprovalRequest>(`/approval-requests/${id}/approve`, { note }),
  rejectRequest: (id: number, note?: string) => api.post<ApprovalRequest>(`/approval-requests/${id}/reject`, { note }),
  listTeams: () => api.get<AgentTeam[]>('/agent-teams'),
  createTeam: (body: { name: string; supervisor_agent_id: number; handoff_strategy?: string; max_depth?: number }) =>
    api.post<AgentTeam>('/agent-teams', body),
  removeTeam: (id: number) => api.delete<{ success: boolean }>(`/agent-teams/${id}`),
  listTeamMembers: (teamId: number) => api.get<AgentTeamMember[]>(`/agent-teams/${teamId}/members`),
  addTeamMember: (teamId: number, body: { agent_id: number; role?: string }) =>
    api.post<AgentTeamMember>(`/agent-teams/${teamId}/members`, body),
  removeTeamMember: (teamId: number, agentId: number) =>
    api.delete<{ success: boolean }>(`/agent-teams/${teamId}/members/${agentId}`),
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

export const chatApi = {
  chat: (dialogId: number, body: ChatRequest) => api.post<ChatResponse>(`/dialogs/${dialogId}/rag/chat`, body),
  stream: (
    dialogId: number,
    body: ChatRequest,
    handlers: { onMessage: (msg: SSEMessage) => void; onError?: (err: Error) => void; signal?: AbortSignal },
  ) => streamPost(`/dialogs/${dialogId}/rag/chat/stream`, { body, ...handlers }),
};

export const dialogApi = {
  list: () => api.get<Dialog[]>('/dialogs'),
  get: (id: number) => api.get<Dialog>(`/dialogs/${id}`),
  create: (body: {
    name: string;
    description?: string;
    provider_id?: number;
    model?: string;
    system_prompt?: string;
    prologue?: string;
    kb_ids?: number[];
    top_k?: number;
    retrieval_mode?: string;
    history_round_limit?: number;
  }) => api.post<Dialog>('/dialogs', body),
  update: (id: number, body: Partial<Dialog>) => api.patch<Dialog>(`/dialogs/${id}`, body),
  remove: (id: number) => api.delete<{ success: boolean }>(`/dialogs/${id}`),
};

export const conversationApi = {
  list: (dialogId: number) => api.get<Conversation[]>(`/dialogs/${dialogId}/conversations`),
  get: (dialogId: number, id: number) => api.get<Conversation>(`/dialogs/${dialogId}/conversations/${id}`),
  listMessages: (dialogId: number, id: number) => api.get<Message[]>(`/dialogs/${dialogId}/conversations/${id}/messages`),
  remove: (dialogId: number, id: number) => api.delete<{ success: boolean }>(`/dialogs/${dialogId}/conversations/${id}`),
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
    list: () => api.get<Memory[]>('/memories'),
    create: (body: { memory_type: string; title?: string; content: string; importance?: number; source?: string }) =>
      api.post<Memory>('/memories', body),
    remove: (id: number) => api.delete<{ success: boolean }>(`/memories/${id}`),
  },
  tools: {
    list: () => api.get<ToolDefinition[]>('/tool-definitions'),
    create: (body: { name: string; tool_type?: string; description?: string; config_json: Record<string, unknown> }) =>
      api.post<ToolDefinition>('/tool-definitions', body),
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
    createMCPServer: (body: { name: string; transport: 'sse' | 'stdio'; endpoint_url?: string; command?: string; args_json?: string[]; env_json?: Record<string, string> }) =>
      api.post<MCPServer>('/mcp-servers', body),
    updateMCPServer: (id: number, body: Partial<MCPServer>) => api.patch<MCPServer>(`/mcp-servers/${id}`, body),
    removeMCPServer: (id: number) => api.delete<{ success: boolean }>(`/mcp-servers/${id}`),
    refreshMCPServer: (id: number) => api.post<{ server: MCPServer; tools: MCPToolCache[] }>(`/mcp-servers/${id}/refresh`),
    listMCPTools: (id: number) => api.get<MCPToolCache[]>(`/mcp-servers/${id}/tools`),
  },
};

export function publicRequest<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body, auth: false });
}
