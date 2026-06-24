import { FormEvent, useEffect, useRef, useState } from 'react';
import { ChevronLeft, Database, FileText, Plus, RefreshCw, Save, Search, Trash2, Upload } from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import { knowledgeApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Panel, Select, StatusBadge, Switch, TextArea, TextInput, Toast } from '../components/ui';
import type { AgentDocument, DocumentChunk, KnowledgeBase, ModelProvider, RetrievalResult } from '../types/api';
import { formatBytes, formatDate, friendlyErrorMessage } from '../utils/format';

const ACTIVE_DOCUMENT_STATUSES = new Set(['pending', 'parsing', 'chunking', 'indexing']);

export function KnowledgePage() {
  const navigate = useNavigate();
  const { id } = useParams();
  const routeId = id ? Number(id) : 0;
  const [items, setItems] = useState<KnowledgeBase[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [documents, setDocuments] = useState<AgentDocument[]>([]);
  const [chunks, setChunks] = useState<DocumentChunk[]>([]);
  const [searchResults, setSearchResults] = useState<RetrievalResult[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [createRetrievalMode, setCreateRetrievalMode] = useState('keyword');
  const [createEmbeddingProviderId, setCreateEmbeddingProviderId] = useState('');
  const [createEmbeddingModel, setCreateEmbeddingModel] = useState('');
  const [docToDelete, setDocToDelete] = useState<AgentDocument | null>(null);
  const [togglingId, setTogglingId] = useState<number | null>(null);
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
  // 记录已为哪个知识库填充过表单：保证 items 异步加载完成后填充一次，
  // 之后 items 因其它原因刷新时不再覆盖用户正在编辑的输入。
  const filledFormForRef = useRef<number>(0);

  async function load() {
    const [list, providerList] = await Promise.all([knowledgeApi.list(), settingsApi.providers.list()]);
    setItems(list);
    setProviders(providerList);
  }

  useEffect(() => {
    void load().catch((err) => setError(friendlyErrorMessage(err, '加载知识库失败')));
  }, []);

  useEffect(() => {
    if (!routeId) {
      setDocuments([]);
      setSearchResults([]);
      return;
    }
    let cancelled = false;
    knowledgeApi.listDocuments(routeId)
      .then((docs) => {
        if (!cancelled) setDocuments(docs);
      })
      .catch((err) => {
        if (!cancelled) setError(friendlyErrorMessage(err, '加载文档失败'));
      });
    return () => {
      cancelled = true;
    };
  }, [routeId]);

  useEffect(() => {
    if (!routeId || !documents.some((doc) => ACTIVE_DOCUMENT_STATUSES.has(doc.parser_status))) return;
    let cancelled = false;
    const timer = window.setInterval(() => {
      knowledgeApi.listDocuments(routeId)
        .then((docs) => {
          if (!cancelled) setDocuments(docs);
        })
        .catch((err) => {
          if (!cancelled) setError(friendlyErrorMessage(err, '刷新文档状态失败'));
        });
    }, 2500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [documents, routeId]);

  useEffect(() => {
    if (!routeId) {
      filledFormForRef.current = 0;
      return;
    }
    // 仅当切换到新知识库、且其数据已加载时才填充表单一次，避免覆盖用户正在编辑的输入。
    if (filledFormForRef.current === routeId) return;
    const selected = items.find((item) => item.id === routeId);
    if (!selected) return;
    filledFormForRef.current = routeId;
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
  }, [items, routeId]);

  async function createKB(event: FormEvent) {
    event.preventDefault();
    if (createRetrievalMode !== 'keyword' && !createEmbeddingProviderId) {
      setError('向量 / 混合检索需要选择 Embedding Provider');
      return;
    }
    try {
      setError('');
      const body: Parameters<typeof knowledgeApi.create>[0] = {
        name,
        description,
        retrieval_mode: createRetrievalMode,
        chunk_size: 800,
        chunk_overlap: 100,
      };
      if (createRetrievalMode !== 'keyword') {
        body.embedding_provider_id = Number(createEmbeddingProviderId);
        if (createEmbeddingModel.trim()) body.embedding_model = createEmbeddingModel.trim();
      }
      const kb = await knowledgeApi.create(body);
      setCreateOpen(false);
      setName('');
      setDescription('');
      setCreateRetrievalMode('keyword');
      setCreateEmbeddingProviderId('');
      setCreateEmbeddingModel('');
      setMessage('知识库已创建');
      await load();
      navigate(`/app/knowledge/${kb.id}`);
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建知识库失败'));
    }
  }

  async function upload(file: File | undefined) {
    if (!file || !routeId) return;
    try {
      setError('');
      const resp = await knowledgeApi.uploadDocument(routeId, file);
      setMessage(resp.job ? `文档已上传，任务 #${resp.job.id} 等待处理` : '文档已上传，等待处理');
      setDocuments(await knowledgeApi.listDocuments(routeId));
    } catch (err) {
      setError(friendlyErrorMessage(err, '上传文档失败'));
    }
  }

  async function showChunks(documentId: number) {
    try {
      setChunks(await knowledgeApi.listChunks(documentId));
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载文档片段失败'));
    }
  }

  async function toggleDocument(doc: AgentDocument, enabled: boolean) {
    setTogglingId(doc.id);
    try {
      setError('');
      const updated = await knowledgeApi.setDocumentEnabled(doc.id, enabled);
      setDocuments((current) => current.map((item) => (item.id === doc.id ? { ...item, enabled: updated.enabled } : item)));
      setMessage(enabled ? '文档已启用，将参与检索' : '文档已禁用，不再参与检索');
    } catch (err) {
      setError(friendlyErrorMessage(err, '更新文档状态失败'));
    } finally {
      setTogglingId(null);
    }
  }

  async function removeDocument(doc: AgentDocument) {
    try {
      setError('');
      await knowledgeApi.deleteDocument(doc.id);
      setDocToDelete(null);
      setMessage('文档已删除');
      if (routeId) setDocuments(await knowledgeApi.listDocuments(routeId));
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除文档失败'));
    }
  }

  async function saveRetrievalSettings(event: FormEvent) {
    event.preventDefault();
    if (!routeId) return;
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
    try {
      const updated = await knowledgeApi.update(routeId, body);
      setItems((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      setMessage(
        retrievalMode === 'keyword'
          ? '检索设置已保存'
          : '检索设置已保存，若调整了检索模式或 Embedding，请点击「重建索引」让已上传文档重新生成向量',
      );
    } catch (err) {
      setError(friendlyErrorMessage(err, '保存检索设置失败'));
    }
  }

  async function reindex() {
    if (!routeId) return;
    try {
      const resp = await knowledgeApi.reindex(routeId);
      setMessage(`已创建 ${resp.job_count} 个重建任务`);
      setDocuments(await knowledgeApi.listDocuments(routeId));
    } catch (err) {
      setError(friendlyErrorMessage(err, '重建索引失败'));
    }
  }

  async function testSearch(event: FormEvent) {
    event.preventDefault();
    if (!routeId || !searchQuery.trim()) return;
    const completedDocs = documents.filter((doc) => doc.parser_status === 'completed');
    if (completedDocs.length === 0 || completedDocs.every((doc) => doc.chunk_count === 0)) {
      setError('当前知识库还没有完成索引的文档，请等待文档处理完成后再检索。');
      return;
    }
    try {
      setError('');
      const resp = await knowledgeApi.search(routeId, { query: searchQuery, top_k: 5, mode: searchMode });
      setSearchResults(resp.results);
      setMessage(`检索完成 · ${resp.latency_ms}ms`);
    } catch (err) {
      setError(friendlyErrorMessage(err, '检索测试失败'));
    }
  }

  const selected = items.find((item) => item.id === routeId);
  const isDetail = Boolean(routeId);
  const hasActiveDocuments = documents.some((doc) => ACTIVE_DOCUMENT_STATUSES.has(doc.parser_status));

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>知识库</h1>
          <p>{isDetail ? '管理文档、切分与检索策略。' : '为 Agent 和 RAG 对话准备可检索的内容集合。'}</p>
        </div>
        {isDetail ? (
          <Button onClick={() => navigate('/app/knowledge')}>
            <ChevronLeft size={17} />
            返回列表
          </Button>
        ) : (
          <Button tone="primary" onClick={() => setCreateOpen(true)}>
            <Plus size={17} />
            新建知识库
          </Button>
        )}
      </div>
      {error ? <p className="error-text">{error}</p> : null}

      {!isDetail && items.length === 0 ? (
        <EmptyState icon={<Database size={24} />} title="还没有知识库" description="先创建一个知识库，再上传 txt 或 md 文档。" action={<Button tone="primary" onClick={() => setCreateOpen(true)}>新建知识库</Button>} />
      ) : null}

      {!isDetail && items.length > 0 ? (
        <div className="grid">
          {items.map((kb) => (
            <article className="card" key={kb.id}>
              <div className="card-title">
                <h3 className="truncate">{kb.name}</h3>
                <StatusBadge tone="good">Active</StatusBadge>
              </div>
              <p className="muted clamp-2">{kb.description || '暂无描述'}</p>
              <div className="meta-row">
                <span>{kb.document_count} documents</span>
                <span>{kb.chunk_count} chunks</span>
                <span>更新 {formatDate(kb.updated_at)}</span>
              </div>
              <div className="row-wrap">
                <Button tone="primary" onClick={() => navigate(`/app/knowledge/${kb.id}`)}>
                  打开知识库
                </Button>
              </div>
            </article>
          ))}
        </div>
      ) : null}

      {isDetail ? (
        <div className="knowledge-layout">
          <div className="stack">
            <Panel
              title={selected?.name ?? '知识库详情'}
              eyebrow={hasActiveDocuments ? '文档处理中' : '文档'}
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
                        <th className="col-switch">启用</th>
                        <th>文档</th>
                        <th>状态</th>
                        <th>大小</th>
                        <th>片段数</th>
                        <th>创建时间</th>
                        <th className="col-actions" aria-label="操作" />
                      </tr>
                    </thead>
                    <tbody>
                      {documents.map((doc) => (
                        <tr key={doc.id} className={doc.enabled ? '' : 'row-disabled'}>
                          <td className="col-switch">
                            <Switch
                              checked={doc.enabled}
                              disabled={togglingId === doc.id}
                              onChange={(value) => void toggleDocument(doc, value)}
                              label={doc.enabled ? '点击禁用该文档（不参与检索）' : '点击启用该文档（参与检索）'}
                            />
                          </td>
                          <td><button type="button" onClick={() => void showChunks(doc.id)}>{doc.name}</button></td>
                          <td><StatusBadge tone={doc.parser_status === 'completed' ? 'good' : doc.parser_status === 'failed' ? 'bad' : 'warn'}>{doc.parser_status}</StatusBadge></td>
                          <td>{formatBytes(doc.file_size)}</td>
                          <td>{doc.chunk_count}</td>
                          <td>
                            <div>{formatDate(doc.created_at)}</div>
                            {doc.parser_error ? <div className="error-text">{doc.parser_error}</div> : null}
                          </td>
                          <td className="col-actions">
                            <IconButton label="删除文档" className="icon-btn-danger" onClick={() => setDocToDelete(doc)}>
                              <Trash2 size={16} />
                            </IconButton>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Panel>

            <Panel
              title="检索设置"
              eyebrow="策略"
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

            <Panel title="检索测试" eyebrow="测试">
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
              <Panel title="文档片段" eyebrow="切分结果">
                {chunks.map((chunk) => (
                  <pre className="code-box" key={chunk.id}>{chunk.content}</pre>
                ))}
              </Panel>
            ) : null}
          </div>
        </div>
      ) : null}

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
          <Field label="检索模式" hint="向量 / 混合检索可对语义进行召回，需配置 Embedding Provider">
            <Select value={createRetrievalMode} onChange={(event) => setCreateRetrievalMode(event.target.value)}>
              <option value="keyword">Keyword（关键词）</option>
              <option value="vector">Vector（向量）</option>
              <option value="hybrid">Hybrid（混合）</option>
            </Select>
          </Field>
          {createRetrievalMode !== 'keyword' ? (
            <>
              <Field label="Embedding Provider" hint="向量 / 混合检索必填">
                <Select value={createEmbeddingProviderId} onChange={(event) => setCreateEmbeddingProviderId(event.target.value)}>
                  <option value="">请选择</option>
                  {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                </Select>
              </Field>
              <Field label="Embedding 模型">
                <TextInput value={createEmbeddingModel} onChange={(event) => setCreateEmbeddingModel(event.target.value)} placeholder="留空使用 Provider 默认值" />
              </Field>
            </>
          ) : null}
          <Field label="默认切分">
            <Select value="fixed" disabled>
              <option value="fixed">Fixed token · 800 / 100</option>
            </Select>
          </Field>
        </form>
      </Modal>
      <Modal
        open={docToDelete !== null}
        title="删除文档"
        onClose={() => setDocToDelete(null)}
        footer={
          <>
            <Button type="button" onClick={() => setDocToDelete(null)}>取消</Button>
            <Button type="button" tone="danger" onClick={() => docToDelete && void removeDocument(docToDelete)}>删除</Button>
          </>
        }
      >
        <p>确定删除文档「{docToDelete?.name}」吗？该操作会同时移除其切片与索引，且不可恢复。</p>
      </Modal>
      <Toast message={message} tone="good" />
    </div>
  );
}
