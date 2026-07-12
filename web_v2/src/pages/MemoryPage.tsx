import { FormEvent, useEffect, useState } from 'react';
import { Brain, Plus, Trash2 } from 'lucide-react';
import { Button, Card, EmptyState, Field, IconButton, Modal, Select, TextArea, TextInput } from '@/components/ui';
import { settingsApi } from '@/api/resources';
import type { Memory } from '@/types/api';
import { formatDate } from '@/utils/format';

export function MemoryPage() {
  const [items, setItems] = useState<Memory[]>([]);
  const [open, setOpen] = useState(false);
  const [memoryType, setMemoryType] = useState('summary');
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const load = () => settingsApi.memories.list().then(setItems).catch(() => setItems([]));
  useEffect(() => { void load(); }, []);
  const create = async (event: FormEvent) => { event.preventDefault(); await settingsApi.memories.create({ memory_type: memoryType, title, content, importance: .6 }); setOpen(false); setTitle(''); setContent(''); void load(); };
  const remove = async (id: number) => { await settingsApi.memories.remove(id); void load(); };
  return <div className="page-grid"><div className="section-header"><div><h2>Memory Vault</h2><p>把长期记忆变成可审计、可维护的资产，而不是埋在运行日志里的黑盒。</p></div><Button tone="primary" onClick={() => setOpen(true)}><Plus size={18} /> 添加记忆</Button></div>{items.length === 0 ? <Card><EmptyState title="暂无记忆" /></Card> : <div className="grid-cards">{items.map((item) => <Card key={item.id}><div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}><span className="palette-icon"><Brain size={17} /></span><IconButton onClick={() => void remove(item.id)}><Trash2 size={15} /></IconButton></div><h3>{item.title || item.memory_type}</h3><p style={{ color: 'var(--text-muted)', lineHeight: 1.6 }}>{item.content}</p><span className="badge">{item.memory_type} · {formatDate(item.updated_at)}</span></Card>)}</div>}<Modal open={open} title="添加记忆" onClose={() => setOpen(false)}><form className="auth-form" onSubmit={create}><Field label="类型"><Select value={memoryType} onChange={(e) => setMemoryType(e.target.value)}><option value="profile">profile</option><option value="summary">summary</option><option value="episodic">episodic</option><option value="task_memory">task_memory</option></Select></Field><Field label="标题"><TextInput value={title} onChange={(e) => setTitle(e.target.value)} /></Field><Field label="内容"><TextArea value={content} onChange={(e) => setContent(e.target.value)} required /></Field><Button tone="primary">保存</Button></form></Modal></div>;
}
