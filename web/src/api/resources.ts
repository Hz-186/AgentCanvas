import { api, request } from './client';
import { streamPost, type SSEMessage } from './sse';
import type {
  Agent,
  AgentDocument,
  ApiToken,
  ApiTokenCreated,
  AuditLog,
  ChatRequest,
  ChatResponse,
  Conversation,
  Dialog,
  CreateAgentRequest,
  CreateFlowVersionRequest,
  CreateProviderRequest,
  DocumentChunk,
  FlowVersion,
  IngestionJob,
  KnowledgeBase,
  Message,
  ModelProvider,
  ProviderCatalog,
  RetrievalResponse,
  Run,
  RunAgentRequest,
  RunEvent,
  RunStep,
  NodeLog,
  Memory,
  MemoryWriteLog,
  ToolDefinition,
  ToolInvocation,
  UpdateAgentRequest,
  UpdateProviderRequest,
  UploadDocumentResponse,
} from '../types/api';

export const agentApi = {
  list: () => api.get<Agent[]>('/agents'),
  get: (id: number) => api.get<Agent>(`/agents/${id}`),
  create: (body: CreateAgentRequest) => api.post<Agent>('/agents', body),
  update: (id: number, body: UpdateAgentRequest) => api.patch<Agent>(`/agents/${id}`, body),
  remove: (id: number) => api.delete<{ success: boolean }>(`/agents/${id}`),
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
  listNodeLogs: (id: number) => api.get<NodeLog[]>(`/runs/${id}/node-logs`),
  listRunSteps: (id: number) => api.get<RunStep[]>(`/runs/${id}/steps`),
  listMemoryWriteLogs: (id: number) => api.get<MemoryWriteLog[]>(`/runs/${id}/memory-write-logs`),
  listToolInvocations: (id: number) => api.get<ToolInvocation[]>(`/runs/${id}/tool-invocations`),
  cancelRun: (id: number) => api.post<Run>(`/runs/${id}/cancel`),
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
  },
};

export function publicRequest<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body, auth: false });
}
