import { FormEvent, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Bot, Plus, Trash2 } from 'lucide-react';
import { workflowApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { Workflow } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

export function WorkflowsPage() {
  const navigate = useNavigate();
  const [workflows, setAgents] = useState<Workflow[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    setLoading(true);
    try {
      setAgents(await workflowApi.list());
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载 Workflow 失败'));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function createAgent(event: FormEvent) {
    event.preventDefault();
    try {
      const agent = await workflowApi.create({ name, description });
      setOpen(false);
      setName('');
      setDescription('');
      setMessage('Workflow 已创建');
      await load();
      navigate(`/app/workflows/${agent.id}/canvas`);
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Workflow 失败'));
    }
  }

  async function removeAgent(id: number) {
    if (!window.confirm('确认删除这个 Workflow？')) return;
    try {
      await workflowApi.remove(id);
      setMessage('Workflow 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 Workflow 失败'));
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Workflow 工作区</h1>
          <p>每个 Workflow 都是一块独立画布，可保存、发布并直接流式调试。</p>
        </div>
        <Button tone="primary" onClick={() => setOpen(true)}>
          <Plus size={17} />
          新建 Workflow
        </Button>
      </div>

      {error ? <p className="error-text">{error}</p> : null}
      {loading ? <p className="muted">正在加载 Workflow...</p> : null}

      {!loading && workflows.length === 0 ? (
        <EmptyState
          icon={<Bot size={24} />}
          title="还没有 Workflow"
          description="创建一个 Workflow 后即可进入可视化画布。"
          action={<Button tone="primary" onClick={() => setOpen(true)}>新建 Workflow</Button>}
        />
      ) : (
        <div className="grid">
          {workflows.map((agent) => (
            <article className="card" key={agent.id}>
              <div className="card-title">
                <h3 className="truncate">{agent.name}</h3>
                <StatusBadge tone={agent.status === 1 ? 'good' : 'neutral'}>{agent.status === 1 ? 'Active' : 'Inactive'}</StatusBadge>
              </div>
              <p className="muted clamp-2">{agent.description || '暂无描述'}</p>
              <div className="meta-row">
                <span>版本 {agent.current_version_id ?? 'draft'}</span>
                <span>更新 {formatDate(agent.updated_at)}</span>
              </div>
              <div className="row-wrap">
                <Button tone="primary" onClick={() => navigate(`/app/workflows/${agent.id}/canvas`)}>
                  打开画布
                </Button>
                <IconButton label="删除 Workflow" onClick={() => void removeAgent(agent.id)}>
                  <Trash2 size={16} />
                </IconButton>
              </div>
            </article>
          ))}
        </div>
      )}

      <Modal
        open={open}
        title="新建 Workflow"
        onClose={() => setOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setOpen(false)}>取消</Button>
            <Button type="submit" form="create-agent-form" tone="primary">创建并进入画布</Button>
          </>
        }
      >
        <form id="create-agent-form" className="form-stack" onSubmit={(event) => void createAgent(event)}>
          <Field label="名称">
            <TextInput value={name} onChange={(event) => setName(event.target.value)} required placeholder="Customer Support Workflow" />
          </Field>
          <Field label="描述">
            <TextArea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="这个 Workflow 负责回答知识库问题并生成回复。" />
          </Field>
        </form>
      </Modal>

      <Toast message={message} tone="good" />
    </div>
  );
}
