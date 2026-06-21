import { FormEvent, useEffect, useState } from 'react';
import { Database, FileText, Plus, RefreshCw, Save, Search, Upload } from 'lucide-react';
import { knowledgeApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, Modal, Panel, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { AgentDocument, DocumentChunk, KnowledgeBase, ModelProvider, RetrievalResult } from '../types/api';
import { formatBytes, formatDate } from '../utils/format';

export function KnowledgePage() {
  const [items, setItems] = useState<KnowledgeBase[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [selectedId, setSelectedId] = useState<number>(0);
  const [documents, setDocuments] = useState<AgentDocument[]>([]);
  const [chunks, setChunks] = useState<DocumentChunk[]>([]);
  const [searchResults, setSearchResults] = useState<RetrievalResult[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [retrievalMode, setRetrievalMode] = useState('keyword');
  const [embeddingProviderId, setEmbeddingProviderId] = useState('');
  const [embeddingModel, setEmbeddingModel] = useState('');
  const [embeddingDimensions, setEmbeddingDimensions] = useState(0);
  const [hybridWeight, setHybridWeight] = useState(0.5);
  const [rerankEnabled, setRerankEnabled] = useState(false);
  const [rerankProviderId, setRerankProviderId] = useState('');
  const [rerankModel, setRerankModel] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [searchMode, setSearchMode] = useState('keyword');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    const [list, providerList] = await Promise.all([knowledgeApi.list(), settingsApi.providers.list()]);
    setItems(list);
    setProviders(providerList);
    setSelectedId((current) => current || list[0]?.id || 0);
  }

  useEffect(() => {
    void load().catch((err) => setError(err instanceof Error ? err.message : '加载知识库失败'));
  }, []);

  useEffect(() => {
    if (!selectedId) {
      setDocuments([]);
      setSearchResults([]);
      return;
    }
    void knowledgeApi.listDocuments(selectedId).then(setDocuments).catch((err) => setError(err instanceof Error ? err.message : '加载文档失败'));
  }, [selectedId]);

  useEffect(() => {
    const selected = items.find((item) => item.id === selectedId);
    if (!selected) return;
    setRetrievalMode(selected.retrieval_mode || 'keyword');
    setSearchMode(selected.retrieval_mode || 'keyword');
    setEmbeddingProviderId(selected.embedding_provider_id ? String(selected.embedding_provider_id) : '');
    setEmbeddingModel(selected.embedding_model || '');
    setEmbeddingDimensions(selected.embedding_dimensions || 0);
    setHybridWeight(selected.hybrid_weight || 0.5);
    setRerankEnabled(Boolean(selected.rerank_enabled));
    setRerankProviderId(selected.rerank_provider_id ? String(selected.rerank_provider_id) : '');
    setRerankModel(selected.rerank_model || '');
    setSearchResults([]);
  }, [items, selectedId]);

  async function createKB(event: FormEvent) {
    event.preventDefault();
    const kb = await knowledgeApi.create({ name, description, chunk_size: 800, chunk_overlap: 100 });
    setCreateOpen(false);
    setName('');
    setDescription('');
    setMessage('知识库已创建');
    await load();
    setSelectedId(kb.id);
  }

  async function upload(file: File | undefined) {
    if (!file || !selectedId) return;
    await knowledgeApi.uploadDocument(selectedId, file);
    setMessage('文档已上传，等待 ingestion');
    setDocuments(await knowledgeApi.listDocuments(selectedId));
  }

  async function showChunks(documentId: number) {
    setChunks(await knowledgeApi.listChunks(documentId));
  }

  async function saveRetrievalSettings(event: FormEvent) {
    event.preventDefault();
    if (!selectedId) return;
    const body: Parameters<typeof knowledgeApi.update>[1] = {
      retrieval_mode: retrievalMode,
      embedding_model: embeddingModel,
      embedding_dimensions: embeddingDimensions,
      hybrid_weight: hybridWeight,
      rerank_enabled: rerankEnabled,
      rerank_model: rerankModel,
    };
    if (embeddingProviderId) body.embedding_provider_id = Number(embeddingProviderId);
    if (rerankProviderId) body.rerank_provider_id = Number(rerankProviderId);
    const updated = await knowledgeApi.update(selectedId, body);
    setItems((current) => current.map((item) => (item.id === updated.id ? updated : item)));
    setMessage('检索设置已保存');
  }

  async function reindex() {
    if (!selectedId) return;
    const resp = await knowledgeApi.reindex(selectedId);
    setMessage(`已创建 ${resp.job_count} 个重建任务`);
    setDocuments(await knowledgeApi.listDocuments(selectedId));
  }

  async function testSearch(event: FormEvent) {
    event.preventDefault();
    if (!selectedId || !searchQuery.trim()) return;
    const resp = await knowledgeApi.search(selectedId, { query: searchQuery, top_k: 5, mode: searchMode });
    setSearchResults(resp.results);
    setMessage(`检索完成 · ${resp.latency_ms}ms`);
  }

  const selected = items.find((item) => item.id === selectedId);

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>知识库</h1>
          <p>上传 Markdown 或文本文件，检索结果可直接被 Agent Flow 与 RAG Chat 使用。</p>
        </div>
        <Button tone="primary" onClick={() => setCreateOpen(true)}>
          <Plus size={17} />
          新建知识库
        </Button>
      </div>
      {error ? <p className="error-text">{error}</p> : null}

      {items.length === 0 ? (
        <EmptyState icon={<Database size={24} />} title="还没有知识库" description="先创建一个知识库，再上传 txt 或 md 文档。" action={<Button tone="primary" onClick={() => setCreateOpen(true)}>新建知识库</Button>} />
      ) : (
        <div className="split-layout">
          <Panel title="知识库列表" eyebrow="Knowledge">
            <div className="stack">
              {items.map((kb) => (
                <button className="card" key={kb.id} type="button" onClick={() => setSelectedId(kb.id)} style={{ borderColor: kb.id === selectedId ? 'var(--accent)' : undefined }}>
                  <div className="card-title">
                    <h3 className="truncate">{kb.name}</h3>
                    <StatusBadge tone="good">Active</StatusBadge>
                  </div>
                  <p className="muted clamp-2">{kb.description || '暂无描述'}</p>
                  <div className="meta-row">
                    <span>{kb.document_count} documents</span>
                    <span>{kb.chunk_count} chunks</span>
                  </div>
                </button>
              ))}
            </div>
          </Panel>

          <div className="stack">
            <Panel
              title={selected?.name ?? '知识库详情'}
              eyebrow="Documents"
              action={
                <label className="btn btn-secondary">
                  <Upload size={16} />
                  上传文档
                  <input className="sr-only" type="file" accept=".txt,.md,text/plain,text/markdown" onChange={(event) => void upload(event.target.files?.[0])} />
                </label>
              }
            >
              {documents.length === 0 ? (
                <EmptyState icon={<FileText size={22} />} title="暂无文档" description="当前后端支持 .txt 与 .md 文件。" />
              ) : (
                <div className="table-wrap">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>文档</th>
                        <th>状态</th>
                        <th>大小</th>
                        <th>Chunks</th>
                        <th>创建时间</th>
                      </tr>
                    </thead>
                    <tbody>
                      {documents.map((doc) => (
                        <tr key={doc.id}>
                          <td><button type="button" onClick={() => void showChunks(doc.id)}>{doc.name}</button></td>
                          <td><StatusBadge tone={doc.parser_status === 'completed' ? 'good' : doc.parser_status === 'failed' ? 'bad' : 'warn'}>{doc.parser_status}</StatusBadge></td>
                          <td>{formatBytes(doc.file_size)}</td>
                          <td>{doc.chunk_count}</td>
                          <td>{formatDate(doc.created_at)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Panel>

            <Panel
              title="检索设置"
              eyebrow="Phase 7"
              action={
                <Button type="button" onClick={() => void reindex()}>
                  <RefreshCw size={16} />
                  重建索引
                </Button>
              }
            >
              <form className="form-stack" onSubmit={(event) => void saveRetrievalSettings(event)}>
                <div className="dense-grid">
                  <Field label="默认模式">
                    <Select value={retrievalMode} onChange={(event) => setRetrievalMode(event.target.value)}>
                      <option value="keyword">Keyword</option>
                      <option value="vector">Vector</option>
                      <option value="hybrid">Hybrid</option>
                    </Select>
                  </Field>
                  <Field label="Hybrid 权重">
                    <TextInput type="number" min={0} max={1} step={0.05} value={hybridWeight} onChange={(event) => setHybridWeight(Number(event.target.value))} />
                  </Field>
                </div>
                <div className="dense-grid">
                  <Field label="Embedding Provider">
                    <Select value={embeddingProviderId} onChange={(event) => setEmbeddingProviderId(event.target.value)}>
                      <option value="">未选择</option>
                      {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="Embedding 模型">
                    <TextInput value={embeddingModel} onChange={(event) => setEmbeddingModel(event.target.value)} placeholder="留空使用 Provider 默认值" />
                  </Field>
                </div>
                <Field label="Embedding 维度">
                  <TextInput type="number" min={0} value={embeddingDimensions} onChange={(event) => setEmbeddingDimensions(Number(event.target.value))} />
                </Field>
                <div className="dense-grid">
                  <Field label="Rerank">
                    <Select value={rerankEnabled ? 'on' : 'off'} onChange={(event) => setRerankEnabled(event.target.value === 'on')}>
                      <option value="off">关闭</option>
                      <option value="on">开启</option>
                    </Select>
                  </Field>
                  <Field label="Rerank Provider">
                    <Select value={rerankProviderId} onChange={(event) => setRerankProviderId(event.target.value)}>
                      <option value="">未选择</option>
                      {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                    </Select>
                  </Field>
                </div>
                <Field label="Rerank 模型">
                  <TextInput value={rerankModel} onChange={(event) => setRerankModel(event.target.value)} placeholder="留空使用 Provider 默认 Chat 模型" />
                </Field>
                <div className="row-wrap">
                  <Button tone="primary">
                    <Save size={16} />
                    保存设置
                  </Button>
                </div>
              </form>
            </Panel>

            <Panel title="检索测试" eyebrow="Search">
              <form className="form-stack" onSubmit={(event) => void testSearch(event)}>
                <div className="dense-grid">
                  <Field label="查询">
                    <TextInput value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} />
                  </Field>
                  <Field label="模式">
                    <Select value={searchMode} onChange={(event) => setSearchMode(event.target.value)}>
                      <option value="keyword">Keyword</option>
                      <option value="vector">Vector</option>
                      <option value="hybrid">Hybrid</option>
                    </Select>
                  </Field>
                </div>
                <div className="row-wrap">
                  <Button tone="primary">
                    <Search size={16} />
                    检索
                  </Button>
                </div>
              </form>
              {searchResults.length > 0 ? (
                <div className="stack">
                  {searchResults.map((result) => (
                    <article className="card" key={result.chunk_id}>
                      <div className="card-title">
                        <h3 className="truncate">{result.document_name}</h3>
                        <StatusBadge tone="info">{result.final_score ? result.final_score.toFixed(3) : result.score.toFixed(3)}</StatusBadge>
                      </div>
                      <p className="muted clamp-2">{result.content}</p>
                      <div className="meta-row">
                        <span>keyword {Number(result.keyword_score || 0).toFixed(2)}</span>
                        <span>vector {Number(result.vector_score || 0).toFixed(2)}</span>
                      </div>
                    </article>
                  ))}
                </div>
              ) : null}
            </Panel>

            {chunks.length > 0 ? (
              <Panel title="文档 Chunks" eyebrow="Chunks">
                {chunks.map((chunk) => (
                  <pre className="code-box" key={chunk.id}>{chunk.content}</pre>
                ))}
              </Panel>
            ) : null}
          </div>
        </div>
      )}

      <Modal
        open={createOpen}
        title="新建知识库"
        onClose={() => setCreateOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setCreateOpen(false)}>取消</Button>
            <Button form="create-kb-form" tone="primary">创建</Button>
          </>
        }
      >
        <form id="create-kb-form" className="form-stack" onSubmit={(event) => void createKB(event)}>
          <Field label="名称">
            <TextInput value={name} onChange={(event) => setName(event.target.value)} required />
          </Field>
          <Field label="描述">
            <TextArea value={description} onChange={(event) => setDescription(event.target.value)} />
          </Field>
          <Field label="默认切分">
            <Select value="fixed" disabled>
              <option value="fixed">Fixed token · 800 / 100</option>
            </Select>
          </Field>
        </form>
      </Modal>
      <Toast message={message} tone="good" />
    </div>
  );
}
