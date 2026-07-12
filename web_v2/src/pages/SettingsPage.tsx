import { FormEvent, useEffect, useState } from 'react';
import { Braces, KeyRound, PackageCheck, PlugZap, Plus, Server, ShieldCheck, Trash2 } from 'lucide-react';
import { Button, Card, EmptyState, Field, IconButton, Modal, Select, TextArea, TextInput } from '@/components/ui';
import { settingsApi } from '@/api/resources';
import type { ApiToken, MCPServer, ModelProvider, ProviderCatalog, ProviderType, Skill, ToolDefinition } from '@/types/api';

type ModalKind = 'provider' | 'token' | 'tool' | 'mcp' | 'skill' | null;

export function SettingsPage() {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [catalog, setCatalog] = useState<ProviderCatalog[]>([]);
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [modal, setModal] = useState<ModalKind>(null);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [providerType, setProviderType] = useState<ProviderType>('openai_compatible');
  const [apiKey, setApiKey] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [model, setModel] = useState('');
  const [scratch, setScratch] = useState('{}');

  const load = async () => {
    const [p, c, t, toolList, mcpList, skillList] = await Promise.all([
      settingsApi.providers.list().catch(() => []),
      settingsApi.providers.catalog().catch(() => []),
      settingsApi.tokens.list().catch(() => []),
      settingsApi.tools.list().catch(() => []),
      settingsApi.tools.listMCPServers().catch(() => []),
      settingsApi.skills.list().catch(() => []),
    ]);
    setProviders(p); setCatalog(c); setTokens(t); setTools(toolList); setMcpServers(mcpList); setSkills(skillList);
  };

  useEffect(() => { void load(); }, []);

  const reset = () => { setModal(null); setName(''); setDescription(''); setApiKey(''); setBaseUrl(''); setModel(''); setScratch('{}'); };
  const createToken = async (event: FormEvent) => { event.preventDefault(); await settingsApi.tokens.create({ name, scopes: ['*'] }); reset(); void load(); };
  const removeToken = async (id: number) => { await settingsApi.tokens.remove(id); void load(); };
  const createProvider = async (event: FormEvent) => { event.preventDefault(); await settingsApi.providers.create({ name, provider_type: providerType, base_url: baseUrl, api_key: apiKey, default_chat_model: model }); reset(); void load(); };
  const createTool = async (event: FormEvent) => { event.preventDefault(); await settingsApi.tools.create({ name, description, config_json: JSON.parse(scratch) as Record<string, unknown> }); reset(); void load(); };
  const createMcp = async (event: FormEvent) => { event.preventDefault(); await settingsApi.tools.createMCPServer({ name, transport: 'sse', endpoint_url: baseUrl }); reset(); void load(); };
  const createSkill = async (event: FormEvent) => { event.preventDefault(); await settingsApi.skills.create({ name, description, content_md: scratch, skill_type: 'instruction', source_type: 'inline' }); reset(); void load(); };

  return (
    <div className="page-grid">
      <div className="section-header"><div><h2>System Settings</h2><p>模型、工具、MCP、Skills 与 Token 集中管理。保持控制台安静、密集、可扫描。</p></div><Button tone="primary" onClick={() => setModal('provider')}><Plus size={18} /> 添加 Provider</Button></div>
      <div className="settings-grid">
        <Section title="Model Providers" icon={<Server size={17} />} action={<Button size="small" onClick={() => setModal('provider')}><Plus size={15} /> 添加</Button>} empty="暂无 Provider">
          {providers.map((provider) => <div className="table-row" key={provider.id}><strong>{provider.name}</strong><span>{provider.provider_type}</span><span>{provider.default_chat_model || '-'}</span><Button size="small" onClick={() => void settingsApi.providers.test(provider.id)}>测试</Button></div>)}
        </Section>
        <Section title="API Tokens" icon={<KeyRound size={17} />} action={<Button size="small" onClick={() => setModal('token')}><Plus size={15} /> 创建</Button>} empty="暂无 Token">
          {tokens.map((token) => <div className="table-row" key={token.id}><strong>{token.name}</strong><span>{token.token_prefix}</span><span>{token.expires_at || 'never'}</span><IconButton onClick={() => void removeToken(token.id)}><Trash2 size={15} /></IconButton></div>)}
        </Section>
        <Section title="Tools" icon={<Braces size={17} />} action={<Button size="small" onClick={() => setModal('tool')}><Plus size={15} /> 添加</Button>} empty="暂无 Tool">
          {tools.map((tool) => <div className="table-row" key={tool.id}><strong>{tool.name}</strong><span>{tool.tool_type}</span><span>{tool.status === 1 ? 'active' : 'disabled'}</span><Button size="small" onClick={() => void settingsApi.tools.test(tool.id, {})}>测试</Button></div>)}
        </Section>
        <Section title="MCP Servers" icon={<PlugZap size={17} />} action={<Button size="small" onClick={() => setModal('mcp')}><Plus size={15} /> 添加</Button>} empty="暂无 MCP Server">
          {mcpServers.map((server) => <div className="table-row" key={server.id}><strong>{server.name}</strong><span>{server.transport}</span><span>{server.status === 1 ? 'active' : 'disabled'}</span><Button size="small" onClick={() => void settingsApi.tools.refreshMCPServer(server.id)}>刷新</Button></div>)}
        </Section>
        <Section title="Skills" icon={<PackageCheck size={17} />} action={<Button size="small" onClick={() => setModal('skill')}><Plus size={15} /> 添加</Button>} empty="暂无 Skill">
          {skills.map((skill) => <div className="table-row" key={skill.id}><strong>{skill.name}</strong><span>{skill.skill_type}</span><span>v{skill.version}</span><Button size="small" onClick={() => void settingsApi.skills.validate(skill.id)}>校验</Button></div>)}
        </Section>
        <Card className="glass-strong"><div className="panel-header" style={{ padding: 0, borderBottom: 0 }}><h3><ShieldCheck size={17} /> Provider Catalog</h3></div><div className="grid-cards">{catalog.slice(0, 8).map((item) => <div className="mini-card" key={item.key}><strong>{item.name}</strong><span>{item.models.length} models</span></div>)}</div></Card>
      </div>
      <SettingsModal modal={modal} reset={reset} createProvider={createProvider} createToken={createToken} createTool={createTool} createMcp={createMcp} createSkill={createSkill} name={name} setName={setName} description={description} setDescription={setDescription} providerType={providerType} setProviderType={setProviderType} apiKey={apiKey} setApiKey={setApiKey} baseUrl={baseUrl} setBaseUrl={setBaseUrl} model={model} setModel={setModel} scratch={scratch} setScratch={setScratch} />
    </div>
  );
}

