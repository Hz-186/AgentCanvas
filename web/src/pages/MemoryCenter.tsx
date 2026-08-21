import { FormEvent, useEffect, useMemo, useState } from 'react';
import { ArrowUpRight, BrainCircuit, Network, Pencil, Plus, Trash2 } from 'lucide-react';
import { settingsApi } from '../api/resources';
import { EditorialHeader } from '../components/editorial';
import { Button, EmptyState, Field, IconButton, Modal, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { ChangeProposal, Memory, MemoryRecallLog } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

const memoryTypes = [
  { value: 'profile_memory', label: '个人画像（偏好与设定）' },
  { value: 'episodic_memory', label: '事件记忆（具体经历）' },
  { value: 'task_memory', label: '任务记忆（目标与待办）' },
  { value: 'archival_memory', label: '归档记忆（长期事实）' },
];

function typeLabel(value: string) {
  if (value === 'summary_memory') return '旧摘要记忆（只读兼容）';
  return memoryTypes.find((option) => option.value === value)?.label ?? value;
}

export function MemoryCenter() {
  const [memories, setMemories] = useState<Memory[]>([]);
  const [candidates, setCandidates] = useState<ChangeProposal[]>([]);
  const [view, setView] = useState<'active' | 'pending' | 'conflicts' | 'history'>('active');
  const [open, setOpen] = useState(false);
  const [editingId, setEditingId] = useState(0);
  const [memoryType, setMemoryType] = useState(memoryTypes[0].value);
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [importance, setImportance] = useState(0.5);
  const [recallMemory, setRecallMemory] = useState<Memory | null>(null);
  const [recallLogs, setRecallLogs] = useState<MemoryRecallLog[]>([]);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try {
      const [memoryItems, candidateItems] = await Promise.all([settingsApi.memories.list(), settingsApi.memories.listCandidates('pending')]);
      setMemories(memoryItems); setCandidates(candidateItems); setError('');
    } catch (err) { setError(friendlyErrorMessage(err, '加载记忆失败')); }
  }
  useEffect(() => { void load(); }, []);

  const visibleMemories = useMemo(() => memories.filter((item) => {
    const status = item.status || 'active';
    if (view === 'active') return status === 'active' && !item.conflict_flag;
    if (view === 'conflicts') return Boolean(item.conflict_flag);
    if (view === 'history') return status === 'superseded' || status === 'revoked';
    return false;
  }), [memories, view]);

  function openCreate() {
    setEditingId(0); setMemoryType(memoryTypes[0].value); setTitle(''); setContent(''); setImportance(0.5); setOpen(true);
  }
  function openEdit(item: Memory) {
    setEditingId(item.id); setMemoryType(item.memory_type); setTitle(item.title || ''); setContent(item.content); setImportance(item.importance); setOpen(true);
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    try {
      const body = { memory_type: memoryType, title, content, importance, source: 'manual' };
      if (editingId) await settingsApi.memories.update(editingId, body); else await settingsApi.memories.create(body);
      setOpen(false); setMessage(editingId ? '记忆已更新' : '记忆已创建'); await load();
    } catch (err) { setError(friendlyErrorMessage(err, '保存记忆失败')); }
  }
  async function remove(id: number) {
    try { await settingsApi.memories.remove(id); setMessage('记忆已撤销，可在历史版本中追溯'); await load(); }
    catch (err) { setError(friendlyErrorMessage(err, '删除记忆失败')); }
  }
  async function decideCandidate(id: number, approved: boolean) {
    try { if (approved) await settingsApi.memories.approveCandidate(id); else await settingsApi.memories.rejectCandidate(id); setMessage(approved ? '候选已批准并写入有效记忆' : '候选已拒绝'); await load(); }
    catch (err) { setError(friendlyErrorMessage(err, '审核候选失败')); }
  }
  async function showRecallReason(item: Memory) {
    try { setRecallMemory(item); setRecallLogs(await settingsApi.memories.listRecallLogs(item.id)); }
    catch (err) { setError(friendlyErrorMessage(err, '加载召回记录失败')); }
  }

  return (
    <div className="page memory-page">
      <EditorialHeader word="Memory" script="Center" kicker="GOVERNED CONTEXT / V2" description="有效记忆、候选审核、冲突与版本历史 · 所有自动记忆先审核、后生效。" action={<Button tone="primary" onClick={openCreate}><Plus size={17} />New Memory</Button>} />
      {error ? <p className="error-text">{error}</p> : null}
      <div className="row-wrap" role="tablist" aria-label="Memory Center views">{([
        ['active', `有效记忆 ${memories.filter((item) => (item.status || 'active') === 'active' && !item.conflict_flag).length}`],
        ['pending', `待审核 ${candidates.length}`], ['conflicts', `冲突 ${memories.filter((item) => item.conflict_flag).length}`],
        ['history', `历史版本 ${memories.filter((item) => item.status === 'superseded' || item.status === 'revoked').length}`],
      ] as const).map(([value, label]) => <Button key={value} tone={view === value ? 'primary' : undefined} onClick={() => setView(value)}>{label}</Button>)}</div>
      {view === 'pending' ? (candidates.length === 0 ? <EmptyState title="没有待审核候选" description="Agent、Dream 和自动复盘产生的记忆会先出现在这里。" /> : <div className="resource-library-list memory-library-list">{candidates.map((candidate) => <article className="resource-library-item memory-library-item" key={candidate.id}><div className="resource-library-copy"><div className="card-title"><h3 className="truncate">{candidate.title || '记忆候选'}</h3><StatusBadge tone="warn">待审核 · {(candidate.confidence * 100).toFixed(0)}%</StatusBadge></div><p className="muted clamp-2">{candidate.content}</p><div className="meta-row"><span>SOURCE {String((candidate.payload_json as Record<string, unknown> | undefined)?.source || 'automatic')}</span><span>EVIDENCE {JSON.stringify(candidate.evidence_json ?? [])}</span></div></div><div className="resource-library-actions"><Button tone="primary" onClick={() => void decideCandidate(candidate.id, true)}>批准</Button><Button onClick={() => void decideCandidate(candidate.id, false)}>拒绝</Button></div></article>)}</div>) : visibleMemories.length === 0 ? <EmptyState title="还没有记忆" description="新增一条记忆后，Agent 就可以在流程中读取它。" /> : <div className="resource-library-list memory-library-list">{visibleMemories.map((memory) => <article className="resource-library-item memory-library-item" key={memory.id}><div className="resource-miniature memory-miniature" aria-hidden="true"><span><BrainCircuit size={16} /></span><i /><span><Network size={16} /></span><i /><span className="resource-miniature-end"><ArrowUpRight size={16} /></span></div><div className="resource-library-copy"><div className="card-title"><h3 className="truncate">{memory.title || memory.memory_type}</h3><StatusBadge tone={memory.status === 'revoked' ? 'neutral' : memory.conflict_flag ? 'bad' : 'info'}>{memory.status || 'active'} · {typeLabel(memory.memory_type)}</StatusBadge></div><p className="muted clamp-2">{memory.content}</p><div className="meta-row"><span>SCOPE {memory.scope_type || 'user'}:{memory.scope_id || memory.owner_id}</span><span>SOURCE {memory.source || 'unknown'}</span><span>ACCESSES {memory.access_count ?? 0}</span><span>UPDATED {formatDate(memory.updated_at)}</span></div></div><div className="resource-library-actions"><Button onClick={() => void showRecallReason(memory)}>为什么被召回</Button>{(memory.status || 'active') === 'active' ? <IconButton label="编辑记忆" onClick={() => openEdit(memory)}><Pencil size={16} /></IconButton> : null}{(memory.status || 'active') === 'active' ? <IconButton label="撤销记忆" onClick={() => void remove(memory.id)}><Trash2 size={16} /></IconButton> : null}</div></article>)}</div>}
      <Modal open={open} title={editingId ? '编辑记忆' : '新增记忆'} onClose={() => setOpen(false)} footer={<><Button type="button" onClick={() => setOpen(false)}>取消</Button><Button form="memory-page-form" tone="primary">保存</Button></>}><form id="memory-page-form" className="form-stack" onSubmit={(event) => void save(event)}><Field label="类型"><Select value={memoryType} onChange={(event) => setMemoryType(event.target.value)}>{memoryTypes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</Select></Field><Field label="标题"><TextInput value={title} onChange={(event) => setTitle(event.target.value)} /></Field><Field label="内容"><TextArea value={content} onChange={(event) => setContent(event.target.value)} required /></Field><Field label="重要度"><TextInput type="number" min={0} max={1} step={0.1} value={importance} onChange={(event) => setImportance(Number(event.target.value))} /></Field></form></Modal>
      <Modal open={Boolean(recallMemory)} title={`召回依据 · ${recallMemory?.title || recallMemory?.memory_type || ''}`} onClose={() => { setRecallMemory(null); setRecallLogs([]); }}><div className="stack">{recallLogs.length === 0 ? <EmptyState title="暂无召回记录" description="该记忆尚未实际进入 Agent 上下文。" /> : recallLogs.map((log) => { const detail = log.injected_json?.find((item) => item.memory_id === recallMemory?.id); return <article className="card" key={log.id}><div className="card-title"><h3 className="truncate">{log.query || '未记录查询'}</h3><StatusBadge tone="info">score {detail?.score?.toFixed(3) ?? '—'}</StatusBadge></div><p className="muted">{detail?.reason || 'unified_context_index'} · token {detail?.token_cost ?? 0} · run {log.run_id || '—'}</p><div className="row-wrap"><Button onClick={() => void settingsApi.memories.setRecallFeedback(log.id, 'helpful')}>有帮助</Button><Button onClick={() => void settingsApi.memories.setRecallFeedback(log.id, 'irrelevant')}>不相关</Button><Button onClick={() => void settingsApi.memories.setRecallFeedback(log.id, 'incorrect')}>错误记忆</Button></div></article>; })}</div></Modal>
      <Toast message={message} tone="good" />
    </div>
  );
}
