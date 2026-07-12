import { FormEvent, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowUpRight, Bot, GitBranch, Plus, Trash2 } from 'lucide-react';
import { resourceSummaryApi, workflowApi } from '../api/resources';
import { EditorialHeader } from '../components/editorial';
import { Button, EmptyState, Field, IconButton, Modal, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { ResourceSummary } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

export function WorkflowsPage() {
  const navigate = useNavigate();
  const [workflows, setAgents] = useState<ResourceSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    setLoading(true);
    try {
      setAgents((await resourceSummaryApi.list('workflows', { limit: 50 })).items);
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, 'Unable to load workflows.'));
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
      setMessage('Workflow created');
      navigate(`/app/workflows/${agent.id}/canvas`);
    } catch (err) {
      setError(friendlyErrorMessage(err, 'Unable to create workflow.'));
    }
  }

  async function removeAgent(id: number) {
    if (!window.confirm('Delete this workflow?')) return;
    try {
      await workflowApi.remove(id);
      setMessage('Workflow deleted');
      setAgents((current) => current.filter((item) => item.id !== id));
    } catch (err) {
      setError(friendlyErrorMessage(err, 'Unable to delete workflow.'));
    }
  }

  return (
    <div className="page workflow-library-page">
      <EditorialHeader word="Workflow" script="Library" kicker="WORKFLOW STUDIO / 01" description="工作流 · 在同一处设计、发布并追踪每一张独立画布。" action={<Button tone="primary" onClick={() => setOpen(true)}>
          <Plus size={17} />
          New Workflow
        </Button>} />

      {error ? <p className="error-text">{error}</p> : null}
      {loading ? <p className="muted workflow-loading">Loading workflows...</p> : null}

      {!loading && workflows.length === 0 ? (
        <EmptyState
          icon={<Bot size={24} />}
          title="No workflows yet"
          description="Create your first workflow and enter the visual canvas."
          action={<Button tone="primary" onClick={() => setOpen(true)}>New Workflow</Button>}
        />
      ) : (
        <div className="workflow-library-list">
          {workflows.map((agent) => (
            <article className="workflow-library-item" key={agent.id}>
              <div className="workflow-miniature" aria-hidden="true">
                <span><Bot size={16} /></span>
                <i />
                <span><GitBranch size={16} /></span>
                <i />
                <span className="workflow-miniature-end"><ArrowUpRight size={16} /></span>
              </div>
              <div className="workflow-library-copy">
                <div className="card-title">
                  <h3 className="truncate">{agent.name}</h3>
                  <StatusBadge tone={agent.status === 1 ? 'good' : 'neutral'}>{agent.status === 1 ? 'Active' : 'Draft'}</StatusBadge>
                </div>
                <p className="muted clamp-2">{agent.description || 'An independent canvas ready for your next idea.'}</p>
                <div className="meta-row">
                  <span>VERSION {agent.current_version_id ?? 'DRAFT'}</span>
                  <span>UPDATED {formatDate(agent.updated_at)}</span>
                </div>
              </div>
              <div className="workflow-library-actions">
                <Button tone="primary" onClick={() => navigate(`/app/workflows/${agent.id}/canvas`)}>
                  Open Canvas
                  <ArrowUpRight size={16} />
                </Button>
                <IconButton label="Delete Workflow" onClick={() => void removeAgent(agent.id)}>
                  <Trash2 size={16} />
                </IconButton>
              </div>
            </article>
          ))}
        </div>
      )}

      <Modal
        open={open}
        title="New Workflow"
        onClose={() => setOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setOpen(false)}>Cancel</Button>
            <Button type="submit" form="create-agent-form" tone="primary">Create & Open Canvas</Button>
          </>
        }
      >
        <form id="create-agent-form" className="form-stack" onSubmit={(event) => void createAgent(event)}>
          <Field label="NAME">
            <TextInput value={name} onChange={(event) => setName(event.target.value)} required placeholder="Customer Support Workflow" />
          </Field>
          <Field label="DESCRIPTION">
            <TextArea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Describe what this workflow should accomplish." />
          </Field>
        </form>
      </Modal>

      <Toast message={message} tone="good" />
    </div>
  );
}
