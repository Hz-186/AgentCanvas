import { FormEvent, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Bot, Plus, Trash2 } from 'lucide-react';
import { agentApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { Agent } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

export function AgentsPage() {
  const navigate = useNavigate();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    setLoading(true);
    try {
      setAgents(await agentApi.list());
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载 Agent 失败'));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function createAgent(event: FormEvent) {
    event.preventDefault();
    const agent = await agentApi.create({ name, description });
    setOpen(false);
    setName('');
    setDescription('');
    setMessage('Agent 已创建');
    await load();
    navigate(`/app/agents/${agent.id}/canvas`);
  }

  async function removeAgent(id: number) {
    if (!window.confirm('确认删除这个 Agent？')) return;
    await agentApi.remove(id);
    setMessage('Agent 已删除');
    await load();
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Agent 工作区</h1>
          <p>每个 Agent 都是一块独立画布，可保存、发布并直接流式调试。</p>
        </div>
        <Button tone="primary" onClick={() => setOpen(true)}>
          <Plus size={17} />
          新建 Agent
        </Button>
      </div>

      {error ? <p className="error-text">{error}</p> : null}
      {loading ? <p className="muted">正在加载 Agent...</p> : null}

      {!loading && agents.length === 0 ? (
        <EmptyState
          icon={<Bot size={24} />}
          title="还没有 Agent"
          description="创建一个 Agent 后即可进入可视化画布。"
          action={<Button tone="primary" onClick={() => setOpen(true)}>新建 Agent</Button>}
        />
      ) : (
        <div className="grid">
          {agents.map((agent) => (
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
                <Button tone="primary" onClick={() => navigate(`/app/agents/${agent.id}/canvas`)}>
                  打开画布
                </Button>
                <IconButton label="删除 Agent" onClick={() => void removeAgent(agent.id)}>
                  <Trash2 size={16} />
                </IconButton>
              </div>
            </article>
          ))}
        </div>
      )}

      <Modal
        open={open}
        title="新建 Agent"
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
            <TextInput value={name} onChange={(event) => setName(event.target.value)} required placeholder="Customer Support Agent" />
          </Field>
          <Field label="描述">
            <TextArea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="这个 Agent 负责回答知识库问题并生成回复。" />
          </Field>
        </form>
      </Modal>

      <Toast message={message} tone="good" />
    </div>
  );
}
