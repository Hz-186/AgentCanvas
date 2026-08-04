import { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import {
  ArrowUpRight,
  Bot,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  GitBranch,
  MessageSquareText,
  Plus,
  Search,
  Send,
  Settings2,
  Square,
  Trash2,
  X,
} from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import { agentApi, knowledgeApi, runApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import { EditorialHeader, ResizableRail, paneStyle, storedWidth } from '../components/editorial';
import { ApprovalQueue } from '../components/ApprovalQueue';
import type {
  Agent,
  AgentEditableSettings,
  AgentTurn,
  ApprovalRequest,
  Conversation,
  KnowledgeBase,
  Message,
  MessageSearchResult,
  ModelProvider,
  Run,
  RunEvent,
} from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

type AgentMode = NonNullable<Conversation['agent_mode']>;

const terminalTurnStatuses = new Set(['succeeded', 'failed', 'cancelled', 'waiting_human', 'paused']);
const activeTurnStatuses = new Set(['queued', 'retry_wait', 'running']);
const inspectorWidthKey = 'agentcanvas-agent-inspector-width';

const emptySettings = (providerID = 0): AgentEditableSettings => ({
  provider_id: providerID,
  model: '',
  system_prompt: '',
  knowledge_ids: [],
});

function eventPayload(event: RunEvent): Record<string, unknown> {
  if (!event.payload_json) return {};
  try { return JSON.parse(event.payload_json) as Record<string, unknown>; } catch { return {}; }
}

function tracePresentation(event: RunEvent): { title: string; summary: string } {
  const payload = eventPayload(event);
  const stepType = typeof payload.type === 'string' ? payload.type : '';
  const title = (event.event_type === 'agent_step' ? stepType : event.event_type).split('_').join(' ');
  if (typeof payload.error === 'string') return { title, summary: payload.error };
  if (typeof payload.tool_name === 'string') return { title, summary: payload.tool_name };
  if (typeof payload.content === 'string' && ['plan', 'plan_revision', 'tool_result', 'final_answer', 'error'].includes(stepType)) {
    return { title, summary: payload.content };
  }
  if (stepType === 'llm_response') return { title, summary: '模型响应已接收' };
  if (typeof payload.stop_reason === 'string') return { title, summary: payload.stop_reason };
  return { title, summary: `event #${event.id}` };
}

function nextIdempotencyKey(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return `turn-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function mergeMessages(current: Message[], incoming: Message[]): Message[] {
  const byID = new Map<number, Message>();
  [...current.filter((item) => item.id < 0), ...incoming].forEach((item) => byID.set(item.id, item));
  return Array.from(byID.values()).sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime());
}

export function visibleChatMessages(items: Message[]): Message[] {
  return items.filter((item) => item.role === 'user' || item.role === 'assistant');
}

export function deduplicateSearchResults(items: MessageSearchResult[]): MessageSearchResult[] {
  const seen = new Set<number>();
  return items.filter((item) => {
    if (seen.has(item.conversation_id)) return false;
    seen.add(item.conversation_id);
    return true;
  });
}

export function ChatPage() {
  const navigate = useNavigate();
	const { agentId: routeAgentID, conversationId: routeConversationID } = useParams();
	const agentID = routeAgentID ? Number(routeAgentID) : undefined;
  const scoped = Boolean(agentID && !Number.isNaN(agentID));
  const isNewConversation = routeConversationID === 'new';
  const conversationID = routeConversationID && !isNewConversation ? Number(routeConversationID) : undefined;

  const [agents, setAgents] = useState<Agent[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [trace, setTrace] = useState<RunEvent[]>([]);
  const [turn, setTurn] = useState<AgentTurn | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [childRuns, setChildRuns] = useState<Run[]>([]);
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([]);
  const [question, setQuestion] = useState('');
  const [mode, setMode] = useState<AgentMode>('react');
  const [slashOpen, setSlashOpen] = useState(false);
  const [slashIndex, setSlashIndex] = useState(0);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState('');
  const [newProviderID, setNewProviderID] = useState(0);
  const [settings, setSettings] = useState<AgentEditableSettings>(emptySettings());
  const [settingsSaved, setSettingsSaved] = useState(false);
  const [sessionQuery, setSessionQuery] = useState('');
  const [sessionResults, setSessionResults] = useState<MessageSearchResult[]>([]);
  const [hasSearched, setHasSearched] = useState(false);
  const [inspectorWidth, setInspectorWidth] = useState(() => storedWidth(inspectorWidthKey, 380));

  const shellRef = useRef<HTMLDivElement | null>(null);
  const messageListRef = useRef<HTMLDivElement | null>(null);
  const pollGeneration = useRef(0);
  const streamAbort = useRef<AbortController | null>(null);
  const pendingConversation = useRef<number | null>(null);

  const currentAgent = useMemo(() => agents.find((item) => item.id === agentID), [agents, agentID]);
  const currentConversation = useMemo(() => conversations.find((item) => item.id === conversationID), [conversations, conversationID]);
  const visibleMessages = useMemo(() => visibleChatMessages(messages), [messages]);
  const searchResults = useMemo(() => deduplicateSearchResults(sessionResults), [sessionResults]);
  const turnBlocksNewMessage = turn?.status === 'waiting_human' || turn?.status === 'paused';
  const modeOptions: Array<{ value: AgentMode; label: string; description: string }> = [
    { value: 'react', label: 'ReAct', description: '边分析边行动，按需调用能力' },
    { value: 'plan_execute', label: 'Plan Guided', description: '先制定计划，再按计划推进' },
  ];

  async function reloadAgents() {
    const list = await agentApi.list();
    setAgents(list);
    return list;
  }

  useEffect(() => {
    let cancelled = false;
    Promise.all([agentApi.list(), settingsApi.providers.list(), knowledgeApi.list()]).then(([agentList, providerList, kbList]) => {
      if (cancelled) return;
      setAgents(agentList);
      setProviders(providerList);
      setKnowledgeBases(kbList.filter((item) => item.status === 1));
      setNewProviderID(providerList[0]?.id ?? 0);
	}).catch((cause) => !cancelled && setError(friendlyErrorMessage(cause, '加载 Agent 数据失败')));
	return () => { cancelled = true; pollGeneration.current += 1; streamAbort.current?.abort(); };
	}, []);

  useEffect(() => {
    if (!currentAgent) return;
    setSettings({ ...currentAgent.settings, knowledge_ids: [...(currentAgent.settings.knowledge_ids ?? [])] });
    setSettingsSaved(false);
  }, [currentAgent?.id, currentAgent?.updated_at]);

  useEffect(() => {
    if (!agentID) { setConversations([]); return; }
    let cancelled = false;
    agentApi.listConversations(agentID).then((items) => {
      if (cancelled) return;
      setConversations(items);
      if (!routeConversationID && items[0]) navigate(`/app/agents/${agentID}/chat/${items[0].id}`, { replace: true });
    }).catch((cause) => !cancelled && setError(friendlyErrorMessage(cause, '加载会话失败')));
    return () => { cancelled = true; };
  }, [agentID, routeConversationID, navigate]);

  useEffect(() => {
    pollGeneration.current += 1;
    const generation = pollGeneration.current;
    streamAbort.current?.abort();
    setTrace([]);
    setTurn(null);
    setRun(null);
    setChildRuns([]);
    setApprovals([]);
    setBusy(false);
    if (!conversationID || !agentID) {
      if (isNewConversation) setMessages([]);
      setMode('react');
      return;
    }
    const preservePending = pendingConversation.current === conversationID;
    if (!preservePending) setMessages([]);
    let cancelled = false;
    setMode(currentConversation?.agent_mode ?? 'react');
    agentApi.listMessages(agentID, conversationID).then((items) => {
      if (cancelled) return;
      setMessages((current) => preservePending ? mergeMessages(current, items) : items);
      if (pendingConversation.current === conversationID) pendingConversation.current = null;
    }).catch((cause) => !cancelled && setError(friendlyErrorMessage(cause, '加载消息失败')));
    agentApi.getLatestTurn(agentID, conversationID).then((latest) => {
      if (cancelled || pollGeneration.current !== generation) return;
      setTurn(latest);
      if (latest.run_id) {
        Promise.all([runApi.getRun(latest.run_id), runApi.listRunEvents(latest.run_id), runApi.listChildRuns(latest.run_id)]).then(([latestRun, events, children]) => {
          if (cancelled || pollGeneration.current !== generation) return;
          setRun(latestRun); setTrace(events); setChildRuns(children);
        }).catch(() => undefined);
      }
      if (latest.run_id && (activeTurnStatuses.has(latest.status) || latest.status === 'waiting_human' || latest.status === 'paused')) {
        setBusy(activeTurnStatuses.has(latest.status));
        void monitorTurn(latest.id, latest.run_id, generation, conversationID);
      }
    }).catch(() => undefined);
    return () => { cancelled = true; streamAbort.current?.abort(); };
  }, [agentID, conversationID, isNewConversation]);

  useEffect(() => {
    if (conversationID && currentConversation) setMode(currentConversation.agent_mode ?? 'react');
  }, [conversationID, currentConversation?.agent_mode]);

  useEffect(() => {
    const node = messageListRef.current;
    if (node) node.scrollTo({ top: node.scrollHeight, behavior: 'smooth' });
  }, [visibleMessages.length, busy, turn?.status]);

  useEffect(() => {
    if (!sessionQuery.trim()) {
      setSessionResults([]);
      setHasSearched(false);
    }
  }, [sessionQuery]);

  async function createAgent(event: FormEvent) {
    event.preventDefault();
    if (!newName.trim() || !newProviderID) { setError('请输入 Agent 名称并选择模型 Provider'); return; }
    setBusy(true); setError('');
    try {
      const item = await agentApi.create({ name: newName.trim(), settings: emptySettings(newProviderID) });
      await reloadAgents();
      setCreateOpen(false); setNewName('');
      navigate(`/app/agents/${item.id}/chat/new`);
    } catch (cause) { setError(friendlyErrorMessage(cause, '创建 Agent 失败')); } finally { setBusy(false); }
  }

  async function removeAgent(id: number) {
    if (!window.confirm('确认归档这个 Agent 吗？')) return;
    try { await agentApi.remove(id); await reloadAgents(); if (agentID === id) navigate('/app/agents'); }
    catch (cause) { setError(friendlyErrorMessage(cause, '归档 Agent 失败')); }
  }

  async function saveSettings() {
    if (!agentID) return;
    setBusy(true); setError(''); setSettingsSaved(false);
    try {
      const item = await agentApi.updateSettings(agentID, settings);
      setAgents((current) => current.map((candidate) => candidate.id === item.id ? item : candidate));
      setSettings(item.settings);
      setSettingsSaved(true);
      setMessage('配置已更新，将应用于新会话');
    } catch (cause) { setError(friendlyErrorMessage(cause, '保存 Agent 设置失败')); }
    finally { setBusy(false); }
  }

  async function createConversation(selectedMode = mode): Promise<Conversation | null> {
    if (!agentID) return null;
    try {
      const item = await agentApi.createConversation(agentID, undefined, selectedMode);
      setConversations((current) => [item, ...current]);
      return item;
    } catch (cause) { setError(friendlyErrorMessage(cause, '创建会话失败')); return null; }
  }

  async function removeConversation(id: number) {
    if (!agentID || !window.confirm('确认删除这个会话吗？')) return;
    try {
      await agentApi.removeConversation(agentID, id);
      const rest = conversations.filter((item) => item.id !== id); setConversations(rest);
      navigate(rest[0] ? `/app/agents/${agentID}/chat/${rest[0].id}` : `/app/agents/${agentID}/chat/new`, { replace: true });
    } catch (cause) { setError(friendlyErrorMessage(cause, '删除会话失败')); }
  }

  async function searchSessions(event?: FormEvent) {
    event?.preventDefault();
    if (!agentID || !sessionQuery.trim()) { setSessionResults([]); setHasSearched(false); return; }
    try {
      setSessionResults(await agentApi.searchSessions(agentID, sessionQuery.trim(), 30));
      setHasSearched(true);
    } catch (cause) { setError(friendlyErrorMessage(cause, '搜索历史会话失败')); }
  }

  async function forkConversation(upgrade: boolean) {
    if (!agentID || !conversationID) return;
    try {
      const item = upgrade ? await agentApi.upgradeConversation(agentID, conversationID) : await agentApi.forkConversation(agentID, conversationID);
      setConversations((current) => [item, ...current]);
      navigate(`/app/agents/${agentID}/chat/${item.id}`);
    } catch (cause) { setError(friendlyErrorMessage(cause, upgrade ? '升级会话失败' : '分支会话失败')); }
  }

  async function monitorTurn(turnID: number, runID: number, generation: number, activeConversationID: number) {
    streamAbort.current?.abort();
    const controller = new AbortController();
    streamAbort.current = controller;
    let lastEventId: string | undefined;
    let reconnects = 0;
    while (pollGeneration.current === generation && !controller.signal.aborted) {
      let streamError: Error | undefined;
      await agentApi.streamRunEvents(runID, lastEventId, {
        signal: controller.signal,
        onMessage: (streamMessage) => {
          if (pollGeneration.current !== generation || streamMessage.event === 'run_status' || streamMessage.event === 'error') return;
          try {
            const event = JSON.parse(streamMessage.data) as RunEvent;
            if (!event.id) return;
            lastEventId = streamMessage.id ?? String(event.id);
            setTrace((current) => current.some((item) => item.id === event.id) ? current : [...current, event]);
          } catch { /* 状态帧由随后的 Turn 查询处理。 */ }
        },
        onError: (cause) => { streamError = cause; },
      });
      if (pollGeneration.current !== generation || controller.signal.aborted) return;
      try {
        const nextTurn = await agentApi.getTurn(turnID);
        if (pollGeneration.current !== generation) return;
        setTurn(nextTurn);
        if (terminalTurnStatuses.has(nextTurn.status)) {
          if (nextTurn.status === 'waiting_human') {
            const pending = await runApi.listApprovalRequests('pending');
            setApprovals(pending.filter((item) => item.run_id === runID));
          } else setApprovals([]);
          if (agentID) setMessages(await agentApi.listMessages(agentID, activeConversationID));
          const [latestRun, children] = await Promise.all([runApi.getRun(runID), runApi.listChildRuns(runID)]);
          setRun(latestRun); setChildRuns(children); setBusy(false);
          if (streamAbort.current === controller) streamAbort.current = null;
          return;
        }
      } catch (cause) { streamError = cause instanceof Error ? cause : new Error(String(cause)); }
      reconnects += 1;
      if (streamError && reconnects === 3) setError('Agent 事件流暂时中断，正在自动重连。');
      await new Promise((resolve) => window.setTimeout(resolve, Math.min(250 * reconnects, 2000)));
    }
  }

  async function send(event: FormEvent) {
    event.preventDefault();
    const content = question.trim();
    if (!content || !agentID || busy || slashOpen) return;
    let activeConversationID = conversationID;
    let created: Conversation | null = null;
    if (!activeConversationID) {
      created = await createConversation(mode);
      activeConversationID = created?.id;
    }
    if (!activeConversationID) return;
    const optimisticID = -Date.now();
    setBusy(true); setError(''); setQuestion(''); setTrace([]); setApprovals([]);
    setMessages((current) => [...current, { id: optimisticID, owner_id: 0, conversation_id: activeConversationID!, role: 'user', content, content_type: 'text', token_count: 0, created_at: new Date().toISOString() }]);
    try {
      const accepted = await agentApi.startTurn(agentID, activeConversationID, content, nextIdempotencyKey());
      setMessages((current) => mergeMessages(current.filter((item) => item.id !== optimisticID), [accepted.user_message]));
      setTurn(accepted.turn); setRun(accepted.run); setChildRuns([]);
      if (created) {
        pendingConversation.current = activeConversationID;
        navigate(`/app/agents/${agentID}/chat/${activeConversationID}`, { replace: true });
        return;
      }
      const generation = pollGeneration.current + 1; pollGeneration.current = generation;
      await monitorTurn(accepted.turn.id, accepted.run.id, generation, activeConversationID);
    } catch (cause) { setBusy(false); setError(friendlyErrorMessage(cause, '启动 Agent Run 失败')); }
  }

  async function stopRun() {
    if (!turn?.run_id) return;
    try {
      const cancelledRun = await runApi.cancelRun(turn.run_id);
      streamAbort.current?.abort();
      const nextTurn = await agentApi.getTurn(turn.id);
      setTurn(nextTurn); setRun(cancelledRun);
      if (agentID) setMessages(await agentApi.listMessages(agentID, turn.conversation_id));
      setBusy(false);
    } catch (cause) { setError(friendlyErrorMessage(cause, '停止 Run 失败')); }
  }

  async function decideApproval(item: ApprovalRequest, approved: boolean, optionID?: string) {
    if (!item.run_id) return;
    setBusy(true); setError('');
    try {
      if (approved) await runApi.approveRequest(item.id, optionID ? `choice:${optionID}` : undefined);
      else await runApi.rejectRequest(item.id, 'Rejected from Agent Chat');
      setApprovals([]);
      if (!turn) return;
      const nextTurn = await agentApi.getTurn(turn.id); setTurn(nextTurn);
      const generation = pollGeneration.current + 1; pollGeneration.current = generation;
      await monitorTurn(nextTurn.id, item.run_id, generation, nextTurn.conversation_id);
    } catch (cause) { setBusy(false); setError(friendlyErrorMessage(cause, '恢复 Agent Run 失败')); }
  }

  async function resumePausedRun() {
    if (!turn?.run_id) return;
    setBusy(true); setError('');
    try {
      await runApi.resumeRun(turn.run_id);
      const nextTurn = await agentApi.getTurn(turn.id); setTurn(nextTurn);
      const generation = pollGeneration.current + 1; pollGeneration.current = generation;
      await monitorTurn(nextTurn.id, turn.run_id, generation, nextTurn.conversation_id);
    } catch (cause) { setBusy(false); setError(friendlyErrorMessage(cause, '继续 Agent Run 失败')); }
  }

  async function selectMode(nextMode: AgentMode) {
    if (busy || turnBlocksNewMessage || !agentID) return;
    try {
      if (conversationID) {
        const updated = await agentApi.updateConversationMode(agentID, conversationID, nextMode);
        setConversations((current) => current.map((item) => item.id === updated.id ? updated : item));
      }
      setMode(nextMode); setQuestion(''); setSlashOpen(false); setSlashIndex(0);
    } catch (cause) { setError(friendlyErrorMessage(cause, '切换会话模式失败')); }
  }

  function onComposerKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (slashOpen) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        setSlashIndex((current) => event.key === 'ArrowDown' ? (current + 1) % modeOptions.length : (current - 1 + modeOptions.length) % modeOptions.length);
        return;
      }
      if (event.key === 'Enter') { event.preventDefault(); void selectMode(modeOptions[slashIndex].value); return; }
      if (event.key === 'Escape') { event.preventDefault(); setSlashOpen(false); setQuestion(''); return; }
    }
    if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); }
  }

  function setInspector(nextWidth: number) {
    setInspectorWidth(nextWidth);
    localStorage.setItem(inspectorWidthKey, String(nextWidth));
    if (nextWidth > 0) localStorage.setItem(`${inspectorWidthKey}-expanded`, String(nextWidth));
  }

  function reopenInspector() {
    const stored = Number(localStorage.getItem(`${inspectorWidthKey}-expanded`));
    setInspector(Number.isFinite(stored) && stored >= 320 ? Math.min(560, stored) : 380);
  }

  if (!scoped) {
    return <div className="page dialogue-page">
      <EditorialHeader word="Agent" script="Chat" kicker="INDEPENDENT AGENTS" description="创建一个 Agent，然后像使用 Codex 一样直接开始任务。" action={<Button tone="primary" onClick={() => setCreateOpen(true)}><Plus size={17} />New Agent</Button>} />
      <div className="stack">
        {agents.length === 0 ? <EmptyState icon={<Bot size={24} />} title="还没有 Agent" description="创建后即可开始多轮对话。" action={<Button tone="primary" onClick={() => setCreateOpen(true)}>创建 Agent</Button>} /> :
          <div className="resource-library-list dialog-library-list">{agents.map((item) => <article className="resource-library-item dialog-library-item" key={item.id}>
            <div className="resource-miniature dialog-miniature"><span><Bot size={16} /></span><i /><span className="resource-miniature-end"><MessageSquareText size={16} /></span></div>
            <div className="resource-library-copy"><div className="card-title"><h3 className="truncate">{item.name}</h3><StatusBadge tone={item.status === 'active' ? 'good' : 'neutral'}>{item.status}</StatusBadge></div><p className="muted clamp-2">{item.description || item.settings.system_prompt || '使用服务端默认提示词'}</p><div className="meta-row"><span>RELEASE {item.current_release_id ?? '—'}</span><span>{formatDate(item.updated_at)}</span></div></div>
            <div className="resource-library-actions"><Button tone="primary" onClick={() => navigate(`/app/agents/${item.id}/chat`)}>Open Agent<ArrowUpRight size={16} /></Button><IconButton label="归档 Agent" className="icon-btn-danger" onClick={() => void removeAgent(item.id)}><Trash2 size={16} /></IconButton></div>
          </article>)}</div>}
      </div>
      <Modal open={createOpen} title="Create Agent" onClose={() => setCreateOpen(false)}><form className="stack" onSubmit={(event) => void createAgent(event)}><Field label="Name"><TextInput value={newName} onChange={(event) => setNewName(event.target.value)} autoFocus /></Field><Field label="Model Provider"><Select value={newProviderID} onChange={(event) => setNewProviderID(Number(event.target.value))}><option value={0}>Select provider</option>{providers.map((providerItem) => <option value={providerItem.id} key={providerItem.id}>{providerItem.name}</option>)}</Select></Field><Button tone="primary" type="submit" disabled={busy}>Create Agent</Button></form></Modal>
      {error ? <Toast tone="bad" message={error} onClose={() => setError('')} /> : null}
    </div>;
  }

  const shellStyle = paneStyle({
    '--dialog-nav-width': `${storedWidth('agentcanvas-agent-navigator-width', 270)}px`,
    '--dialog-inspector-width': `${inspectorWidth}px`,
  });
  const runDetailsVisible = trace.length > 0 || childRuns.length > 0 || approvals.length > 0 || Boolean(run);

  return <div className="page chat-page-scoped"><div ref={shellRef} className="chat-shell" style={shellStyle}>
    <aside className="chat-sidebar glass">
      <div className="chat-sidebar-head">
        <button type="button" className="chat-back" onClick={() => navigate('/app/agents')}><ChevronLeft size={15} />全部 Agent</button>
        <h2 className="truncate">{currentAgent?.name ?? 'Agent'}</h2>
        <Button tone="primary" onClick={() => navigate(`/app/agents/${agentID}/chat/new`)}><Plus size={15} />新建会话</Button>
        <form className="chat-search-form" role="search" onSubmit={(event) => void searchSessions(event)}>
          <TextInput value={sessionQuery} onChange={(event) => { setSessionQuery(event.target.value); setHasSearched(false); }} placeholder="搜索历史会话" aria-label="搜索历史会话" />
          {sessionQuery ? <IconButton type="button" label="清空搜索" className="chat-search-clear" onClick={() => setSessionQuery('')}><X size={14} /></IconButton> : null}
          <IconButton type="submit" label="搜索" className="chat-search-submit"><Search size={15} /></IconButton>
        </form>
      </div>
      <div className="chat-conversation-list">
        {sessionQuery.trim() ? <>
          {!hasSearched ? <p className="chat-empty-hint">按 Enter 或点击放大镜搜索</p> : null}
          {hasSearched && searchResults.length === 0 ? <p className="chat-empty-hint">没有找到相关会话</p> : null}
          {searchResults.map((item) => <button type="button" className="chat-conversation-item chat-search-result" key={item.conversation_id} onClick={() => navigate(`/app/agents/${agentID}/chat/${item.conversation_id}`)}><span className="truncate">{item.content}</span><span className="chat-conversation-time">{item.role} · {formatDate(item.created_at)}</span></button>)}
        </> : <>
          {isNewConversation ? <div className="chat-conversation-item active"><span>新会话…</span></div> : null}
          {conversations.map((item) => <div className={`chat-conversation-item ${item.id === conversationID ? 'active' : ''}`} key={item.id}><button type="button" onClick={() => navigate(`/app/agents/${agentID}/chat/${item.id}`)}><span className="truncate">{item.title || '未命名会话'}</span><span className="chat-conversation-time">{item.agent_mode === 'plan_execute' ? 'Plan Guided' : 'ReAct'} · {formatDate(item.last_message_at ?? item.updated_at)}</span></button><IconButton label="删除会话" className="chat-delete" onClick={() => void removeConversation(item.id)}><Trash2 size={14} /></IconButton></div>)}
        </>}
      </div>
    </aside>
    <ResizableRail containerRef={shellRef} variable="--dialog-nav-width" storageKey="agentcanvas-agent-navigator-width" side="left" min={220} max={380} collapsed={112} defaultWidth={270} label="调整会话导航宽度" />
    <section className="chat-main surface">
      <div className="chat-session-heading">
        <div><span>AGENT CHAT</span><strong>{currentAgent?.name}</strong></div>
        <div className="chat-heading-actions">
          {conversationID ? <><button className="chat-back" type="button" onClick={() => void forkConversation(false)}><GitBranch size={13} />Fork</button><button className="chat-back" type="button" onClick={() => void forkConversation(true)}>新配置会话</button></> : null}
          <IconButton label={inspectorWidth > 0 ? '收起全局设置' : '打开全局设置'} onClick={() => inspectorWidth > 0 ? setInspector(0) : reopenInspector()}><Settings2 size={16} /></IconButton>
        </div>
      </div>
      <div className="message-list" ref={messageListRef} role="log" aria-live="polite" aria-busy={busy}>
        {visibleMessages.length === 0 && !busy ? <EmptyState icon={<MessageSquareText size={24} />} title="开始对话" description="输入任务，或输入 / 选择运行模式。" /> : null}
        {visibleMessages.map((item) => <article className={`message ${item.role}`} key={item.id}><span className="message-role">{item.role === 'user' ? '你' : currentAgent?.name ?? 'Agent'}</span><p>{item.content}</p></article>)}
        {busy ? <article className="message assistant pending"><span className="message-role">{currentAgent?.name ?? 'Agent'}</span><p><i />{turn?.status === 'queued' ? '任务已排队…' : '正在处理…'}</p></article> : null}
        {turn?.status === 'failed' ? <article className="message assistant failed"><span className="message-role">运行失败</span><p>{turn.error_message || 'Agent 未能完成本次任务。'}</p></article> : null}
        {runDetailsVisible ? <details className="chat-run-details">
          <summary><ChevronDown size={14} /><span>执行详情</span>{run ? <StatusBadge tone={run.status === 'succeeded' ? 'good' : run.status === 'failed' ? 'bad' : 'info'}>{run.status}</StatusBadge> : null}</summary>
          <div className="chat-run-details-body stack">
            {run ? <div className="meta-row"><span>{run.total_tokens ?? 0} tok</span><span>{run.latency_ms ?? 0} ms</span><span>{run.run_type}</span><span>depth {run.delegation_depth}</span></div> : null}
            {trace.map((item) => { const view = tracePresentation(item); return <article className="chat-trace-row" key={item.id}><strong>{view.title}</strong><p>{view.summary}</p></article>; })}
            {childRuns.map((child) => <article className="chat-trace-row" key={child.id}><strong>Subagent run {child.id}</strong><p>{child.status} · {child.total_tokens} tok</p></article>)}
            <ApprovalQueue items={approvals} onDecide={(item, approve, optionID) => void decideApproval(item, approve, optionID)} />
          </div>
        </details> : null}
      </div>
      <form className="chat-composer" onSubmit={(event) => void send(event)}>
        <div className="chat-composer-input">
          {slashOpen ? <div className="slash-menu" role="listbox" aria-label="选择 Agent 模式">{modeOptions.map((option, index) => <button type="button" role="option" aria-selected={index === slashIndex} className={index === slashIndex ? 'active' : ''} key={option.value} onMouseEnter={() => setSlashIndex(index)} onClick={() => void selectMode(option.value)}><strong>{option.label}</strong><span>{option.description}</span></button>)}</div> : null}
          <TextArea value={question} onChange={(event) => { const value = event.target.value; setQuestion(value); setSlashOpen(value === '/' || value.startsWith('/mode')); }} placeholder={turnBlocksNewMessage ? '请先处理当前暂停或审批中的 Run' : '交给 Agent 一个任务…'} disabled={busy || turnBlocksNewMessage} onKeyDown={onComposerKeyDown} />
        </div>
        <div className="chat-composer-actions">
          <button type="button" className="mode-chip" aria-label={`当前模式：${mode === 'plan_execute' ? 'Plan Guided' : 'ReAct'}`} disabled={busy || turnBlocksNewMessage} onClick={() => { setQuestion('/'); setSlashOpen(true); }}><span>{mode === 'plan_execute' ? 'Plan Guided' : 'ReAct'}</span><ChevronDown size={13} /></button>
          <div className="chat-send-actions">{busy ? <Button type="button" tone="danger" onClick={() => void stopRun()}><Square size={15} />Stop</Button> : turn?.status === 'paused' ? <Button type="button" tone="primary" onClick={() => void resumePausedRun()}>Resume</Button> : turn?.status === 'waiting_human' ? <Button type="button" disabled>Awaiting approval</Button> : <Button tone="primary" type="submit" disabled={!question.trim() || slashOpen}><Send size={16} />发送</Button>}</div>
        </div>
      </form>
    </section>
    <ResizableRail containerRef={shellRef} variable="--dialog-inspector-width" storageKey={inspectorWidthKey} side="right" min={320} max={560} collapsed={140} defaultWidth={380} onCommit={setInspectorWidth} label="调整 Agent 设置宽度" />
    <aside className="chat-inspector dialog-inspector glass">
      <div className="chat-inspector-head"><div><p className="eyebrow">AGENT SETTINGS</p><h3>全局设置</h3></div><IconButton label="收起设置面板" onClick={() => setInspector(0)}><ChevronRight size={16} /></IconButton></div>
      <div className="chat-inspector-body">
        <Field label="Model Provider"><Select value={settings.provider_id} onChange={(event) => setSettings((current) => ({ ...current, provider_id: Number(event.target.value), model: '' }))}>{providers.map((providerItem) => <option value={providerItem.id} key={providerItem.id}>{providerItem.name}</option>)}</Select></Field>
        <Field label="Model" hint="留空时使用 Provider 的默认模型"><TextInput value={settings.model} onChange={(event) => setSettings((current) => ({ ...current, model: event.target.value }))} placeholder={providers.find((item) => item.id === settings.provider_id)?.default_chat_model || 'Provider 默认模型'} /></Field>
        <Field label="全局 System Prompt"><TextArea className="textarea settings-prompt" value={settings.system_prompt} onChange={(event) => setSettings((current) => ({ ...current, system_prompt: event.target.value }))} placeholder="留空时使用系统默认提示词" /></Field>
        <Field label="知识库"><div className="knowledge-checklist">{knowledgeBases.length === 0 ? <p className="muted">暂无可用知识库</p> : knowledgeBases.map((kb) => <label key={kb.id}><input type="checkbox" checked={settings.knowledge_ids.includes(kb.id)} onChange={(event) => setSettings((current) => ({ ...current, knowledge_ids: event.target.checked ? [...current.knowledge_ids, kb.id] : current.knowledge_ids.filter((id) => id !== kb.id) }))} /><span><strong>{kb.name}</strong><small>{kb.document_count} 个文档</small></span></label>)}</div></Field>
        <Field label="Temperature" hint="留空时使用模型默认值"><TextInput type="number" min={0} max={2} step={0.1} value={settings.temperature ?? ''} onChange={(event) => setSettings((current) => ({ ...current, temperature: event.target.value === '' ? undefined : Number(event.target.value) }))} /></Field>
        {settingsSaved ? <div className="settings-saved-notice"><p>配置已更新，将应用于新会话。</p><Button type="button" onClick={() => navigate(`/app/agents/${agentID}/chat/new`)}><Plus size={14} />使用新配置新建会话</Button></div> : null}
      </div>
      <div className="chat-inspector-footer"><Button tone="primary" onClick={() => void saveSettings()} disabled={busy || !settings.provider_id}>保存设置</Button></div>
    </aside>
  </div>
  {message ? <Toast tone="good" message={message} onClose={() => setMessage('')} /> : null}
  {error ? <Toast tone="bad" message={error} duration={4800} onClose={() => setError('')} /> : null}
  </div>;
}
