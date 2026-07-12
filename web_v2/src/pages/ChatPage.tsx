import { FormEvent, useEffect, useState } from 'react';
import { MessageCircle, Plus, Send } from 'lucide-react';
import { Button, Card, EmptyState, Field, Modal, Select, TextArea, TextInput } from '@/components/ui';
import { chatApi, conversationApi, dialogApi, knowledgeApi, settingsApi } from '@/api/resources';
import type { Conversation, Dialog, KnowledgeBase, Message, MessageReference, ModelProvider } from '@/types/api';
import type { ChatStreamEvent } from '@/types/events';

export function ChatPage() {
  const [dialogs, setDialogs] = useState<Dialog[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [kbs, setKbs] = useState<KnowledgeBase[]>([]);
  const [selected, setSelected] = useState<Dialog | null>(null);
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [question, setQuestion] = useState('');
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [references, setReferences] = useState<MessageReference[]>([]);
  const [retrievalLatency, setRetrievalLatency] = useState<number | null>(null);

  const load = async () => {
    const [dialogList, providerList, kbList] = await Promise.all([dialogApi.list().catch(() => []), settingsApi.providers.list().catch(() => []), knowledgeApi.list().catch(() => [])]);
    setDialogs(dialogList); setProviders(providerList); setKbs(kbList); setSelected((current) => current ?? dialogList[0] ?? null);
  };
  useEffect(() => { void load(); }, []);
  useEffect(() => { if (selected) void conversationApi.list(selected.id).then((list) => { setConversation(list[0] ?? null); if (list[0]) void conversationApi.listMessages(selected.id, list[0].id).then(setMessages); else setMessages([]); }).catch(() => setMessages([])); }, [selected]);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const provider = providers[0];
    await dialogApi.create({ name, provider_id: provider?.id, model: provider?.default_chat_model, kb_ids: kbs[0] ? [kbs[0].id] : [], top_k: 5, retrieval_mode: 'hybrid' });
    setOpen(false); setName(''); await load();
  };

  const send = async (event: FormEvent) => {
    event.preventDefault();
    if (!selected || !providers[0] || !question.trim()) return;
    const localQuestion = question;
    setQuestion('');
    setReferences([]);
    setRetrievalLatency(null);
    setStreaming(true);
    const userDraft: Message = { id: -Date.now(), owner_id: 0, conversation_id: conversation?.id ?? 0, role: 'user', content: localQuestion, content_type: 'text', token_count: 0, created_at: new Date().toISOString() };
    const assistantDraft: Message = { id: userDraft.id - 1, owner_id: 0, conversation_id: conversation?.id ?? 0, role: 'assistant', content: '', content_type: 'text', token_count: 0, created_at: new Date().toISOString() };
    setMessages((current) => [...current, userDraft, assistantDraft]);
    await chatApi.stream(selected.id, { provider_id: selected.provider_id || providers[0].id, kb_ids: selected.kb_ids?.length ? selected.kb_ids : kbs.slice(0, 1).map((kb) => kb.id), question: localQuestion, conversation_id: conversation?.id, model: selected.model || providers[0].default_chat_model, top_k: selected.top_k || 5 }, {
      onMessage: (msg) => {
        const event = { type: msg.event, data: JSON.parse(msg.data) } as ChatStreamEvent;
        if (event.type === 'conversation') setConversation(event.data);
        if (event.type === 'retrieval') { setReferences(event.data.references); setRetrievalLatency(event.data.latency_ms); }
        if (event.type === 'delta') setMessages((current) => current.map((message) => message.id === assistantDraft.id ? { ...message, content: message.content + event.data.content } : message));
        if (event.type === 'done') { setConversation(event.data.conversation); setMessages((current) => current.filter((message) => message.id !== userDraft.id && message.id !== assistantDraft.id).concat(event.data.user_message, event.data.assistant_message)); setReferences(event.data.references); }
        if (event.type === 'error') setMessages((current) => current.map((message) => message.id === assistantDraft.id ? { ...message, content: event.data.message } : message));
      },
      onError: (error) => setMessages((current) => current.map((message) => message.id === assistantDraft.id ? { ...message, content: error.message } : message)),
    });
    setStreaming(false);
  };

  return <div className="page-grid"><div className="section-header"><div><h2>Dialog Lab</h2><p>面向调试的 RAG 对话工作台。保持界面安静，让模型、知识库和引用结果更容易被检查。</p></div><Button tone="primary" onClick={() => setOpen(true)}><Plus size={18} /> 新建对话</Button></div><div className="chat-layout"><Card className="resource-list glass-strong">{dialogs.length === 0 ? <EmptyState title="暂无对话" /> : dialogs.map((dialog) => <button className={`resource-row${selected?.id === dialog.id ? ' active' : ''}`} key={dialog.id} onClick={() => setSelected(dialog)}><span className="palette-icon"><MessageCircle size={17} /></span><span><strong>{dialog.name}</strong><small>{dialog.model || 'default model'}</small></span></button>)}</Card><Card className="chat-panel glass-strong"><div className="chat-messages scroll-surface">{retrievalLatency !== null ? <div className="retrieval-strip"><strong>Retrieval</strong><span>{references.length} references · {retrievalLatency}ms</span></div> : null}{messages.length === 0 ? <EmptyState title="选择对话并开始提问" /> : messages.map((message) => <div key={message.id} className={`chat-bubble ${message.role}`}><strong>{message.role}</strong><p>{message.content || (message.role === 'assistant' && streaming ? '正在生成...' : '')}</p></div>)}{references.length > 0 ? <div className="reference-grid">{references.slice(0, 4).map((ref) => <div className="mini-card" key={ref.id || `${ref.chunk_id}-${ref.ref_index}`}><strong>Ref #{ref.ref_index + 1}</strong><span>{ref.quote_text}</span></div>)}</div> : null}</div><form className="chat-composer" onSubmit={send}><TextArea value={question} onChange={(e) => setQuestion(e.target.value)} placeholder="输入问题..." /><Button tone="primary" disabled={streaming}><Send size={17} /> {streaming ? '生成中' : '发送'}</Button></form></Card></div><Modal open={open} title="新建对话" onClose={() => setOpen(false)}><form className="auth-form" onSubmit={create}><Field label="名称"><TextInput value={name} onChange={(e) => setName(e.target.value)} required /></Field><Field label="默认模型"><Select disabled><option>{providers[0]?.default_chat_model || '需要先配置 Provider'}</option></Select></Field><Button tone="primary">创建</Button></form></Modal></div>;
}
