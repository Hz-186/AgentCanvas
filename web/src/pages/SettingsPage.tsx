import { FormEvent, useEffect, useState } from 'react';
import { BrainCircuit, Globe2, KeyRound, Plus, ShieldCheck, Trash2, Zap } from 'lucide-react';
import { settingsApi } from '../api/resources';
import { Button, Field, IconButton, Modal, Panel, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { ApiToken, AuditLog, Memory, ModelProvider, ProviderType, ToolDefinition } from '../types/api';
import { formatDate, parseJsonObject } from '../utils/format';

const providerTypes: ProviderType[] = ['openai_compatible', 'deepseek', 'qwen', 'ollama', 'azure_openai', 'local'];

export function SettingsPage() {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [audits, setAudits] = useState<AuditLog[]>([]);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [providerOpen, setProviderOpen] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [toolOpen, setToolOpen] = useState(false);
  const [providerName, setProviderName] = useState('');
  const [providerType, setProviderType] = useState<ProviderType>('openai_compatible');
  const [baseUrl, setBaseUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [chatModel, setChatModel] = useState('');
  const [embeddingModel, setEmbeddingModel] = useState('');
  const [tokenName, setTokenName] = useState('');
  const [memoryType, setMemoryType] = useState('profile_memory');
  const [memoryTitle, setMemoryTitle] = useState('');
  const [memoryContent, setMemoryContent] = useState('');
  const [toolName, setToolName] = useState('');
  const [toolDescription, setToolDescription] = useState('');
  const [toolConfig, setToolConfig] = useState('{\n  "url": "https://api.example.com/search",\n  "method": "GET",\n  "timeout_ms": 5000,\n  "max_response_bytes": 524288\n}');
  const [createdToken, setCreatedToken] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    const [providerResp, tokenResp, auditResp, memoryResp, toolResp] = await Promise.all([
      settingsApi.providers.list(),
      settingsApi.tokens.list(),
      settingsApi.audits.list(),
      settingsApi.memories.list(),
      settingsApi.tools.list(),
    ]);
    setProviders(providerResp);
    setTokens(tokenResp);
    setAudits(auditResp);
    setMemories(memoryResp);
    setTools(toolResp);
  }

  useEffect(() => {
    void load().catch((err) => setError(err instanceof Error ? err.message : '加载设置失败'));
  }, []);

  async function createProvider(event: FormEvent) {
    event.preventDefault();
    await settingsApi.providers.create({
      name: providerName,
      provider_type: providerType,
      base_url: baseUrl,
      api_key: apiKey,
      default_chat_model: chatModel,
      default_embedding_model: embeddingModel,
    });
    setProviderOpen(false);
    setProviderName('');
    setApiKey('');
    setMessage('Provider 已创建');
    await load();
  }

  async function testProvider(id: number) {
    await settingsApi.providers.test(id);
    setMessage('Provider 连通性测试已完成');
    await load();
  }

  async function removeProvider(id: number) {
    if (!window.confirm('确认删除这个 Provider？')) return;
    await settingsApi.providers.remove(id);
    setMessage('Provider 已删除');
    await load();
  }

  async function createToken(event: FormEvent) {
    event.preventDefault();
    const created = await settingsApi.tokens.create({ name: tokenName, scopes: ['*'] });
    setCreatedToken(created.token);
    setTokenName('');
    setMessage('API Token 已创建，请立即保存');
    await load();
  }

  async function removeToken(id: number) {
    await settingsApi.tokens.remove(id);
    setMessage('API Token 已撤销');
    await load();
  }

  async function createMemory(event: FormEvent) {
    event.preventDefault();
    await settingsApi.memories.create({ memory_type: memoryType, title: memoryTitle, content: memoryContent, importance: 0.5, source: 'manual' });
    setMemoryOpen(false);
    setMemoryTitle('');
    setMemoryContent('');
    setMessage('Memory 已创建');
    await load();
  }

  async function removeMemory(id: number) {
    await settingsApi.memories.remove(id);
    setMessage('Memory 已删除');
    await load();
  }

  async function createTool(event: FormEvent) {
    event.preventDefault();
    await settingsApi.tools.create({ name: toolName, tool_type: 'http', description: toolDescription, config_json: parseJsonObject(toolConfig) });
    setToolOpen(false);
    setToolName('');
    setToolDescription('');
    setMessage('HTTP Tool 已创建');
    await load();
  }

  async function removeTool(id: number) {
    await settingsApi.tools.remove(id);
    setMessage('HTTP Tool 已删除');
    await load();
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>设置</h1>
          <p>管理模型 Provider、API Token 和审计日志。</p>
        </div>
      </div>
      {error ? <p className="error-text">{error}</p> : null}

      <div className="dense-grid">
        <Panel title="Model Providers" eyebrow="LLM" action={<Button tone="primary" onClick={() => setProviderOpen(true)}><Plus size={16} />新增</Button>}>
          <div className="stack">
            {providers.map((provider) => (
              <article className="card" key={provider.id}>
                <div className="card-title">
                  <h3 className="truncate">{provider.name}</h3>
                  <StatusBadge tone={provider.last_test_status === 'ok' ? 'good' : provider.last_test_status === 'failed' ? 'bad' : 'neutral'}>{provider.last_test_status || 'untested'}</StatusBadge>
                </div>
                <p className="muted truncate">{provider.provider_type} · {provider.default_chat_model || '未设置默认模型'}</p>
                <div className="row-wrap">
                  <Button onClick={() => void testProvider(provider.id)}><Zap size={16} />测试</Button>
                  <IconButton label="删除 Provider" onClick={() => void removeProvider(provider.id)}><Trash2 size={16} /></IconButton>
                </div>
              </article>
            ))}
          </div>
        </Panel>

        <Panel title="API Tokens" eyebrow="Access" action={<Button tone="primary" onClick={() => setTokenOpen(true)}><KeyRound size={16} />创建</Button>}>
          <div className="stack">
            {tokens.map((token) => (
              <article className="card" key={token.id}>
                <div className="card-title">
                  <h3 className="truncate">{token.name}</h3>
                  <StatusBadge tone={token.revoked_at ? 'bad' : 'good'}>{token.revoked_at ? 'Revoked' : 'Active'}</StatusBadge>
                </div>
                <p className="muted">{token.token_prefix} · {formatDate(token.created_at)}</p>
                <IconButton label="撤销 Token" onClick={() => void removeToken(token.id)}><Trash2 size={16} /></IconButton>
              </article>
            ))}
            {createdToken ? <pre className="code-box">{createdToken}</pre> : null}
          </div>
        </Panel>
      </div>

      <div className="dense-grid">
        <Panel title="Memories" eyebrow="Phase 6" action={<Button tone="primary" onClick={() => setMemoryOpen(true)}><BrainCircuit size={16} />新增</Button>}>
          <div className="stack">
            {memories.map((memory) => (
              <article className="card" key={memory.id}>
                <div className="card-title">
                  <h3 className="truncate">{memory.title || memory.memory_type}</h3>
                  <StatusBadge tone="info">{memory.memory_type}</StatusBadge>
                </div>
                <p className="muted clamp-2">{memory.content}</p>
                <div className="row-wrap">
                  <span className="muted">{formatDate(memory.updated_at)}</span>
                  <IconButton label="删除 Memory" onClick={() => void removeMemory(memory.id)}><Trash2 size={16} /></IconButton>
                </div>
              </article>
            ))}
          </div>
        </Panel>

        <Panel title="HTTP Tools" eyebrow="Phase 6" action={<Button tone="primary" onClick={() => setToolOpen(true)}><Globe2 size={16} />新增</Button>}>
          <div className="stack">
            {tools.map((tool) => (
              <article className="card" key={tool.id}>
                <div className="card-title">
                  <h3 className="truncate">{tool.name}</h3>
                  <StatusBadge tone={tool.status === 1 ? 'good' : 'neutral'}>{tool.status === 1 ? 'Active' : 'Disabled'}</StatusBadge>
                </div>
                <p className="muted truncate">{String(tool.config_json?.method ?? 'GET')} · {String(tool.config_json?.url ?? '')}</p>
                <IconButton label="删除 HTTP Tool" onClick={() => void removeTool(tool.id)}><Trash2 size={16} /></IconButton>
              </article>
            ))}
          </div>
        </Panel>
      </div>

      <Panel title="审计日志" eyebrow="Audit">
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Resource</th>
                <th>IP</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {audits.map((audit) => (
                <tr key={audit.id}>
                  <td>{audit.action}</td>
                  <td>{audit.resource_type} / {audit.resource_id}</td>
                  <td>{audit.ip_address}</td>
                  <td>{formatDate(audit.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>

      <Modal
        open={providerOpen}
        title="新增 Provider"
        onClose={() => setProviderOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setProviderOpen(false)}>取消</Button>
            <Button form="create-provider-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-provider-form" className="form-stack" onSubmit={(event) => void createProvider(event)}>
          <Field label="名称"><TextInput value={providerName} onChange={(event) => setProviderName(event.target.value)} required /></Field>
          <Field label="类型">
            <Select value={providerType} onChange={(event) => setProviderType(event.target.value as ProviderType)}>
              {providerTypes.map((type) => <option key={type} value={type}>{type}</option>)}
            </Select>
          </Field>
          <Field label="Base URL"><TextInput value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} /></Field>
          <Field label="API Key"><TextInput type="password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} /></Field>
          <Field label="默认 Chat 模型"><TextInput value={chatModel} onChange={(event) => setChatModel(event.target.value)} /></Field>
          <Field label="默认 Embedding 模型"><TextInput value={embeddingModel} onChange={(event) => setEmbeddingModel(event.target.value)} /></Field>
        </form>
      </Modal>

      <Modal
        open={tokenOpen}
        title="创建 API Token"
        onClose={() => setTokenOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setTokenOpen(false)}>取消</Button>
            <Button form="create-token-form" tone="primary">创建</Button>
          </>
        }
      >
        <form id="create-token-form" className="form-stack" onSubmit={(event) => void createToken(event)}>
          <Field label="Token 名称"><TextInput value={tokenName} onChange={(event) => setTokenName(event.target.value)} required /></Field>
          <div className="row muted"><ShieldCheck size={16} /> 默认授予当前后端支持的通用作用域。</div>
        </form>
      </Modal>

      <Modal
        open={memoryOpen}
        title="新增 Memory"
        onClose={() => setMemoryOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setMemoryOpen(false)}>取消</Button>
            <Button form="create-memory-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-memory-form" className="form-stack" onSubmit={(event) => void createMemory(event)}>
          <Field label="类型">
            <Select value={memoryType} onChange={(event) => setMemoryType(event.target.value)}>
              <option value="profile_memory">profile_memory</option>
              <option value="summary_memory">summary_memory</option>
              <option value="episodic_memory">episodic_memory</option>
              <option value="task_memory">task_memory</option>
            </Select>
          </Field>
          <Field label="标题"><TextInput value={memoryTitle} onChange={(event) => setMemoryTitle(event.target.value)} /></Field>
          <Field label="内容"><TextArea value={memoryContent} onChange={(event) => setMemoryContent(event.target.value)} required /></Field>
        </form>
      </Modal>

      <Modal
        open={toolOpen}
        title="新增 HTTP Tool"
        onClose={() => setToolOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setToolOpen(false)}>取消</Button>
            <Button form="create-tool-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-tool-form" className="form-stack" onSubmit={(event) => void createTool(event)}>
          <Field label="名称"><TextInput value={toolName} onChange={(event) => setToolName(event.target.value)} required /></Field>
          <Field label="描述"><TextInput value={toolDescription} onChange={(event) => setToolDescription(event.target.value)} /></Field>
          <Field label="配置 JSON">
            <TextArea value={toolConfig} onChange={(event) => setToolConfig(event.target.value)} required />
          </Field>
          <pre className="code-box">{toolConfig}</pre>
        </form>
      </Modal>

      <Toast message={message} tone="good" />
    </div>
  );
}
