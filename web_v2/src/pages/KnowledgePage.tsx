import { FormEvent, useEffect, useState } from 'react';
import { Database, FileUp, Plus, RefreshCw } from 'lucide-react';
import { Button, Card, EmptyState, Field, Modal, TextArea, TextInput } from '@/components/ui';
import { knowledgeApi } from '@/api/resources';
import type { AgentDocument, KnowledgeBase } from '@/types/api';
import { formatBytes, formatDate } from '@/utils/format';

export function KnowledgePage() {
  const [items, setItems] = useState<KnowledgeBase[]>([]);
  const [selected, setSelected] = useState<KnowledgeBase | null>(null);
  const [docs, setDocs] = useState<AgentDocument[]>([]);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');

  const load = async () => {
    const list = await knowledgeApi.list().catch(() => []);
    setItems(list);
    setSelected((current) => current ?? list[0] ?? null);
  };

  useEffect(() => { void load(); }, []);
  useEffect(() => { if (selected) void knowledgeApi.listDocuments(selected.id).then(setDocs).catch(() => setDocs([])); }, [selected]);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    await knowledgeApi.create({ name, description, retrieval_mode: 'hybrid', chunk_size: 900, chunk_overlap: 120 });
    setOpen(false);
    setName('');
    setDescription('');
    await load();
  };

  const upload = async (file: File) => {
    if (!selected) return;
    await knowledgeApi.uploadDocument(selected.id, file);
    setDocs(await knowledgeApi.listDocuments(selected.id));
  };

  return (
    <div className="page-grid">
      <div className="section-header">
        <div><h2>Knowledge Studio</h2><p>用克制的资料工作台管理知识库、文档解析和索引状态。画布节点可以直接引用这里的知识源。</p></div>
        <Button tone="primary" onClick={() => setOpen(true)}><Plus size={18} /> 新建知识库</Button>
      </div>
      <div className="resource-layout">
        <Card className="resource-list glass-strong">
          {items.length === 0 ? <EmptyState title="暂无知识库" /> : items.map((item) => (
            <button className={`resource-row${selected?.id === item.id ? ' active' : ''}`} key={item.id} onClick={() => setSelected(item)}>
              <span className="palette-icon"><Database size={17} /></span>
              <span><strong>{item.name}</strong><small>{item.document_count} docs · {item.chunk_count} chunks</small></span>
            </button>
          ))}
        </Card>
        <Card className="resource-detail glass-strong">
          {selected ? (
            <>
              <div className="panel-header" style={{ padding: 0, borderBottom: 0 }}><h3>{selected.name}</h3><Button size="small" onClick={() => void knowledgeApi.reindex(selected.id)}><RefreshCw size={15} /> 重建索引</Button></div>
              <p style={{ color: 'var(--text-muted)', lineHeight: 1.6 }}>{selected.description || '没有描述'}</p>
              <label className="upload-zone"><FileUp size={22} /><span>拖入或选择文档上传</span><input type="file" onChange={(e) => { const file = e.currentTarget.files?.[0]; if (file) void upload(file); }} /></label>
              <div className="table-list">
                {docs.map((doc) => <div className="table-row" key={doc.id}><strong>{doc.name}</strong><span>{doc.parser_status}</span><span>{formatBytes(doc.file_size)}</span><span>{formatDate(doc.updated_at)}</span></div>)}
              </div>
            </>
          ) : <EmptyState title="选择一个知识库" />}
        </Card>
      </div>
      <Modal open={open} title="新建知识库" onClose={() => setOpen(false)}>
        <form className="auth-form" onSubmit={create}><Field label="名称"><TextInput value={name} onChange={(e) => setName(e.target.value)} required /></Field><Field label="描述"><TextArea value={description} onChange={(e) => setDescription(e.target.value)} /></Field><Button tone="primary">创建</Button></form>
      </Modal>
    </div>
  );
}
