import { useEffect, useMemo, useState } from 'react';
import { Network } from 'lucide-react';
import { settingsApi } from '../api/resources';
import { EditorialHeader } from '../components/editorial';
import { Button, EmptyState, Modal, StatusBadge, Toast } from '../components/ui';
import type { Memory, MemoryRecallLog } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

/** Durable memory is owned by the asynchronous durable-memory consolidation worker. */
export function MemoryCenter() {
  const [memories, setMemories] = useState<Memory[]>([]);
  const [view, setView] = useState<'active' | 'conflicts' | 'history'>('active');
  const [recallMemory, setRecallMemory] = useState<Memory | null>(null);
  const [recallLogs, setRecallLogs] = useState<MemoryRecallLog[]>([]);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try {
      setMemories(await settingsApi.memories.list());
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载记忆审计数据失败'));
    }
  }

  useEffect(() => { void load(); }, []);

  const visibleMemories = useMemo(() => memories.filter((item) => {
    const status = item.status || 'active';
    if (view === 'active') return status === 'active' && !item.has_conflict;
    if (view === 'conflicts') return Boolean(item.has_conflict);
    return status === 'superseded' || status === 'revoked';
  }), [memories, view]);

  async function showRecallReason(item: Memory) {
    try {
      setRecallMemory(item);
      setRecallLogs(await settingsApi.memories.listRecallLogs(item.id));
    } catch (err) {
      setMessage('');
      setError(friendlyErrorMessage(err, '加载召回记录失败'));
    }
  }

  return (
    <div className="page memory-page">
      <EditorialHeader word="Memory" script="Audit" kicker="DURABLE MEMORY" description="持久记忆由后台单一 consolidation 管线维护；此处只展示审计快照与召回证据。" action={<span className="muted"><Network size={16} /> read-only</span>} />
      {error ? <p className="error-text">{error}</p> : null}
      <div className="row-wrap" role="tablist" aria-label="Memory audit views">
        {([
          ['active', `有效 ${memories.filter((item) => (item.status || 'active') === 'active' && !item.has_conflict).length}`],
          ['conflicts', `冲突 ${memories.filter((item) => item.has_conflict).length}`],
          ['history', `历史 ${memories.filter((item) => item.status === 'superseded' || item.status === 'revoked').length}`],
        ] as const).map(([value, label]) => <Button key={value} tone={view === value ? 'primary' : undefined} onClick={() => setView(value)}>{label}</Button>)}
      </div>
      {visibleMemories.length === 0 ? <EmptyState title="暂无审计记忆" description="新的 rollout 会由后台 consolidation 管线异步处理。" /> : <div className="resource-library-list memory-library-list">{visibleMemories.map((item) => <article className="resource-library-item memory-library-item" key={item.id}><div className="resource-library-copy"><div className="card-title"><h3 className="truncate">{item.title || 'Durable memory'}</h3><StatusBadge tone={item.status === 'revoked' ? 'neutral' : item.has_conflict ? 'bad' : 'info'}>{item.status || 'active'}</StatusBadge></div><p className="muted clamp-3">{item.content}</p><div className="meta-row"><span>SOURCE {item.source || 'consolidation'}</span>{item.source_conversation_id ? <span>CONVERSATION {item.source_conversation_id}</span> : null}{item.source_project_id ? <span>PROJECT {item.source_project_id}</span> : null}<span>UPDATED {formatDate(item.updated_at)}</span></div></div><div className="resource-library-actions"><Button onClick={() => void showRecallReason(item)}>召回证据</Button></div></article>)}</div>}
      <Modal open={Boolean(recallMemory)} title={`召回证据 · ${recallMemory?.title || 'Durable memory'}`} onClose={() => { setRecallMemory(null); setRecallLogs([]); }}><div className="stack">{recallLogs.length === 0 ? <EmptyState title="暂无召回记录" description="该记忆尚未进入 Agent 上下文。" /> : recallLogs.map((log) => <article className="card" key={log.id}><div className="card-title"><h3 className="truncate">{log.query || '未记录查询'}</h3><StatusBadge tone="info">run {log.run_id || '—'}</StatusBadge></div><p className="muted">token {log.token_cost ?? 0} · {formatDate(log.created_at)}</p></article>)}</div></Modal>
      <Toast message={message} tone="good" />
    </div>
  );
}
