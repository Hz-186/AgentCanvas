import { FormEvent, useEffect, useState } from 'react';
import { MessageSquareText, Send } from 'lucide-react';
import { chatApi, conversationApi, knowledgeApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, Panel, Select, StatusBadge, TextArea } from '../components/ui';
import type { Conversation, KnowledgeBase, Message, MessageReference, ModelProvider } from '../types/api';
import { formatDate } from '../utils/format';

interface ChatLine {
  role: 'user' | 'assistant';
  content: string;
}

export function ChatPage() {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [providerId, setProviderId] = useState(0);
  const [kbId, setKbId] = useState(0);
  const [conversationId, setConversationId] = useState<number | undefined>();
  const [question, setQuestion] = useState('');
  const [lines, setLines] = useState<ChatLine[]>([]);
  const [references, setReferences] = useState<MessageReference[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState('');

  async function load() {
    const [providerResp, kbResp, convResp] = await Promise.all([
      settingsApi.providers.list(),
      knowledgeApi.list(),
      conversationApi.list(),
    ]);
    setProviders(providerResp);
    setKnowledgeBases(kbResp);
    setConversations(convResp);
    setProviderId((current) => current || providerResp[0]?.id || 0);
    setKbId((current) => current || kbResp[0]?.id || 0);
  }

  useEffect(() => {
    void load().catch((err) => setError(err instanceof Error ? err.message : '加载聊天配置失败'));
  }, []);

  async function openConversation(id: number) {
    setConversationId(id);
    const messages = await conversationApi.listMessages(id);
    setLines(messages.map((msg: Message) => ({ role: msg.role === 'user' ? 'user' : 'assistant', content: msg.content })));
  }

  async function ask(event: FormEvent) {
    event.preventDefault();
    if (!question.trim() || !providerId || !kbId) {
      setError('请选择 Provider、知识库并输入问题');
      return;
    }
    const currentQuestion = question.trim();
    setLines((current) => [...current, { role: 'user', content: currentQuestion }, { role: 'assistant', content: '' }]);
    setQuestion('');
    setReferences([]);
    setStreaming(true);
    setError('');
    await chatApi.stream(
      { provider_id: providerId, kb_ids: [kbId], question: currentQuestion, conversation_id: conversationId, top_k: 8 },
      {
        onMessage: (msg) => {
          const data = JSON.parse(msg.data) as unknown;
          if (msg.event === 'conversation') {
            const conv = data as Conversation;
            setConversationId(conv.id);
            void load();
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
          if (msg.event === 'done') setStreaming(false);
          if (msg.event === 'error') {
            const payload = data as { message?: string };
            setError(payload.message ?? '流式请求失败');
            setStreaming(false);
          }
        },
        onError: (err) => {
          setError(err.message);
          setStreaming(false);
        },
      },
    );
    setStreaming(false);
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>RAG Chat</h1>
          <p>选择 Provider 和知识库，用真实检索结果驱动流式回答。</p>
        </div>
        {streaming ? <StatusBadge tone="info">Streaming</StatusBadge> : <StatusBadge>Ready</StatusBadge>}
      </div>

      <div className="chat-layout">
        <Panel title="会话与上下文" eyebrow="Conversations">
          <div className="form-stack">
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
          <div className="stack">
            {conversations.map((conv) => (
              <button className="card" type="button" key={conv.id} onClick={() => void openConversation(conv.id)} style={{ borderColor: conv.id === conversationId ? 'var(--accent)' : undefined }}>
                <h3 className="truncate">{conv.title}</h3>
                <p className="muted">{formatDate(conv.last_message_at ?? conv.created_at)}</p>
              </button>
            ))}
          </div>
        </Panel>

        <Panel title="对话" eyebrow="Streaming Chat">
          <div className="message-list">
            {lines.length === 0 ? (
              <EmptyState icon={<MessageSquareText size={24} />} title="开始一次知识库对话" description="回答会随 SSE 增量显示，引用会在下方保留。" />
            ) : (
              lines.map((line, index) => <div className={`message ${line.role}`} key={`${line.role}-${index}`}>{line.content || '...'}</div>)
            )}
          </div>
          {references.length > 0 ? (
            <div className="stack">
              <p className="eyebrow">References</p>
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
          {error ? <p className="error-text">{error}</p> : null}
          <form className="chat-composer" onSubmit={(event) => void ask(event)}>
            <TextArea value={question} onChange={(event) => setQuestion(event.target.value)} placeholder="输入问题" />
            <Button tone="primary" disabled={streaming}>
              <Send size={16} />
              发送
            </Button>
          </form>
        </Panel>
      </div>
    </div>
  );
}
