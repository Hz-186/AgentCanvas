import { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowUpRight, Bot, Brain, ChevronLeft, GitBranch, MessageSquareText, Plus, Save, Send, Sparkles, Square, Trash2, Wrench } from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import { agentApi, settingsApi, workflowApi, workspaceApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import { EditorialHeader, ResizableRail, paneStyle, storedWidth } from '../components/editorial';
import type { Agent, AgentDefinition, AgentTurn, ApprovalRequest, ChangeProposal, Conversation, Message, MessageSearchResult, ModelProvider, Run, RunEvent, WorkspacePack } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

const terminalTurnStatuses = new Set(['succeeded', 'failed', 'cancelled', 'waiting_human', 'paused']);
const activeTurnStatuses = new Set(['queued', 'retry_wait', 'running']);

function defaultDefinition(providerID = 0): AgentDefinition {
  return {
    provider_id: providerID,
    model: '',
    system_prompt: 'You are a capable, careful AI agent. Use tools when they materially help, and return a concise final answer.',
    mode: 'react',
    tool_pack_ids: [],
    tool_ids: [],
    skill_ids: [],
    skill_loading_mode: 'auto',
    knowledge_ids: [],
    knowledge_top_k: 5,
    knowledge_mode: 'hybrid',
    mcp_server_ids: [],
    callable_agent_ids: [],
    call_workflow_ids: [],
    allow_inline_agents: false,
    memory_enabled: true,
    reflection_enabled: true,
    max_iterations: 8,
    max_tool_calls: 16,
    max_execution_time_ms: 120000,
    max_tool_timeout_ms: 30000,
    max_tool_output_bytes: 524288,
    max_parallel_sub_agents: 4,
    max_workflow_call_depth: 3,
    output_mode: 'final_answer',
    workspace_enabled: false,
  };
}

function parseIDs(value: string): number[] {
  return Array.from(new Set(value.split(',').map((item) => Number(item.trim())).filter((item) => Number.isInteger(item) && item > 0)));
}

function formatIDs(value?: number[]): string { return (value ?? []).join(', '); }

function eventPayload(event: RunEvent): Record<string, unknown> {
  if (!event.payload_json) return {};
  try { return JSON.parse(event.payload_json) as Record<string, unknown>; } catch { return {}; }
}

function tracePresentation(event: RunEvent): { title: string; summary: string; detail: string } {
  const payload = eventPayload(event);
  const stepType = typeof payload.type === 'string' ? payload.type : '';
  const title = (event.event_type === 'agent_step' ? stepType : event.event_type).split('_').join(' ');
  const safeContentTypes = new Set(['plan', 'plan_revision', 'tool_result', 'reflection_recall', 'reflection', 'final_answer', 'error']);
  let summary = '';
  if (typeof payload.error === 'string') summary = payload.error;
  else if (typeof payload.tool_name === 'string') summary = payload.tool_name;
  else if (safeContentTypes.has(stepType) && typeof payload.content === 'string') summary = payload.content;
  else if (stepType === 'llm_response') summary = 'Model response received; hidden reasoning is not recorded.';
  else if (typeof payload.action === 'string') summary = payload.action;
  else if (typeof payload.stop_reason === 'string') summary = payload.stop_reason;
  else summary = `event #${event.id}`;
  const details = [];
  if (payload.token_count) details.push(`${payload.token_count} tok`);
  if (payload.latency_ms) details.push(`${payload.latency_ms} ms`);
  if (payload.compressed) details.push('compressed');
  return { title, summary, detail: details.join(' · ') };
}

function nextIdempotencyKey(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return `turn-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function JSONEditor({ label, value, onChange }: { label: string; value: unknown; onChange: (value: unknown) => void }) {
  const serialized = JSON.stringify(value ?? {}, null, 2);
  const [text, setText] = useState(serialized);
  useEffect(() => setText(serialized), [serialized]);
  return <Field label={label}><TextArea value={text} onChange={(event) => setText(event.target.value)} onBlur={() => {
    try { onChange(JSON.parse(text || '{}') as unknown); } catch { setText(serialized); }
  }} /></Field>;
}

export function ChatPage() {
  const navigate = useNavigate();
  const { agentId: routeAgentID, dialogId: routeDialogID, conversationId: routeConversationID } = useParams();
  const agentID = routeAgentID ? Number(routeAgentID) : undefined;
  const legacyDialogID = routeDialogID ? Number(routeDialogID) : undefined;
  const scoped = Boolean(agentID && !Number.isNaN(agentID));
  const isNewConversation = routeConversationID === 'new';
  const conversationID = routeConversationID && !isNewConversation ? Number(routeConversationID) : undefined;
  const [agents, setAgents] = useState<Agent[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [trace, setTrace] = useState<RunEvent[]>([]);
  const [turn, setTurn] = useState<AgentTurn | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [childRuns, setChildRuns] = useState<Run[]>([]);
  const [approvals, setApprovals] = useState<ApprovalRequest[]>([]);
  const [question, setQuestion] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState('');
  const [newProviderID, setNewProviderID] = useState(0);
  const [draft, setDraft] = useState<AgentDefinition>(defaultDefinition());
  const [draftName, setDraftName] = useState('');
  const [releases, setReleases] = useState<number[]>([]);
  const [workspacePacks, setWorkspacePacks] = useState<WorkspacePack[]>([]);
  const [changeProposals, setChangeProposals] = useState<ChangeProposal[]>([]);
  const [sessionQuery, setSessionQuery] = useState('');
  const [sessionResults, setSessionResults] = useState<MessageSearchResult[]>([]);
  const shellRef = useRef<HTMLDivElement | null>(null);
  const pollGeneration = useRef(0);
  const streamAbort = useRef<AbortController | null>(null);

  const currentAgent = useMemo(() => agents.find((item) => item.id === agentID), [agents, agentID]);

  async function reloadAgents() {
    const list = await agentApi.list();
    setAgents(list);
    return list;
  }

  useEffect(() => {
    let cancelled = false;
    Promise.all([agentApi.list(), settingsApi.providers.list()]).then(([agentList, providerList]) => {
      if (cancelled) return;
      setAgents(agentList);
      setProviders(providerList);
      setNewProviderID((value) => value || providerList[0]?.id || 0);
      if (legacyDialogID) {
        const migrated = agentList.find((item) => item.legacy_dialog_id === legacyDialogID);
        if (migrated) {
          navigate(routeConversationID ? `/app/agents/${migrated.id}/chat/${routeConversationID}` : `/app/agents/${migrated.id}/chat`, { replace: true });
        } else {
          setError('这个旧 Dialog 尚未迁移。请先运行 make backfill-agents，再重新打开该地址。');
        }
      }
    }).catch((cause) => !cancelled && setError(friendlyErrorMessage(cause, '加载 Agent 列表失败')));
    return () => { cancelled = true; pollGeneration.current += 1; streamAbort.current?.abort(); };
  }, [legacyDialogID, navigate, routeConversationID]);

  useEffect(() => {
    let cancelled = false;
    workspaceApi.list().then(async (workspaces) => {
      const packs = (await Promise.all(workspaces.filter((item) => item.status === 'active').map((item) => workspaceApi.listPacks(item.id)))).flat();
      if (!cancelled) setWorkspacePacks(packs.filter((item) => item.status === 'active'));
    }).catch(() => { if (!cancelled) setWorkspacePacks([]); });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!currentAgent) return;
    setDraftName(currentAgent.name);
    setDraft({ ...defaultDefinition(), ...currentAgent.definition });
    agentApi.listReleases(currentAgent.id).then((items) => setReleases(items.map((item) => item.version_no))).catch(() => setReleases([]));
  }, [currentAgent?.id, currentAgent?.updated_at]);

  useEffect(() => {
    if (!agentID) { setChangeProposals([]); return; }
    let cancelled = false;
    agentApi.listChangeProposals(agentID).then((items) => { if (!cancelled) setChangeProposals(items); }).catch(() => { if (!cancelled) setChangeProposals([]); });
    return () => { cancelled = true; };
  }, [agentID, turn?.status]);

  useEffect(() => {
    pollGeneration.current += 1;
    const generation = pollGeneration.current;
    streamAbort.current?.abort();
    setMessages([]);
    setTrace([]);
    setTurn(null);
    setRun(null);
    setChildRuns([]);
    setApprovals([]);
    setBusy(false);
    if (!agentID) { setConversations([]); return; }
    let cancelled = false;
    agentApi.listConversations(agentID).then((items) => {
      if (cancelled) return;
      setConversations(items);
      if (!routeConversationID && items[0]) navigate(`/app/agents/${agentID}/chat/${items[0].id}`, { replace: true });
    }).catch((cause) => !cancelled && setError(friendlyErrorMessage(cause, '加载会话失败')));
    if (conversationID) {
      agentApi.listMessages(agentID, conversationID).then((items) => !cancelled && setMessages(items)).catch((cause) => !cancelled && setError(friendlyErrorMessage(cause, '加载消息失败')));
      agentApi.getLatestTurn(agentID, conversationID).then((latest) => {
        if (cancelled || pollGeneration.current !== generation) return;
        setTurn(latest);
        if (latest.run_id) {
          Promise.all([workflowApi.getRun(latest.run_id), workflowApi.listRunEvents(latest.run_id), workflowApi.listChildRuns(latest.run_id)]).then(([latestRun, events, children]) => {
            if (cancelled || pollGeneration.current !== generation) return;
            setRun(latestRun); setTrace(events); setChildRuns(children);
          }).catch(() => undefined);
        }
        if (latest.run_id && (activeTurnStatuses.has(latest.status) || latest.status === 'waiting_human' || latest.status === 'paused')) {
          setBusy(activeTurnStatuses.has(latest.status));
          void monitorTurn(latest.id, latest.run_id, generation, conversationID);
        }
      }).catch(() => undefined);
    }
    return () => { cancelled = true; streamAbort.current?.abort(); };
  }, [agentID, conversationID, routeConversationID, navigate]);

  async function createAgent(event: FormEvent) {
    event.preventDefault();
    if (!newName.trim() || !newProviderID) { setError('请输入 Agent 名称并选择模型 Provider'); return; }
    setBusy(true); setError('');
    try {
      const item = await agentApi.create({ name: newName.trim(), definition: defaultDefinition(newProviderID) });
      await agentApi.publish(item.id);
      await reloadAgents();
      setCreateOpen(false); setNewName('');
      navigate(`/app/agents/${item.id}/chat/new`);
    } catch (cause) { setError(friendlyErrorMessage(cause, '创建 Agent 失败')); } finally { setBusy(false); }
  }

  async function removeAgent(id: number) {
    if (!window.confirm('确认归档这个 Agent 吗？已有 Release 与会话仍保留审计记录。')) return;
    try { await agentApi.remove(id); await reloadAgents(); if (agentID === id) navigate('/app/agents'); }
    catch (cause) { setError(friendlyErrorMessage(cause, '归档 Agent 失败')); }
  }

  async function saveDraft(publish: boolean) {
    if (!agentID) return;
    setBusy(true); setError('');
    try {
      await agentApi.update(agentID, { name: draftName.trim(), definition: draft });
      const validation = await agentApi.validate(agentID);
      if (!validation.valid) throw new Error(validation.errors.join('；'));
      if (publish) await agentApi.publish(agentID);
      await reloadAgents();
    } catch (cause) { setError(friendlyErrorMessage(cause, publish ? '发布 Agent 失败' : '保存草稿失败')); }
    finally { setBusy(false); }
  }

  async function createConversation(): Promise<Conversation | null> {
    if (!agentID) return null;
    try {
      const item = await agentApi.createConversation(agentID);
      setConversations((current) => [item, ...current]);
      navigate(`/app/agents/${agentID}/chat/${item.id}`, { replace: true });
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

  async function searchSessions() {
    if (!agentID || !sessionQuery.trim()) { setSessionResults([]); return; }
    try { setSessionResults(await agentApi.searchSessions(agentID, sessionQuery.trim())); }
    catch (cause) { setError(friendlyErrorMessage(cause, '搜索历史会话失败')); }
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
        onMessage: (message) => {
          if (pollGeneration.current !== generation || message.event === 'run_status' || message.event === 'error') return;
          try {
            const event = JSON.parse(message.data) as RunEvent;
            if (!event.id) return;
            lastEventId = message.id ?? String(event.id);
            setTrace((current) => current.some((item) => item.id === event.id) ? current : [...current, event]);
          } catch { /* 非 RunEvent 状态帧由随后的 Turn 查询处理。 */ }
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
            const pending = await workflowApi.listApprovalRequests('pending');
            setApprovals(pending.filter((item) => item.run_id === runID));
          } else {
            setApprovals([]);
          }
          if (agentID) setMessages(await agentApi.listMessages(agentID, activeConversationID));
          const [latestRun, children] = await Promise.all([workflowApi.getRun(runID), workflowApi.listChildRuns(runID)]);
          setRun(latestRun); setChildRuns(children);
          setBusy(false);
          if (streamAbort.current === controller) streamAbort.current = null;
          return;
        }
      } catch (cause) {
        streamError = cause instanceof Error ? cause : new Error(String(cause));
      }
      reconnects += 1;
      if (streamError && reconnects === 3) setError('Agent 事件流暂时中断，正在从最后事件自动重连。');
      await new Promise((resolve) => window.setTimeout(resolve, Math.min(250 * reconnects, 2000)));
    }
  }

  async function send(event: FormEvent) {
    event.preventDefault();
    const content = question.trim();
    if (!content || !agentID || busy) return;
    let activeConversationID = conversationID;
    if (!activeConversationID) { const created = await createConversation(); activeConversationID = created?.id; }
    if (!activeConversationID) return;
    setBusy(true); setError(''); setQuestion(''); setTrace([]); setApprovals([]);
    setMessages((current) => [...current, { id: -Date.now(), owner_id: 0, conversation_id: activeConversationID!, role: 'user', content, content_type: 'text', token_count: 0, created_at: new Date().toISOString() }]);
    try {
      const accepted = await agentApi.startTurn(agentID, activeConversationID, content, nextIdempotencyKey());
      setTurn(accepted.turn); setRun(accepted.run); setChildRuns([]);
      const generation = pollGeneration.current + 1; pollGeneration.current = generation;
      await monitorTurn(accepted.turn.id, accepted.run.id, generation, activeConversationID);
    } catch (cause) { setBusy(false); setError(friendlyErrorMessage(cause, '启动 Agent Run 失败')); }
  }

  async function stopRun() {
    if (!turn?.run_id) return;
    try {
      const cancelledRun = await workflowApi.cancelRun(turn.run_id);
      streamAbort.current?.abort();
      const nextTurn = await agentApi.getTurn(turn.id);
      setTurn(nextTurn);
      setRun(cancelledRun);
      if (agentID) setMessages(await agentApi.listMessages(agentID, turn.conversation_id));
      setBusy(false);
    }
    catch (cause) { setError(friendlyErrorMessage(cause, '停止 Run 失败')); }
  }

  async function decideApproval(item: ApprovalRequest, approved: boolean) {
    if (!item.run_id) return;
    setBusy(true); setError('');
    try {
      if (approved) await workflowApi.approveRequest(item.id); else await workflowApi.rejectRequest(item.id, 'Rejected from Agent Chat');
      const resumedRun = await workflowApi.resumeRun(item.run_id);
      setRun(resumedRun);
      setApprovals([]);
      if (!turn) return;
      const nextTurn = await agentApi.getTurn(turn.id);
      setTurn(nextTurn);
      const generation = pollGeneration.current + 1; pollGeneration.current = generation;
      await monitorTurn(nextTurn.id, item.run_id, generation, nextTurn.conversation_id);
    } catch (cause) { setBusy(false); setError(friendlyErrorMessage(cause, '恢复 Agent Run 失败')); }
  }

  async function resumePausedRun() {
    if (!turn?.run_id) return;
    setBusy(true); setError('');
    try {
      const resumedRun = await workflowApi.resumeRun(turn.run_id);
      setRun(resumedRun);
      const nextTurn = await agentApi.getTurn(turn.id);
      setTurn(nextTurn);
      const generation = pollGeneration.current + 1; pollGeneration.current = generation;
      await monitorTurn(nextTurn.id, turn.run_id, generation, nextTurn.conversation_id);
    } catch (cause) { setBusy(false); setError(friendlyErrorMessage(cause, '继续 Agent Run 失败')); }
  }

  async function decideChangeProposal(item: ChangeProposal, approved: boolean) {
    try {
      const updated = approved ? await agentApi.approveChangeProposal(item.id) : await agentApi.rejectChangeProposal(item.id);
      setChangeProposals((current) => current.map((candidate) => candidate.id === updated.id ? updated : candidate));
    } catch (cause) { setError(friendlyErrorMessage(cause, approved ? '应用改进提案失败' : '拒绝改进提案失败')); }
  }

  const updateDraft = <K extends keyof AgentDefinition>(key: K, value: AgentDefinition[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const turnBlocksNewMessage = turn?.status === 'waiting_human' || turn?.status === 'paused';

  if (!scoped) {
    return <div className="page dialogue-page">
      <EditorialHeader word="Agent" script="Chat" kicker="INDEPENDENT AGENTS / RELEASE-PINNED" description="每个会话固定一个不可变 Agent Release；消息直接启动 Agent Runtime，不需要 Workflow 容器。" action={<Button tone="primary" onClick={() => setCreateOpen(true)}><Plus size={17} />New Agent</Button>} />
      <div className="stack">
        {agents.length === 0 ? <EmptyState icon={<Bot size={24} />} title="还没有独立 Agent" description="创建、发布后即可直接多轮聊天，并按需使用工具、Skill、知识、记忆与子 Agent。" action={<Button tone="primary" onClick={() => setCreateOpen(true)}>创建 Agent</Button>} /> :
          <div className="workflow-library-list dialog-library-list">{agents.map((item) => <article className="workflow-library-item dialog-library-item" key={item.id}>
            <div className="workflow-miniature dialog-miniature"><span><Bot size={16} /></span><i /><span><Wrench size={16} /></span><i /><span className="workflow-miniature-end"><Sparkles size={16} /></span></div>
            <div className="workflow-library-copy"><div className="card-title"><h3 className="truncate">{item.name}</h3><StatusBadge tone={item.status === 'active' ? 'good' : 'neutral'}>{item.status}</StatusBadge></div><p className="muted clamp-2">{item.description || item.definition.system_prompt}</p><div className="meta-row"><span>RELEASE {item.current_release_id ?? 'DRAFT'}</span><span>{item.definition.mode}</span><span>{formatDate(item.updated_at)}</span></div></div>
            <div className="workflow-library-actions"><Button tone="primary" onClick={() => navigate(`/app/agents/${item.id}/chat`)}>Open Agent<ArrowUpRight size={16} /></Button><IconButton label="归档 Agent" className="icon-btn-danger" onClick={() => void removeAgent(item.id)}><Trash2 size={16} /></IconButton></div>
          </article>)}</div>}
      </div>
      <Modal open={createOpen} title="Create independent Agent" onClose={() => setCreateOpen(false)}><form className="stack" onSubmit={(event) => void createAgent(event)}><Field label="Name"><TextInput value={newName} onChange={(event) => setNewName(event.target.value)} /></Field><Field label="Model Provider"><Select value={newProviderID} onChange={(event) => setNewProviderID(Number(event.target.value))}><option value={0}>Select provider</option>{providers.map((provider) => <option value={provider.id} key={provider.id}>{provider.name}</option>)}</Select></Field><Button tone="primary" type="submit" disabled={busy}>Create & publish</Button></form></Modal>
      {error ? <Toast tone="bad" message={error} /> : null}
    </div>;
  }

  return <div className="page chat-page-scoped"><div ref={shellRef} className="chat-shell" style={paneStyle({ '--dialog-nav-width': `${storedWidth('agentcanvas-agent-navigator-width', 270)}px`, '--dialog-inspector-width': `${storedWidth('agentcanvas-agent-inspector-width', 380)}px` })}>
    <aside className="chat-sidebar glass"><div className="chat-sidebar-head"><button type="button" className="chat-back" onClick={() => navigate('/app/agents')}><ChevronLeft size={15} />全部 Agent</button><h2 className="truncate">{currentAgent?.name ?? 'Agent'}</h2><div className="meta-row"><StatusBadge tone={currentAgent?.status === 'active' ? 'good' : 'neutral'}>{currentAgent?.status ?? 'loading'}</StatusBadge><span>release {currentAgent?.current_release_id ?? '—'}</span></div><Button tone="primary" onClick={() => navigate(`/app/agents/${agentID}/chat/new`)}><Plus size={15} />新建会话</Button><div className="meta-row"><TextInput value={sessionQuery} onChange={(event) => setSessionQuery(event.target.value)} placeholder="搜索历史会话" onKeyDown={(event) => { if (event.key === 'Enter') void searchSessions(); }} /><Button onClick={() => void searchSessions()}>Search</Button></div>{sessionResults.map((item) => <button type="button" className="chat-conversation-item" key={item.message_id} onClick={() => navigate(`/app/agents/${agentID}/chat/${item.conversation_id}`)}><span className="truncate">{item.content}</span><span className="chat-conversation-time">{item.role} · {formatDate(item.created_at)}</span></button>)}</div>
      <div className="chat-conversation-list">{isNewConversation ? <div className="chat-conversation-item active"><span>新会话…</span></div> : null}{conversations.map((item) => <div className={`chat-conversation-item ${item.id === conversationID ? 'active' : ''}`} key={item.id}><button type="button" onClick={() => navigate(`/app/agents/${agentID}/chat/${item.id}`)}><span className="truncate">{item.title || '未命名会话'}</span><span className="chat-conversation-time">R{item.agent_release_id} · {formatDate(item.last_message_at ?? item.updated_at)}</span></button><IconButton label="删除会话" className="chat-delete" onClick={() => void removeConversation(item.id)}><Trash2 size={14} /></IconButton></div>)}</div>
    </aside>
    <ResizableRail containerRef={shellRef} variable="--dialog-nav-width" storageKey="agentcanvas-agent-navigator-width" side="left" min={220} max={380} collapsed={112} defaultWidth={270} label="调整会话导航宽度" />
    <section className="chat-main surface"><div className="chat-session-heading"><span>AGENT RUN</span><strong>{currentAgent?.name}</strong>{conversationID ? <div className="meta-row"><button className="chat-back" type="button" onClick={() => void forkConversation(false)}><GitBranch size={13} />Fork</button><button className="chat-back" type="button" onClick={() => void forkConversation(true)}>Upgrade release</button></div> : null}</div>
      <div className="message-list">{messages.length === 0 ? <EmptyState icon={<MessageSquareText size={24} />} title="开始与完整 Agent 对话" description="Agent 可以规划、调用工具、检索知识、使用记忆与 Reflection，并委派给白名单子 Agent。" /> : messages.filter((item) => item.role === 'user' || item.role === 'assistant').map((item) => <div className={`message ${item.role}`} key={item.id}>{item.content}</div>)}
        {run ? <div className="meta-row"><StatusBadge tone={run.status === 'succeeded' ? 'good' : run.status === 'failed' ? 'bad' : 'info'}>{run.status}</StatusBadge><span>{run.total_tokens ?? 0} tok</span><span>{run.latency_ms ?? 0} ms</span><span>{run.run_kind ?? 'agent'}</span></div> : null}
        {trace.length > 0 ? <div className="stack"><p className="eyebrow">RUN TRACE · PLAN / ACTION / RESULT</p>{trace.map((item) => { const view = tracePresentation(item); return <article className="card" key={item.id}><div className="card-title"><strong>{view.title}</strong><StatusBadge tone={item.event_type.includes('failed') ? 'bad' : item.event_type === 'agent_step' ? 'info' : 'neutral'}>{item.node_type || 'runtime'}</StatusBadge></div><p className="muted clamp-2">{view.summary}</p>{view.detail ? <div className="meta-row"><span>{view.detail}</span></div> : null}</article>; })}</div> : null}
        {childRuns.length > 0 ? <div className="stack"><p className="eyebrow">SUBAGENT TREE</p>{childRuns.map((child) => <article className="card" key={child.id}><div className="card-title"><strong>Agent {child.agent_id ?? 'inline'} · Run {child.id}</strong><StatusBadge tone={child.status === 'succeeded' ? 'good' : child.status === 'failed' ? 'bad' : 'info'}>{child.status}</StatusBadge></div><div className="meta-row"><span>{child.run_kind}</span><span>{child.total_tokens} tok</span><span>{child.latency_ms} ms</span></div></article>)}</div> : null}
        {approvals.map((item) => <article className="card" key={item.id}><div className="card-title"><strong>Approval required · {item.tool_name}</strong><StatusBadge tone="warn">{item.risk_level}</StatusBadge></div><p className="muted">{item.reason}</p><div className="meta-row"><Button tone="primary" onClick={() => void decideApproval(item, true)}>Approve & resume</Button><Button tone="danger" onClick={() => void decideApproval(item, false)}>Reject</Button></div></article>)}
      </div>
      <form className="chat-composer" onSubmit={(event) => void send(event)}><TextArea value={question} onChange={(event) => setQuestion(event.target.value)} placeholder={currentAgent?.current_release_id ? turnBlocksNewMessage ? '请先处理当前暂停或审批中的 Run' : '交给 Agent 一个任务…' : '请先发布 Agent Release'} disabled={busy || turnBlocksNewMessage || !currentAgent?.current_release_id} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }} /><div className="chat-composer-actions"><span className="muted">{turn ? `${turn.status} · run ${turn.run_id}` : `Release ${currentAgent?.current_release_id ?? 'not published'}`}</span>{busy ? <Button type="button" tone="danger" onClick={() => void stopRun()}><Square size={15} />Stop</Button> : turn?.status === 'paused' ? <Button type="button" tone="primary" onClick={() => void resumePausedRun()}>Resume</Button> : turn?.status === 'waiting_human' ? <Button type="button" disabled>Awaiting approval</Button> : <Button tone="primary" type="submit" disabled={!question.trim()}><Send size={16} />Run</Button>}</div></form>
    </section>
    <ResizableRail containerRef={shellRef} variable="--dialog-inspector-width" storageKey="agentcanvas-agent-inspector-width" side="right" min={320} max={560} collapsed={140} defaultWidth={380} label="调整 Agent Inspector 宽度" />
    <aside className="chat-inspector glass"><div className="chat-inspector-head"><div><p className="eyebrow">AGENT INSPECTOR</p><h3>Capabilities</h3></div><div className="meta-row"><Button onClick={() => void saveDraft(false)} disabled={busy}><Save size={15} />Save</Button><Button tone="primary" onClick={() => void saveDraft(true)} disabled={busy}><Sparkles size={15} />Publish</Button></div></div>
      <div className="chat-inspector-body stack"><Field label="Identity"><TextInput value={draftName} onChange={(event) => setDraftName(event.target.value)} /></Field><Field label="Role"><TextInput value={draft.role ?? ''} onChange={(event) => updateDraft('role', event.target.value)} /></Field><Field label="Goal"><TextArea value={draft.goal ?? ''} onChange={(event) => updateDraft('goal', event.target.value)} /></Field><Field label="Background"><TextArea value={draft.backstory ?? ''} onChange={(event) => updateDraft('backstory', event.target.value)} /></Field><Field label="Model Provider"><Select value={draft.provider_id} onChange={(event) => updateDraft('provider_id', Number(event.target.value))}>{providers.map((provider) => <option value={provider.id} key={provider.id}>{provider.name}</option>)}</Select></Field><Field label="Model"><TextInput value={draft.model ?? ''} onChange={(event) => updateDraft('model', event.target.value)} placeholder="留空使用 Provider 默认模型" /></Field><Field label="Mode"><Select value={draft.mode} onChange={(event) => updateDraft('mode', event.target.value as AgentDefinition['mode'])}><option value="react">ReAct</option><option value="plan_execute">Plan Guided</option></Select></Field><Field label="Temperature"><TextInput type="number" min={0} max={2} step={0.1} value={draft.temperature ?? ''} onChange={(event) => updateDraft('temperature', event.target.value === '' ? undefined : Number(event.target.value))} /></Field><Field label="System Prompt"><TextArea value={draft.system_prompt} onChange={(event) => updateDraft('system_prompt', event.target.value)} /></Field>
        <div className="card"><div className="card-title"><strong><Wrench size={15} /> Tools & Knowledge</strong></div><Field label="Tool Pack IDs"><TextInput value={formatIDs(draft.tool_pack_ids)} onChange={(event) => updateDraft('tool_pack_ids', parseIDs(event.target.value))} /></Field><Field label="Tool IDs"><TextInput value={formatIDs(draft.tool_ids)} onChange={(event) => updateDraft('tool_ids', parseIDs(event.target.value))} /></Field><Field label="Skill IDs"><TextInput value={formatIDs(draft.skill_ids)} onChange={(event) => updateDraft('skill_ids', parseIDs(event.target.value))} /></Field><Field label="Skill loading"><Select value={draft.skill_loading_mode ?? 'auto'} onChange={(event) => updateDraft('skill_loading_mode', event.target.value as AgentDefinition['skill_loading_mode'])}><option value="auto">Auto</option><option value="metadata_only">Metadata only</option><option value="search">Search on demand</option></Select></Field><Field label="Knowledge IDs"><TextInput value={formatIDs(draft.knowledge_ids)} onChange={(event) => updateDraft('knowledge_ids', parseIDs(event.target.value))} /></Field><Field label="Knowledge mode"><Select value={draft.knowledge_mode ?? 'hybrid'} onChange={(event) => updateDraft('knowledge_mode', event.target.value as AgentDefinition['knowledge_mode'])}><option value="hybrid">Hybrid</option><option value="vector">Vector</option><option value="keyword">Keyword</option></Select></Field><Field label="Knowledge Top K"><TextInput type="number" min={1} max={20} value={draft.knowledge_top_k ?? 5} onChange={(event) => updateDraft('knowledge_top_k', Number(event.target.value))} /></Field><Field label="MCP Server IDs"><TextInput value={formatIDs(draft.mcp_server_ids)} onChange={(event) => updateDraft('mcp_server_ids', parseIDs(event.target.value))} /></Field></div>
        <div className="card"><div className="card-title"><strong><Bot size={15} /> Delegation</strong></div><Field label="Callable Agent IDs"><TextInput value={formatIDs(draft.callable_agent_ids)} onChange={(event) => updateDraft('callable_agent_ids', parseIDs(event.target.value))} /></Field><Field label="Callable Workflow IDs"><TextInput value={formatIDs(draft.call_workflow_ids)} onChange={(event) => updateDraft('call_workflow_ids', parseIDs(event.target.value))} /></Field><label className="toggle-row"><input type="checkbox" checked={Boolean(draft.allow_inline_agents)} onChange={(event) => updateDraft('allow_inline_agents', event.target.checked)} />允许临时 Subagent</label></div>
        <div className="card"><div className="card-title"><strong><Brain size={15} /> Memory & Reflection</strong></div><label className="toggle-row"><input type="checkbox" checked={Boolean(draft.memory_enabled)} onChange={(event) => updateDraft('memory_enabled', event.target.checked)} />启用记忆工具与 Working Memory</label><label className="toggle-row"><input type="checkbox" checked={Boolean(draft.reflection_enabled)} onChange={(event) => updateDraft('reflection_enabled', event.target.checked)} />启用 Reflection Recall / Review</label><p className="muted">后台 Review 只读取消息、计划、工具动作与结果，不保存隐藏思维链。</p></div>
        <div className="card"><div className="card-title"><strong>Self-Improvement Proposals</strong><StatusBadge tone="info">{changeProposals.filter((item) => item.status === 'pending').length}</StatusBadge></div>{changeProposals.length === 0 ? <p className="muted">尚无提案。完成会话后后台 Reviewer 会生成可审计建议。</p> : changeProposals.slice(0, 8).map((item) => <article className="stack" key={item.id}><div className="card-title"><strong>{item.kind} · {item.title}</strong><StatusBadge tone={item.status === 'pending' ? 'warn' : item.status === 'applied' ? 'good' : 'neutral'}>{item.status}</StatusBadge></div><p className="muted clamp-2">{item.content}</p><div className="meta-row"><span>confidence {Math.round(item.confidence * 100)}%</span><span>run {item.run_id}</span></div>{item.status === 'pending' ? <div className="meta-row"><Button tone="primary" onClick={() => void decideChangeProposal(item, true)}>Approve & apply</Button><Button tone="danger" onClick={() => void decideChangeProposal(item, false)}>Reject</Button></div> : null}</article>)}</div>
        <div className="card"><div className="card-title"><strong>Policies, Rules & Output</strong></div><JSONEditor label="Tool Policy" value={draft.tool_policy_json} onChange={(value) => updateDraft('tool_policy_json', value)} /><JSONEditor label="Memory Policy" value={draft.memory_policy_json} onChange={(value) => updateDraft('memory_policy_json', value)} /><JSONEditor label="Reflection Policy" value={draft.reflection_policy_json} onChange={(value) => updateDraft('reflection_policy_json', value)} /><JSONEditor label="Context Policy" value={draft.context_policy_json} onChange={(value) => updateDraft('context_policy_json', value)} /><JSONEditor label="Rules" value={draft.rules_json ?? []} onChange={(value) => updateDraft('rules_json', value)} /><Field label="Output mode"><Select value={draft.output_mode ?? 'final_answer'} onChange={(event) => updateDraft('output_mode', event.target.value as AgentDefinition['output_mode'])}><option value="final_answer">Final answer</option><option value="full">Full trace summary</option></Select></Field><label className="toggle-row"><input type="checkbox" checked={Boolean(draft.return_intermediate_steps)} onChange={(event) => updateDraft('return_intermediate_steps', event.target.checked)} />在输出中返回中间步骤摘要</label><JSONEditor label="Output Schema" value={draft.output_schema_json} onChange={(value) => updateDraft('output_schema_json', value)} /></div>
        <div className="card"><div className="card-title"><strong>Lifecycle Workflows</strong></div><Field label="Pre-turn Workflow ID"><TextInput type="number" value={draft.pre_turn_workflow_id ?? ''} onChange={(event) => updateDraft('pre_turn_workflow_id', event.target.value ? Number(event.target.value) : null)} /></Field><Field label="Pre-turn Version ID"><TextInput type="number" value={draft.pre_turn_workflow_version_id ?? ''} onChange={(event) => updateDraft('pre_turn_workflow_version_id', event.target.value ? Number(event.target.value) : null)} /></Field><Field label="Post-turn Workflow ID"><TextInput type="number" value={draft.post_turn_workflow_id ?? ''} onChange={(event) => updateDraft('post_turn_workflow_id', event.target.value ? Number(event.target.value) : null)} /></Field><Field label="Post-turn Version ID"><TextInput type="number" value={draft.post_turn_workflow_version_id ?? ''} onChange={(event) => updateDraft('post_turn_workflow_version_id', event.target.value ? Number(event.target.value) : null)} /></Field><p className="muted">生命周期 Workflow 必须固定已发布 Version，且仅允许无 Tool/Memory Write/Sandbox/委派副作用的安全节点。</p></div>
        <div className="card"><div className="card-title"><strong>Permissions & Limits</strong></div><Field label="Max iterations"><TextInput type="number" min={1} max={50} value={draft.max_iterations ?? 8} onChange={(event) => updateDraft('max_iterations', Number(event.target.value))} /></Field><Field label="Max tool calls"><TextInput type="number" min={1} max={100} value={draft.max_tool_calls ?? 16} onChange={(event) => updateDraft('max_tool_calls', Number(event.target.value))} /></Field><Field label="Turn timeout ms"><TextInput type="number" min={1} max={600000} value={draft.max_execution_time_ms ?? 120000} onChange={(event) => updateDraft('max_execution_time_ms', Number(event.target.value))} /></Field><Field label="Tool timeout ms"><TextInput type="number" min={1} max={600000} value={draft.max_tool_timeout_ms ?? 30000} onChange={(event) => updateDraft('max_tool_timeout_ms', Number(event.target.value))} /></Field><Field label="Tool output bytes"><TextInput type="number" min={1024} max={2097152} value={draft.max_tool_output_bytes ?? 524288} onChange={(event) => updateDraft('max_tool_output_bytes', Number(event.target.value))} /></Field><Field label="Parallel subagents"><TextInput type="number" min={1} max={64} value={draft.max_parallel_sub_agents ?? 4} onChange={(event) => updateDraft('max_parallel_sub_agents', Number(event.target.value))} /></Field><Field label="Workflow call depth"><TextInput type="number" min={1} max={5} value={draft.max_workflow_call_depth ?? 3} onChange={(event) => updateDraft('max_workflow_call_depth', Number(event.target.value))} /></Field><Field label="Context window tokens"><TextInput type="number" min={0} value={draft.context_window_tokens ?? 0} onChange={(event) => updateDraft('context_window_tokens', Number(event.target.value))} /></Field><Field label="Reserved output tokens"><TextInput type="number" min={0} value={draft.reserved_output_tokens ?? 0} onChange={(event) => updateDraft('reserved_output_tokens', Number(event.target.value))} /></Field><Field label="Rule token budget"><TextInput type="number" min={0} value={draft.max_rule_tokens ?? 0} onChange={(event) => updateDraft('max_rule_tokens', Number(event.target.value))} /></Field><label className="toggle-row"><input type="checkbox" checked={Boolean(draft.workspace_enabled)} disabled={workspacePacks.length === 0} onChange={(event) => { updateDraft('workspace_enabled', event.target.checked); if (event.target.checked && !draft.workspace_pack_id) updateDraft('workspace_pack_id', workspacePacks[0]?.id ?? null); }} />启用 Workspace Pack（文件、Shell、Git）</label>{draft.workspace_enabled ? <Field label="Workspace Pack"><Select value={draft.workspace_pack_id ?? ''} onChange={(event) => updateDraft('workspace_pack_id', Number(event.target.value))}><option value="">选择 Pack</option>{workspacePacks.map((pack) => <option value={pack.id} key={pack.id}>{pack.name} · {pack.allowed_paths.join(', ') || '.'}</option>)}</Select></Field> : null}{draft.workspace_enabled && draft.workspace_pack_id ? <p className="muted">命令白名单：{workspacePacks.find((item) => item.id === draft.workspace_pack_id)?.command_allowlist.join(', ') || '无'}；写文件、命令和 Git Commit 始终需要审批。</p> : workspacePacks.length === 0 ? <p className="muted">服务器未启用 Workspace Runtime，或尚未创建可用 Pack。</p> : null}</div>
        <div className="card"><div className="card-title"><strong>Releases</strong><StatusBadge tone="info">{releases.length}</StatusBadge></div><p className="muted">已发布版本：{releases.length ? releases.map((item) => `v${item}`).join(', ') : '暂无'}。现有 Conversation 始终固定原 Release。</p></div>
      </div>
    </aside>
  </div>{error ? <Toast tone="bad" message={error} /> : null}</div>;
}