function Section({ title, icon, action, empty, children }: { title: string; icon: React.ReactNode; action: React.ReactNode; empty: string; children: React.ReactNode }) {
  const list = Array.isArray(children) ? children : [children];
  return <Card className="glass-strong"><div className="panel-header" style={{ padding: 0, borderBottom: 0 }}><h3>{icon} {title}</h3>{action}</div>{list.length === 0 ? <EmptyState title={empty} /> : <div className="table-list">{children}</div>}</Card>;
}

function SettingsModal(props: { modal: ModalKind; reset: () => void; createProvider: (e: FormEvent) => void; createToken: (e: FormEvent) => void; createTool: (e: FormEvent) => void; createMcp: (e: FormEvent) => void; createSkill: (e: FormEvent) => void; name: string; setName: (v: string) => void; description: string; setDescription: (v: string) => void; providerType: ProviderType; setProviderType: (v: ProviderType) => void; apiKey: string; setApiKey: (v: string) => void; baseUrl: string; setBaseUrl: (v: string) => void; model: string; setModel: (v: string) => void; scratch: string; setScratch: (v: string) => void }) {
  const p = props;
  if (!p.modal) return null;
  const submit = p.modal === 'provider' ? p.createProvider : p.modal === 'token' ? p.createToken : p.modal === 'tool' ? p.createTool : p.modal === 'mcp' ? p.createMcp : p.createSkill;
  return <Modal open title={`添加 ${p.modal}`} onClose={p.reset}><form className="auth-form" onSubmit={submit}><Field label="名称"><TextInput value={p.name} onChange={(e) => p.setName(e.target.value)} required /></Field>{p.modal === 'provider' ? <><Field label="类型"><Select value={p.providerType} onChange={(e) => p.setProviderType(e.target.value as ProviderType)}><option value="openai_compatible">OpenAI Compatible</option><option value="deepseek">DeepSeek</option><option value="qwen">Qwen</option><option value="ollama">Ollama</option><option value="azure_openai">Azure OpenAI</option><option value="local">Local</option></Select></Field><Field label="Base URL"><TextInput value={p.baseUrl} onChange={(e) => p.setBaseUrl(e.target.value)} /></Field><Field label="API Key"><TextInput type="password" value={p.apiKey} onChange={(e) => p.setApiKey(e.target.value)} /></Field><Field label="默认模型"><TextInput value={p.model} onChange={(e) => p.setModel(e.target.value)} /></Field></> : null}{p.modal === 'tool' || p.modal === 'skill' ? <Field label="描述"><TextInput value={p.description} onChange={(e) => p.setDescription(e.target.value)} /></Field> : null}{p.modal === 'mcp' ? <Field label="Endpoint URL"><TextInput value={p.baseUrl} onChange={(e) => p.setBaseUrl(e.target.value)} /></Field> : null}{p.modal === 'tool' ? <Field label="Tool Config JSON"><TextArea className="mono" value={p.scratch} onChange={(e) => p.setScratch(e.target.value)} /></Field> : null}{p.modal === 'skill' ? <Field label="Skill Markdown"><TextArea className="mono" value={p.scratch} onChange={(e) => p.setScratch(e.target.value)} /></Field> : null}<Button tone="primary">保存</Button></form></Modal>;
}
