import { FormEvent, useEffect, useRef, useState } from 'react';
import { ChevronLeft, MessageSquareText, Plus, Send } from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import { chatApi, conversationApi, dialogApi, knowledgeApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, Modal, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
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
  // 进行中的 SSE 流控制器：组件卸载或重新发起时取消，避免内存泄漏与“卸载后 setState”。
  const askAbortRef = useRef<AbortController | null>(null);
  // 正在流式输出的会话 id：路由 effect 据此跳过对当前流会话的重载，避免覆盖正在生成的内容。
  const streamingConversationRef = useRef<number | undefined>(undefined);

  async function loadBase() {
    const [providerResp, kbResp, dialogResp] = await Promise.all([
      settingsApi.providers.list(),
      knowledgeApi.list(),
      dialogApi.list(),
    ]);
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
  }, []);

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

  return (
    <div className={isDialogScoped ? 'page chat-page-scoped' : 'page'}>
      {!isDialogScoped ? (
        <div className="page-head">
          <div>
            <h1>RAG 对话</h1>
            <p>选择一个 Dialog 进入，左侧排列会话，右侧即对话窗口。</p>
          </div>
          <Button tone="primary" onClick={() => setDialogOpen(true)}>
            <Plus size={17} />
            新增 Dialog
          </Button>
        </div>
      ) : null}

      {!isDialogScoped ? (
        <div className="stack">
          {dialogs.length === 0 ? (
            <EmptyState icon={<MessageSquareText size={24} />} title="还没有 Dialog" description="新增一个 Dialog 后，它下面的会话会按分组展示。" action={<Button tone="primary" onClick={() => setDialogOpen(true)}>新增 Dialog</Button>} />
          ) : (
            <div className="grid">
              {dialogs.map((item) => (
                <article className="card" key={item.id}>
                  <div className="card-title">
                    <h3 className="truncate">{item.name}</h3>
                    <StatusBadge tone={item.status === 1 ? 'good' : 'neutral'}>{item.status === 1 ? '启用' : '停用'}</StatusBadge>
                  </div>
                  {item.description ? <p className="muted">{item.description}</p> : <p className="muted">最近更新 {formatDate(item.updated_at ?? item.created_at)}</p>}
                  <div className="row-wrap">
                    <Button tone="primary" onClick={() => navigate(`/app/dialogs/${item.id}/chat`)}>打开 Dialog</Button>
                  </div>
                </article>
              ))}
            </div>
          )}
        </div>
      ) : null}

      {isDialogScoped ? (
        <div className="chat-shell">
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
                <button
                  type="button"
                  key={conv.id}
                  className={`chat-conversation-item ${conv.id === conversationId ? 'active' : ''}`}
                  onClick={() => navigate(`/app/dialogs/${dialogId}/chat/${conv.id}`)}
                >
                  <span className="truncate">{conv.title || '未命名会话'}</span>
                  <span className="chat-conversation-time">{formatDate(conv.last_message_at ?? conv.updated_at ?? conv.created_at)}</span>
                </button>
              ))}
              {conversations.length === 0 && !isNewConversation ? (
                <p className="chat-empty-hint">还没有会话，点击「新建会话」开始。</p>
              ) : null}
            </div>
          </aside>

          <section className="chat-main surface">
            {isDetail ? (
              <>
                <div className="chat-settings-bar">
                  <Field label="Provider">
                    <Select value={providerId} onChange={(event) => setProviderId(Number(event.target.value))}>
                      <option value={0}>选择 Provider</option>
                      {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="知识库">
                    <Select value={kbId} onChange={(event) => setKbId(Number(event.target.value))}>
                      <option value={0}>选择知识库</option>
                      {knowledgeBases.map((kb) => <option key={kb.id} value={kb.id}>{kb.name}</option>)}
                    </Select>
                  </Field>
                </div>
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
