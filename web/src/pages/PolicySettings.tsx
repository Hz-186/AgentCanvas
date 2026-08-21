import { FormEvent, useEffect, useState } from 'react';
import { Plus, ShieldCheck, Trash2 } from 'lucide-react';
import { settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Panel, Select, StatusBadge, TextInput, Toast } from '../components/ui';
import type { ToolPolicy } from '../types/api';
import { friendlyErrorMessage } from '../utils/format';

export function PolicySettings() {
  const [policies, setPolicies] = useState<ToolPolicy[]>([]);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [risks, setRisks] = useState<string[]>(['high']);
  const [allowedHosts, setAllowedHosts] = useState('');
  const [timeoutMS, setTimeoutMS] = useState(30000);
  const [maxOutputBytes, setMaxOutputBytes] = useState(524288);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try { setPolicies(await settingsApi.tools.listPolicies()); setError(''); }
    catch (err) { setError(friendlyErrorMessage(err, '加载 Tool Policy 失败')); }
  }
  useEffect(() => { void load(); }, []);

  async function createPolicy(event: FormEvent) {
    event.preventDefault();
    try {
      await settingsApi.tools.createPolicy({ name, require_approval_for_risk: risks, max_timeout_ms: timeoutMS, max_output_bytes: maxOutputBytes, allowed_hosts: allowedHosts.split(',').map((item) => item.trim()).filter(Boolean) });
      setOpen(false); setName(''); setAllowedHosts(''); setMessage('Tool Policy 已创建'); await load();
    } catch (err) { setError(friendlyErrorMessage(err, '创建 Tool Policy 失败')); }
  }

  async function removePolicy(id: number) {
    try { await settingsApi.tools.removePolicy(id); setMessage('Tool Policy 已删除'); await load(); }
    catch (err) { setError(friendlyErrorMessage(err, '删除 Tool Policy 失败')); }
  }

  return <>{error ? <p className="error-text">{error}</p> : null}<Panel className="management-panel section-policies" title="Tool Policy" eyebrow="Governance" action={<Button tone="primary" onClick={() => setOpen(true)}><ShieldCheck size={16} />New</Button>}><div className="stack">{policies.length === 0 ? <EmptyState title="还没有 Tool Policy" description="创建策略后，可统一治理高风险工具的审批、超时与输出上限。" /> : policies.map((policy) => <article className="card" key={policy.id}><div className="card-title"><h3 className="truncate">{policy.name}</h3><StatusBadge tone="warn">{(policy.require_approval_for_risk ?? []).join(',') || 'none'}</StatusBadge></div><p className="muted truncate">timeout {policy.max_timeout_ms}ms · output {policy.max_output_bytes} bytes</p><p className="muted truncate">{(policy.allowed_hosts ?? []).join(', ') || '未限制 host'}</p><IconButton label="删除 Tool Policy" onClick={() => void removePolicy(policy.id)}><Trash2 size={16} /></IconButton></article>)}</div></Panel><Modal open={open} title="新增 Tool Policy" onClose={() => setOpen(false)} footer={<><Button type="button" onClick={() => setOpen(false)}>取消</Button><Button form="create-policy-form" tone="primary"><Plus size={15} />保存</Button></>}><form id="create-policy-form" className="form-stack" onSubmit={(event) => void createPolicy(event)}><Field label="名称"><TextInput value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="需审批风险等级"><Select multiple value={risks} onChange={(event) => setRisks(Array.from(event.target.selectedOptions).map((option) => option.value))}><option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option></Select></Field><Field label="Host Allowlist" hint="多个 host 用英文逗号分隔"><TextInput value={allowedHosts} onChange={(event) => setAllowedHosts(event.target.value)} placeholder="api.example.com" /></Field><Field label="超时毫秒"><TextInput type="number" min={1000} max={600000} value={timeoutMS} onChange={(event) => setTimeoutMS(Number(event.target.value))} /></Field><Field label="最大输出字节"><TextInput type="number" min={1024} value={maxOutputBytes} onChange={(event) => setMaxOutputBytes(Number(event.target.value))} /></Field></form></Modal><Toast message={message} tone="good" /></>;
}
