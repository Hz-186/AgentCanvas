import { FormEvent, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowUpRight, Plus, Sparkles, Trash2 } from 'lucide-react';
import { Button, Card, EmptyState, Field, IconButton, Modal, TextArea, TextInput } from '@/components/ui';
import { workflowApi } from '@/api/resources';
import type { Workflow } from '@/types/api';
import { formatDate } from '@/utils/format';

export function WorkflowsPage() {
  const navigate = useNavigate();
  const [items, setItems] = useState<Workflow[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');

  const load = async () => {
    setLoading(true);
    setItems(await workflowApi.list().catch(() => []));
    setLoading(false);
  };

  useEffect(() => { void load(); }, []);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const workflow = await workflowApi.create({ name, description });
    setOpen(false);
    navigate(`/app/workflows/${workflow.id}/canvas`);
  };

  const remove = async (id: number) => {
    if (!window.confirm('删除这个工作流?')) return;
    await workflowApi.remove(id);
    await load();
  };

  return (
    <div className="page-grid">
      <div className="section-header">
        <div><h2>Agent Workflows</h2><p>用节点和连线编排可运行的 Agent 系统。第二版工作台会把画布作为一等公民,让构建和调试都更集中。</p></div>
        <Button tone="primary" onClick={() => setOpen(true)}><Plus size={18} /> 新建工作流</Button>
      </div>
      {loading ? <EmptyState title="正在载入工作流" /> : items.length === 0 ? <EmptyState title="还没有工作流" description="创建一个工作流后进入画布开始编排。" /> : (
        <div className="grid-cards">
          {items.map((item) => (
            <Card key={item.id} className="workflow-card">
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
                <div className="palette-icon"><Sparkles size={18} /></div>
                <IconButton aria-label="删除" onClick={() => void remove(item.id)}><Trash2 size={16} /></IconButton>
              </div>
              <h3 style={{ margin: '18px 0 8px', fontSize: 20 }}>{item.name}</h3>
              <p style={{ minHeight: 44, margin: 0, color: 'var(--text-muted)', lineHeight: 1.55 }}>{item.description || '没有描述'}</p>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 18 }}>
                <span className="badge">更新 {formatDate(item.updated_at)}</span>
                <Link className="button button-ghost button-small" to={`/app/workflows/${item.id}/canvas`}>打开 <ArrowUpRight size={15} /></Link>
              </div>
            </Card>
          ))}
        </div>
      )}
      <Modal open={open} title="创建工作流" onClose={() => setOpen(false)}>
        <form className="auth-form" onSubmit={create}>
          <Field label="名称"><TextInput value={name} onChange={(e) => setName(e.target.value)} required /></Field>
          <Field label="描述"><TextArea value={description} onChange={(e) => setDescription(e.target.value)} /></Field>
          <Button tone="primary" type="submit">创建并进入画布</Button>
        </form>
      </Modal>
    </div>
  );
}
