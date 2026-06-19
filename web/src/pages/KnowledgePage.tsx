import { FormEvent, useEffect, useState } from 'react';
import { Database, FileText, Plus, Search, Upload } from 'lucide-react';
import { knowledgeApi } from '../api/resources';
import { Button, EmptyState, Field, Modal, Panel, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { AgentDocument, DocumentChunk, KnowledgeBase, RetrievalResult } from '../types/api';
import { formatBytes, formatDate } from '../utils/format';

export function KnowledgePage() {
  const [items, setItems] = useState<KnowledgeBase[]>([]);
  const [selectedId, setSelectedId] = useState<number>(0);
  const [documents, setDocuments] = useState<AgentDocument[]>([]);
  const [chunks, setChunks] = useState<DocumentChunk[]>([]);
  const [results, setResults] = useState<RetrievalResult[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [query, setQuery] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    const list = await knowledgeApi.list();
    setItems(list);
    setSelectedId((current) => current || list[0]?.id || 0);
  }

  useEffect(() => {
    void load().catch((err) => setError(err instanceof Error ? err.message : '加载知识库失败'));
  }, []);

  useEffect(() => {
    if (!selectedId) {
      setDocuments([]);
      return;
    }
    void knowledgeApi.listDocuments(selectedId).then(setDocuments).catch((err) => setError(err instanceof Error ? err.message : '加载文档失败'));
  }, [selectedId]);

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

  async function searchKB(event: FormEvent) {
    event.preventDefault();
    if (!selectedId || !query.trim()) return;
    const resp = await knowledgeApi.search(selectedId, { query, top_k: 8, mode: 'keyword' });
    setResults(resp.results);
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

            <Panel title="检索测试" eyebrow="Keyword Search">
              <form className="chat-composer" onSubmit={(event) => void searchKB(event)}>
                <TextInput value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入检索问题" />
                <Button tone="primary">
                  <Search size={16} />
                  检索
                </Button>
              </form>
              {results.map((item) => (
                <article className="card" key={item.chunk_id}>
                  <div className="card-title">
                    <h3 className="truncate">{item.document_name}</h3>
                    <StatusBadge tone="info">{item.score.toFixed(3)}</StatusBadge>
                  </div>
                  <p className="muted">{item.highlight || item.content}</p>
                </article>
              ))}
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
