import { FormEvent, useEffect, useMemo, useState } from 'react';
import { Plus, Trash2, Zap } from 'lucide-react';
import { settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Panel, Select, StatusBadge, TextInput, Toast } from '../components/ui';
import type { ModelProvider, ProviderCatalog, ProviderType } from '../types/api';
import { friendlyErrorMessage } from '../utils/format';

const CUSTOM_PROVIDER = '__custom__';
const supportedProviderTypes: ProviderType[] = ['openai_compatible', 'deepseek', 'qwen', 'azure_openai'];

function statusLabel(status: string) {
  if (status === 'ok') return 'Success';
  if (status === 'failed') return 'Fail';
  return status || 'untested';
}

function statusTone(status: string) {
  if (status === 'ok') return 'good';
  if (status === 'failed') return 'bad';
  return 'neutral';
}

export function ProviderSettings() {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [catalog, setCatalog] = useState<ProviderCatalog[]>([]);
  const [open, setOpen] = useState(false);
  const [catalogKey, setCatalogKey] = useState(CUSTOM_PROVIDER);
  const [name, setName] = useState('');
  const [providerType, setProviderType] = useState<ProviderType>('openai_compatible');
  const [baseUrl, setBaseUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [chatModel, setChatModel] = useState('');
  const [embeddingModel, setEmbeddingModel] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try {
      const [providerList, catalogList] = await Promise.all([settingsApi.providers.list(), settingsApi.providers.catalog()]);
      setProviders(providerList);
      setCatalog(catalogList);
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载 Provider 失败'));
    }
  }

  useEffect(() => { void load(); }, []);

  const selectedCatalog = useMemo(() => catalog.find((item) => item.key === catalogKey), [catalog, catalogKey]);
  const isCustom = catalogKey === CUSTOM_PROVIDER;
  const chatModels = selectedCatalog?.models.filter((item) => item.model_type === 'chat') ?? [];
  const embeddingModels = selectedCatalog?.models.filter((item) => item.model_type === 'embedding') ?? [];

  function selectCatalog(key: string) {
    setCatalogKey(key);
    setApiKey('');
    if (key === CUSTOM_PROVIDER) {
      setName(''); setProviderType('openai_compatible'); setBaseUrl(''); setChatModel(''); setEmbeddingModel('');
      return;
    }
    const item = catalog.find((entry) => entry.key === key);
    if (!item) return;
    setName(item.name);
    setProviderType(item.provider_type);
    setBaseUrl(item.base_url);
    setChatModel(item.models.find((model) => model.model_type === 'chat')?.name ?? '');
    setEmbeddingModel(item.models.find((model) => model.model_type === 'embedding')?.name ?? '');
  }

  async function createProvider(event: FormEvent) {
    event.preventDefault();
    try {
      await settingsApi.providers.create({ name, provider_type: providerType, base_url: baseUrl, api_key: apiKey, default_chat_model: chatModel, default_embedding_model: embeddingModel });
      setOpen(false);
      setMessage('Provider 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Provider 失败'));
    }
  }

  async function testProvider(id: number) {
    try {
      await settingsApi.providers.test(id);
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, 'Provider 连通性测试失败'));
    }
  }

  async function removeProvider(id: number) {
    if (!window.confirm('确认删除这个 Provider？')) return;
    try {
      await settingsApi.providers.remove(id);
      setMessage('Provider 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 Provider 失败'));
    }
  }

  return (
    <>
      {error ? <p className="error-text">{error}</p> : null}
      <Panel className="management-panel section-models" title="模型服务" eyebrow="Models" action={<Button tone="primary" onClick={() => { selectCatalog(catalog[0]?.key ?? CUSTOM_PROVIDER); setOpen(true); }}><Plus size={16} />New</Button>}>
        <div className="stack">
          {providers.length === 0 ? <EmptyState title="还没有模型服务" description="新增一个 Provider 后，Agent 就可以调用模型。" /> : providers.map((provider) => (
            <article className="card" key={provider.id}>
              <div className="card-title"><h3 className="truncate">{provider.name}</h3><StatusBadge tone={statusTone(provider.last_test_status)}>{statusLabel(provider.last_test_status)}</StatusBadge></div>
              <p className="muted truncate">{provider.provider_type} · {provider.default_chat_model || '未设置默认模型'}</p>
              <div className="row-wrap"><Button onClick={() => void testProvider(provider.id)}><Zap size={16} />测试</Button><IconButton label="删除 Provider" onClick={() => void removeProvider(provider.id)}><Trash2 size={16} /></IconButton></div>
            </article>
          ))}
        </div>
      </Panel>
      <Modal open={open} title="新增 Provider" onClose={() => setOpen(false)} footer={<><Button type="button" onClick={() => setOpen(false)}>取消</Button><Button form="create-provider-form" tone="primary">保存</Button></>}>
        <form id="create-provider-form" className="form-stack" onSubmit={(event) => void createProvider(event)}>
          <Field label="供应商" hint="请选择一个供应商模板，或选择自定义手动填写。"><Select value={catalogKey} onChange={(event) => selectCatalog(event.target.value)}>{catalog.map((item) => <option key={item.key} value={item.key}>{item.name}</option>)}<option value={CUSTOM_PROVIDER}>自定义</option></Select></Field>
          {selectedCatalog?.doc_url ? <p className="muted">文档：<a href={selectedCatalog.doc_url} target="_blank" rel="noreferrer">{selectedCatalog.doc_url}</a></p> : null}
          <Field label="名称"><TextInput value={name} onChange={(event) => setName(event.target.value)} required /></Field>
          {isCustom ? <Field label="类型"><Select value={providerType} onChange={(event) => setProviderType(event.target.value as ProviderType)}>{supportedProviderTypes.map((type) => <option key={type} value={type}>{type}</option>)}</Select></Field> : null}
          <Field label="Base URL"><TextInput value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} readOnly={!isCustom} /></Field>
          <Field label="API Key"><TextInput type="password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} /></Field>
          <Field label="默认 Chat 模型">{isCustom ? <TextInput value={chatModel} onChange={(event) => setChatModel(event.target.value)} /> : <Select value={chatModel} onChange={(event) => setChatModel(event.target.value)}><option value="">不设置</option>{chatModels.map((model) => <option key={model.name} value={model.name}>{model.name}</option>)}</Select>}</Field>
          <Field label="默认 Embedding 模型">{isCustom ? <TextInput value={embeddingModel} onChange={(event) => setEmbeddingModel(event.target.value)} /> : <Select value={embeddingModel} onChange={(event) => setEmbeddingModel(event.target.value)}><option value="">不设置</option>{embeddingModels.map((model) => <option key={model.name} value={model.name}>{model.name}</option>)}</Select>}</Field>
        </form>
      </Modal>
      <Toast message={message} tone="good" />
    </>
  );
}
