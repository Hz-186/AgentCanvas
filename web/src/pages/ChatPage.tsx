import { FormEvent, useEffect, useRef, useState } from 'react';
import { ArrowUpRight, ChevronLeft, Database, MessageSquareText, Plus, Save, Send, Trash2 } from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import { chatApi, conversationApi, dialogApi, resourceSummaryApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import { EditorialHeader, ResizableRail, paneStyle, storedWidth } from '../components/editorial';
import type { Conversation, Dialog, KnowledgeBase, Message, MessageReference, ModelProvider } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

interface ChatLine {
  role: 'user' | 'assistant';
  content: string;
}

export function ChatPage() {
  const navigate = useNavigate();
  const { dialogId: routeDialogId, conversationId: routeConversationId } = useParams();
  const dialogId = routeDialogId ? Number(routeDialogId) : undefined;
  const isDialogScoped = Boolean(dialogId && !Number.isNaN(dialogId));
  const isDetail = Boolean(routeConversationId);
  const isNewConversation = routeConversationId === 'new';
  const routeConversationIdNum = routeConversationId && !isNewConversation ? Number(routeConversationId) : undefined;
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [dialogs, setDialogs] = useState<Dialog[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [providerId, setProviderId] = useState(0);
  const [kbId, setKbId] = useState(0);
  const [conversationId, setConversationId] = useState<number | undefined>();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [dialogName, setDialogName] = useState('');
  const [question, setQuestion] = useState('');
  const [lines, setLines] = useState<ChatLine[]>([]);
  const [references, setReferences] = useState<MessageReference[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState('');
  const [model, setModel] = useState('');
  const [retrievalMode, setRetrievalMode] = useState('hybrid');
  const [topK, setTopK] = useState(8);
  const [historyLimit, setHistoryLimit] = useState(10);
  const [systemPrompt, setSystemPrompt] = useState('');
  const [prologue, setPrologue] = useState('');
  const chatLayoutRef = useRef<HTMLDivElement | null>(null);
  // 进行中的 SSE 流控制器：组件卸载或重新发起时取消，避免内存泄漏与“卸载后 setState”。
  const askAbortRef = useRef<AbortController | null>(null);
  // 正在流式输出的会话 id：路由 effect 据此跳过对当前流会话的重载，避免覆盖正在生成的内容。
  const streamingConversationRef = useRef<number | undefined>(undefined);

  async function loadBase() {
    const [providerResp, kbSummary, dialogSummary, selectedDialog] = await Promise.all([
      settingsApi.providers.list(),
      resourceSummaryApi.list('knowledge-bases', { limit: 100 }),
      resourceSummaryApi.list('dialogs', { limit: 100 }),
      dialogId ? dialogApi.get(dialogId) : Promise.resolve(null),
    ]);
    const kbResp = kbSummary.items.map((item) => ({ id: item.id, name: item.name } as KnowledgeBase));
    const dialogResp = dialogSummary.items.map((item) => ({
      id: item.id,
      name: item.name,
      description: item.description ?? '',
      status: item.status ?? 1,
      updated_at: item.updated_at,
    } as Dialog));
    if (selectedDialog) {
      const index = dialogResp.findIndex((item) => item.id === selectedDialog.id);
      if (index >= 0) dialogResp[index] = selectedDialog;
      else dialogResp.unshift(selectedDialog);
    }
    setProviders(providerResp);
    setKnowledgeBases(kbResp);
    setDialogs(dialogResp);
    setProviderId((current) => current || providerResp[0]?.id || 0);
    setKbId((current) => current || kbResp[0]?.id || 0);
  }

  async function loadConversations(currentDialogId: number) {
    setConversations(await conversationApi.list(currentDialogId));
  }

  useEffect(() => {
    void loadBase().catch((err) => setError(friendlyErrorMessage(err, '加载聊天配置失败')));
  }, [dialogId]);

  // 组件卸载时取消进行中的流，避免内存泄漏。
  useEffect(() => () => askAbortRef.current?.abort(), []);

  useEffect(() => {
    // 当前路由会话正是流式输出中的会话：保留正在生成的内容，不重置、不重载。
    if (streamingConversationRef.current && streamingConversationRef.current === routeConversationIdNum) {
      return;
    }
    let cancelled = false;
    setConversationId(undefined);
    setLines([]);
    setReferences([]);
    if (!isDialogScoped || !dialogId) {
      setConversations([]);
      return () => {
        cancelled = true;
      };
    }
    conversationApi
      .list(dialogId)
      .then((list) => {
        if (cancelled) return;
        setConversations(list);
        // 进入 Dialog 但未指定会话时，自动打开最近一个会话（ChatGPT 式左列右窗体验）。
        if (!isDetail && !isNewConversation && list.length > 0) {
          navigate(`/app/dialogs/${dialogId}/chat/${list[0].id}`, { replace: true });
          return;
        }
        if (isDetail && !isNewConversation && routeConversationIdNum && !Number.isNaN(routeConversationIdNum)) {
          void openConversation(dialogId, routeConversationIdNum, () => cancelled);
        }
      })
      .catch((err) => {
        if (!cancelled) setError(friendlyErrorMessage(err, '加载会话列表失败'));
      });
    return () => {
      cancelled = true;
    };
  }, [dialogId, isDetail, isDialogScoped, isNewConversation, routeConversationIdNum, navigate]);

  async function openConversation(currentDialogId: number, id: number, isCancelled: () => boolean = () => false) {
    setConversationId(id);
    const messages = await conversationApi.listMessages(currentDialogId, id);
    // 切换会话时旧请求可能晚返回，确认仍是当前会话再写入，避免串台。
    if (isCancelled()) return;
    setLines(messages.map((msg: Message) => ({ role: msg.role === 'user' ? 'user' : 'assistant', content: msg.content })));
  }

  async function createDialog(event: FormEvent) {
    event.preventDefault();
    const name = dialogName.trim();
    if (!name) {
      setError('请输入 Dialog 名称');
      return;
    }
    setError('');
    try {
      const item = await dialogApi.create({ name });
      setDialogName('');
      setDialogOpen(false);
      setDialogs((current) => [item, ...current]);
      navigate(`/app/dialogs/${item.id}/chat`);
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Dialog 失败'));
    }
  }

  async function removeDialog(id: number) {
    if (!window.confirm('确认删除这个 Dialog 及其会话吗？')) return;
    try {
      await dialogApi.remove(id);
      setDialogs((current) => current.filter((item) => item.id !== id));
      if (dialogId === id) navigate('/app/dialogs', { replace: true });
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 Dialog 失败'));
    }
  }

  async function removeConversation(id: number) {
    if (!dialogId || !window.confirm('确认删除这个会话吗？')) return;
    try {
      await conversationApi.remove(dialogId, id);
      const remaining = conversations.filter((item) => item.id !== id);
      setConversations(remaining);
      if (conversationId === id) navigate(remaining[0] ? `/app/dialogs/${dialogId}/chat/${remaining[0].id}` : `/app/dialogs/${dialogId}/chat/new`, { replace: true });
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除会话失败'));
    }
  }

  async function saveDialogSettings() {
    if (!dialogId) return;
    try {
      const updated = await dialogApi.update(dialogId, { provider_id: providerId, model, kb_ids: kbId ? [kbId] : [], retrieval_mode: retrievalMode, top_k: topK, history_round_limit: historyLimit, system_prompt: systemPrompt, prologue });
      setDialogs((current) => current.map((item) => item.id === updated.id ? updated : item));
    } catch (err) {
      setError(friendlyErrorMessage(err, '保存 Dialog 设置失败'));
    }
  }

  async function ask(event: FormEvent) {
    event.preventDefault();
    if (!question.trim() || !providerId || !kbId || !dialogId) {
      setError('请选择 Dialog、Provider、知识库并输入问题');
      return;
    }
    if (streaming) return;
    const currentQuestion = question.trim();
    // 取消可能仍在进行的上一次流，避免并发写入同一消息列表。
    askAbortRef.current?.abort();
    const controller = new AbortController();
    askAbortRef.current = controller;
    streamingConversationRef.current = conversationId;
    setLines((current) => [...current, { role: 'user', content: currentQuestion }, { role: 'assistant', content: '' }]);
    setQuestion('');
    setReferences([]);
    setStreaming(true);
    setError('');
    try {
      await chatApi.stream(
        dialogId,
        { provider_id: providerId, kb_ids: [kbId], question: currentQuestion, conversation_id: conversationId, top_k: 8 },
        {
          signal: controller.signal,
          onMessage: (msg) => {
            if (controller.signal.aborted) return;
            const data = (() => {
              try {
                return JSON.parse(msg.data) as unknown;
              } catch {
                return msg.data;
              }
            })();
            if (msg.event === 'conversation') {
              const conv = data as Conversation;
              setConversationId(conv.id);
              streamingConversationRef.current = conv.id;
              if (isNewConversation) navigate(`/app/dialogs/${dialogId}/chat/${conv.id}`, { replace: true });
              void loadConversations(dialogId).catch(() => undefined);
              return;
            }
            if (msg.event === 'retrieval') {
              const payload = data as { references: MessageReference[] };
              setReferences(payload.references ?? []);
              return;
            }
            if (msg.event === 'delta') {
              const payload = data as { content: string };
              setLines((current) => current.map((line, index) => index === current.length - 1 ? { ...line, content: line.content + payload.content } : line));
              return;
            }
            if (msg.event === 'error') {
              const payload = data as { message?: string };
              setError(friendlyErrorMessage(payload.message ?? data, '流式请求失败'));
            }
          },
          onError: (err) => {
            if (controller.signal.aborted) return;
            setError(friendlyErrorMessage(err, '流式请求失败'));
          },
        },
      );
    } finally {
      if (askAbortRef.current === controller) {
        askAbortRef.current = null;
        streamingConversationRef.current = undefined;
        if (!controller.signal.aborted) setStreaming(false);
      }
    }
  }

  const currentDialog = dialogs.find((item) => item.id === dialogId);

  useEffect(() => {
    if (!currentDialog) return;
    setProviderId(currentDialog.provider_id || providers[0]?.id || 0);
    setKbId(currentDialog.kb_ids?.[0] || knowledgeBases[0]?.id || 0);
    setModel(currentDialog.model || '');
    setRetrievalMode(currentDialog.retrieval_mode || 'hybrid');
    setTopK(currentDialog.top_k || 8);
    setHistoryLimit(currentDialog.history_round_limit || 10);
    setSystemPrompt(currentDialog.system_prompt || '');
    setPrologue(currentDialog.prologue || '');
  }, [currentDialog?.id]);

  return (
    <div className={isDialogScoped ? 'page chat-page-scoped' : 'page dialogue-page'}>
      {!isDialogScoped ? (
        <EditorialHeader word="Dialogue" script="Studio" kicker="RAG CONVERSATIONS / 03" description="RAG 对话 · 选择一个 Dialog，在专属工作室中组织会话与检索设置。" action={<Button tone="primary" onClick={() => setDialogOpen(true)}>
            <Plus size={17} />
            New Dialog
          </Button>} />
      ) : null}

      {!isDialogScoped ? (
        <div className="stack">
          {dialogs.length === 0 ? (
            <EmptyState icon={<MessageSquareText size={24} />} title="还没有 Dialog" description="新增一个 Dialog 后，它下面的会话会按分组展示。" action={<Button tone="primary" onClick={() => setDialogOpen(true)}>新增 Dialog</Button>} />
          ) : (
            <div className="workflow-library-list dialog-library-list">
              {dialogs.map((item) => (
                <article className="workflow-library-item dialog-library-item" key={item.id}>
                  <div className="workflow-miniature dialog-miniature" aria-hidden="true">
                    <span><MessageSquareText size={16} /></span>
                    <i />
                    <span><Database size={16} /></span>
                    <i />
                    <span className="workflow-miniature-end"><Send size={16} /></span>
                  </div>
                  <div className="workflow-library-copy">
                    <div className="card-title">
                      <h3 className="truncate">{item.name}</h3>
                      <StatusBadge tone={item.status === 1 ? 'good' : 'neutral'}>{item.status === 1 ? '启用' : '停用'}</StatusBadge>
                    </div>
                    {item.description ? <p className="muted clamp-2">{item.description}</p> : <p className="muted clamp-2">A retrieval dialog ready for grounded conversations.</p>}
                    <div className="meta-row">
                      <span>UPDATED {formatDate(item.updated_at ?? item.created_at)}</span>
                    </div>
                  </div>
                  <div className="workflow-library-actions">
                    <Button tone="primary" onClick={() => navigate(`/app/dialogs/${item.id}/chat`)}>
                      Open Dialog
                      <ArrowUpRight size={16} />
                    </Button>
                    <IconButton label="删除 Dialog" className="icon-btn-danger" onClick={() => void removeDialog(item.id)}><Trash2 size={16} /></IconButton>
                  </div>
                </article>
              ))}
            </div>
          )}
        </div>
      ) : null}

      {isDialogScoped ? (
        <div ref={chatLayoutRef} className="chat-shell" style={paneStyle({ '--dialog-nav-width': `${storedWidth('agentcanvas-dialog-navigator-width', 260)}px`, '--dialog-inspector-width': `${storedWidth('agentcanvas-dialog-inspector-width', 350)}px` })}>
          <aside className="chat-sidebar glass">
            <div className="chat-sidebar-head">
              <button type="button" className="chat-back" onClick={() => navigate('/app/dialogs')}>
                <ChevronLeft size={15} />
                全部 Dialog
              </button>
              <h2 className="truncate">{currentDialog?.name ?? '会话'}</h2>
              <Button tone="primary" onClick={() => navigate(`/app/dialogs/${dialogId}/chat/new`)}>
                <Plus size={15} />
                新建会话
              </Button>
            </div>
            <div className="chat-conversation-list">
              {isNewConversation ? (
                <div className="chat-conversation-item active">
                  <span className="truncate">新会话…</span>
                </div>
              ) : null}
              {conversations.map((conv) => (
                <div key={conv.id} className={`chat-conversation-item ${conv.id === conversationId ? 'active' : ''}`}>
                  <button type="button" onClick={() => navigate(`/app/dialogs/${dialogId}/chat/${conv.id}`)}>
                    <span className="truncate">{conv.title || '未命名会话'}</span>
                    <span className="chat-conversation-time">{formatDate(conv.last_message_at ?? conv.updated_at ?? conv.created_at)}</span>
                  </button>
                  <IconButton label="删除会话" className="chat-delete" onClick={() => void removeConversation(conv.id)}><Trash2 size={14} /></IconButton>
                </div>
              ))}
              {conversations.length === 0 && !isNewConversation ? (
                <p className="chat-empty-hint">还没有会话，点击「新建会话」开始。</p>
              ) : null}
            </div>
          </aside>

          <ResizableRail containerRef={chatLayoutRef} variable="--dialog-nav-width" storageKey="agentcanvas-dialog-navigator-width" side="left" min={210} max={360} collapsed={112} defaultWidth={260} label="调整会话导航宽度" />

          <section className="chat-main surface">
            {isDetail ? (
              <>
                <div className="chat-session-heading"><span>LIVE DIALOGUE</span><strong>{currentDialog?.name}</strong></div>
                <div className="message-list">
                  {lines.length === 0 ? (
                    <EmptyState icon={<MessageSquareText size={24} />} title="开始一次知识库对话" description="回答会随 SSE 增量显示，引用会在下方保留。" />
                  ) : (
                    lines.map((line, index) => <div className={`message ${line.role}`} key={`${line.role}-${index}`}>{line.content || '...'}</div>)
                  )}
                  {references.length > 0 ? (
                    <div className="stack">
                      <p className="eyebrow">引用来源</p>
                      {references.map((ref) => (
                        <article className="card" key={ref.id || ref.ref_index}>
                          <div className="card-title">
                            <h3 className="truncate">引用 #{ref.ref_index + 1}</h3>
                            <StatusBadge tone="info">{ref.score.toFixed(3)}</StatusBadge>
                          </div>
                          <p className="muted">{ref.quote_text}</p>
                        </article>
                      ))}
                    </div>
                  ) : null}
                </div>
                <form className="chat-composer" onSubmit={(event) => void ask(event)}>
                  <TextArea
                    value={question}
                    onChange={(event) => setQuestion(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return;
                      event.preventDefault();
                      event.currentTarget.form?.requestSubmit();
                    }}
                    placeholder="输入问题，Enter 发送，Shift+Enter 换行"
                  />
                  <Button tone="primary" disabled={streaming}>
                    <Send size={16} />
                    发送
                  </Button>
                </form>
              </>
            ) : (
              <EmptyState
                icon={<MessageSquareText size={24} />}
                title="选择或新建一个会话"
                description="从左侧选择一个会话，或点击「新建会话」开始对话。"
                action={<Button tone="primary" onClick={() => navigate(`/app/dialogs/${dialogId}/chat/new`)}>新建会话</Button>}
              />
            )}
          </section>
          <ResizableRail containerRef={chatLayoutRef} variable="--dialog-inspector-width" storageKey="agentcanvas-dialog-inspector-width" side="right" min={300} max={520} collapsed={120} defaultWidth={350} label="调整对话设置宽度；双击恢复" />
          <aside className="dialog-inspector glass">
            <div className="pane-heading"><span>DIALOG INSPECTOR</span><StatusBadge tone="info">LIVE</StatusBadge></div>
            <div className="form-stack">
              <Field label="Provider"><Select value={providerId} onChange={(event) => setProviderId(Number(event.target.value))}><option value={0}>选择 Provider</option>{providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</Select></Field>
              <Field label="Model"><TextInput value={model} onChange={(event) => setModel(event.target.value)} placeholder="Provider default" /></Field>
              <Field label="Knowledge Base"><Select value={kbId} onChange={(event) => setKbId(Number(event.target.value))}><option value={0}>不使用知识库</option>{knowledgeBases.map((kb) => <option key={kb.id} value={kb.id}>{kb.name}</option>)}</Select></Field>
              <div className="dense-grid"><Field label="Retrieval"><Select value={retrievalMode} onChange={(event) => setRetrievalMode(event.target.value)}><option value="keyword">Keyword</option><option value="vector">Vector</option><option value="hybrid">Hybrid</option></Select></Field><Field label="Top K"><TextInput type="number" min={1} value={topK} onChange={(event) => setTopK(Number(event.target.value))} /></Field></div>
              <Field label="History rounds"><TextInput type="number" min={1} value={historyLimit} onChange={(event) => setHistoryLimit(Number(event.target.value))} /></Field>
              <Field label="System Prompt"><TextArea value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} /></Field>
              <Field label="Prologue"><TextArea value={prologue} onChange={(event) => setPrologue(event.target.value)} /></Field>
              <Button tone="primary" onClick={() => void saveDialogSettings()}><Save size={16} />Save Settings</Button>
            </div>
          </aside>
        </div>
      ) : null}

      <Modal
        open={dialogOpen}
        title="新增 Dialog"
        onClose={() => setDialogOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setDialogOpen(false)}>取消</Button>
            <Button form="create-dialog-form" tone="primary">创建并进入</Button>
          </>
        }
      >
        <form id="create-dialog-form" className="form-stack" onSubmit={(event) => void createDialog(event)}>
          <Field label="Dialog 名称">
            <TextInput value={dialogName} onChange={(event) => setDialogName(event.target.value)} placeholder="例如：知识库问答" required />
          </Field>
        </form>
      </Modal>
      <Toast message={error} tone="bad" duration={4800} onClose={() => setError('')} />
    </div>
  );
}
