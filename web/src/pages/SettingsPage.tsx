import { FormEvent, useEffect, useMemo, useState } from 'react';
import { ArrowUpRight, BrainCircuit, Boxes, FlaskConical, Globe2, KeyRound, Network, Pencil, PlugZap, Plus, ShieldCheck, Sparkles, Trash2, Zap } from 'lucide-react';
import { resourceSummaryApi, settingsApi } from '../api/resources';
import { EditorialHeader } from '../components/editorial';
import { Button, EmptyState, Field, IconButton, Modal, Panel, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { ApiToken, AuditLog, MCPServer, MCPToolCache, Memory, ModelProvider, ProviderCatalog, ProviderType, Skill, ToolDefinition, ToolPack, ToolPackItem, ToolPolicy } from '../types/api';
import { formatDate, friendlyErrorMessage, parseJsonObject } from '../utils/format';

const providerTypes: ProviderType[] = ['openai_compatible', 'deepseek', 'qwen', 'ollama', 'azure_openai', 'local'];
const CUSTOM_PROVIDER = '__custom__';
const memoryTypeOptions = [
  { value: 'profile_memory', label: '个人画像（偏好与设定）' },
  { value: 'summary_memory', label: '摘要记忆（压缩上下文）' },
  { value: 'episodic_memory', label: '事件记忆（具体经历）' },
  { value: 'task_memory', label: '任务记忆（目标与待办）' },
];

function memoryTypeLabel(value: string) {
  return memoryTypeOptions.find((option) => option.value === value)?.label ?? value;
}

function providerTestStatusLabel(status: string) {
  if (status === 'ok') return 'Success';
  if (status === 'failed') return 'Fail';
  return status || 'untested';
}

function providerTestStatusTone(status: string) {
  if (status === 'ok') return 'good';
  if (status === 'failed') return 'bad';
  return 'neutral';
}

function maskSecret(value: string) {
  const token = value.trim();
  if (token.length <= 10) return `${token.slice(0, 4)}********`;
  return `${token.slice(0, 8)}********${token.slice(-6)}`;
}

function maskTokenPrefix(prefix: string) {
  return `${prefix}********`;
}

export function MemoryPage() {
  const [memories, setMemories] = useState<Memory[]>([]);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [memoryType, setMemoryType] = useState(memoryTypeOptions[0].value);
  const [memoryTitle, setMemoryTitle] = useState('');
  const [memoryContent, setMemoryContent] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try {
      setMemories(await settingsApi.memories.list());
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载记忆失败'));
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function createMemory(event: FormEvent) {
    event.preventDefault();
    try {
      await settingsApi.memories.create({ memory_type: memoryType, title: memoryTitle, content: memoryContent, importance: 0.5, source: 'manual' });
      setMemoryOpen(false);
      setMemoryTitle('');
      setMemoryContent('');
      setMessage('记忆已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建记忆失败'));
    }
  }

  async function removeMemory(id: number) {
    try {
      await settingsApi.memories.remove(id);
      setMessage('记忆已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除记忆失败'));
    }
  }

  return (
    <div className="page memory-page">
      <EditorialHeader word="Memory" script="Archive" kicker="LONG-TERM CONTEXT / 04" description="长期记忆 · 管理可被 Agent 读取和写入的持久上下文。" action={<Button tone="primary" onClick={() => setMemoryOpen(true)}>
          <Plus size={17} />
          New Memory
        </Button>} />
      {error ? <p className="error-text">{error}</p> : null}

      {memories.length === 0 ? (
        <div className="empty">
          <div className="empty-icon"><BrainCircuit size={24} /></div>
          <h3>还没有记忆</h3>
          <p>新增一条记忆后，Agent 就可以在流程中读取它。</p>
          <Button tone="primary" onClick={() => setMemoryOpen(true)}>新增记忆</Button>
        </div>
      ) : (
        <div className="workflow-library-list memory-library-list">
          {memories.map((memory) => (
            <article className="workflow-library-item memory-library-item" key={memory.id}>
              <div className="workflow-miniature memory-miniature" aria-hidden="true">
                <span><BrainCircuit size={16} /></span>
                <i />
                <span><Network size={16} /></span>
                <i />
                <span className="workflow-miniature-end"><ArrowUpRight size={16} /></span>
              </div>
              <div className="workflow-library-copy">
                <div className="card-title">
                  <h3 className="truncate">{memory.title || memory.memory_type}</h3>
                  <StatusBadge tone="info">{memoryTypeLabel(memory.memory_type)}</StatusBadge>
                </div>
                <p className="muted clamp-2">{memory.content}</p>
                <div className="meta-row">
                  <span>IMPORTANCE {memory.importance.toFixed(1)}</span>
                  <span>UPDATED {formatDate(memory.updated_at)}</span>
                </div>
              </div>
              <div className="workflow-library-actions">
                <IconButton label="删除记忆" onClick={() => void removeMemory(memory.id)}><Trash2 size={16} /></IconButton>
              </div>
            </article>
          ))}
        </div>
      )}

      <Modal
        open={memoryOpen}
        title="新增记忆"
        onClose={() => setMemoryOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setMemoryOpen(false)}>取消</Button>
            <Button form="create-memory-page-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-memory-page-form" className="form-stack" onSubmit={(event) => void createMemory(event)}>
          <Field label="类型">
            <Select value={memoryType} onChange={(event) => setMemoryType(event.target.value)}>
              {memoryTypeOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </Select>
          </Field>
          <Field label="标题"><TextInput value={memoryTitle} onChange={(event) => setMemoryTitle(event.target.value)} /></Field>
          <Field label="内容"><TextArea value={memoryContent} onChange={(event) => setMemoryContent(event.target.value)} required /></Field>
        </form>
      </Modal>
      <Toast message={message} tone="good" />
    </div>
  );
}

type ManagementView = 'settings' | 'tools' | 'skills';

function ManagementPage({ view }: { view: ManagementView }) {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [catalog, setCatalog] = useState<ProviderCatalog[]>([]);
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [audits, setAudits] = useState<AuditLog[]>([]);
  const [memories] = useState<Memory[]>([]);
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [toolPolicies, setToolPolicies] = useState<ToolPolicy[]>([]);
  const [toolPacks, setToolPacks] = useState<ToolPack[]>([]);
  const [packItems, setPackItems] = useState<ToolPackItem[]>([]);
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  const [mcpTools, setMcpTools] = useState<MCPToolCache[]>([]);
  const [providerOpen, setProviderOpen] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [toolOpen, setToolOpen] = useState(false);
  const [editingToolId, setEditingToolId] = useState(0);
  const [toolTestInput, setToolTestInput] = useState('{}');
  const [toolTestResult, setToolTestResult] = useState('');
  const [skillOpen, setSkillOpen] = useState(false);
  const [editingSkillId, setEditingSkillId] = useState(0);
  const [policyOpen, setPolicyOpen] = useState(false);
  const [packOpen, setPackOpen] = useState(false);
  const [mcpOpen, setMcpOpen] = useState(false);
  const [catalogKey, setCatalogKey] = useState(CUSTOM_PROVIDER);
  const [providerName, setProviderName] = useState('');
  const [providerType, setProviderType] = useState<ProviderType>('openai_compatible');
  const [baseUrl, setBaseUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [chatModel, setChatModel] = useState('');
  const [embeddingModel, setEmbeddingModel] = useState('');
  const [tokenName, setTokenName] = useState('');
  const [memoryType, setMemoryType] = useState(memoryTypeOptions[0].value);
  const [memoryTitle, setMemoryTitle] = useState('');
  const [memoryContent, setMemoryContent] = useState('');
  const [toolName, setToolName] = useState('');
  const [toolDescription, setToolDescription] = useState('');
  const [toolConfig, setToolConfig] = useState('{\n  "url": "https://api.example.com/search",\n  "method": "GET",\n  "timeout_ms": 5000,\n  "max_response_bytes": 524288\n}');
  const [skillName, setSkillName] = useState('');
  const [skillDescription, setSkillDescription] = useState('');
  const [skillSourceType, setSkillSourceType] = useState<'inline' | 'local_path'>('inline');
  const [skillContent, setSkillContent] = useState('## When To Use\n\nUse this skill when ...\n\n## Steps\n\n1. Inspect the current context.\n2. Decide whether this workflow applies.\n3. Execute the steps using available tools.\n\n## Safety\n\nDo not perform write or external actions without explicit approval.');
  const [skillBundlePath, setSkillBundlePath] = useState('');
  const [skillTags, setSkillTags] = useState('');
  const [policyName, setPolicyName] = useState('');
  const [policyRisks, setPolicyRisks] = useState<string[]>(['high']);
  const [policyAllowedHosts, setPolicyAllowedHosts] = useState('');
  const [policyTimeoutMS, setPolicyTimeoutMS] = useState(30000);
  const [policyMaxOutputBytes, setPolicyMaxOutputBytes] = useState(524288);
  const [packName, setPackName] = useState('');
  const [packDescription, setPackDescription] = useState('');
  const [selectedPackId, setSelectedPackId] = useState(0);
  const [packToolId, setPackToolId] = useState(0);
  const [mcpName, setMcpName] = useState('');
  const [mcpTransport, setMcpTransport] = useState<'sse' | 'stdio'>('sse');
  const [mcpEndpoint, setMcpEndpoint] = useState('');
  const [mcpCommand, setMcpCommand] = useState('');
  const [mcpArgs, setMcpArgs] = useState('');
  const [selectedMcpId, setSelectedMcpId] = useState(0);
  const [createdToken, setCreatedToken] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const tabs = view === 'settings'
    ? ['models', 'access', 'audit'] as const
    : view === 'tools'
      ? ['http', 'mcp', 'packs', 'policies'] as const
      : ['skills'] as const;
  const [activeSection, setActiveSection] = useState<string>(tabs[0]);

  useEffect(() => {
    setActiveSection(tabs[0]);
  }, [view]);

  async function load() {
    setError('');
    if (view === 'skills') {
      const page = await resourceSummaryApi.list('skills', { limit: 100 });
      setSkills(page.items.map((item) => ({
        id: item.id,
        owner_id: 0,
        name: item.name,
        description: item.description ?? '',
        skill_type: item.resource_type === 'bundle' ? 'bundle' : 'instruction',
        source_type: 'inline',
        entry_file: 'SKILL.md',
        status: item.status ?? 1,
        version: 0,
        checksum: '',
        created_at: item.updated_at,
        updated_at: item.updated_at,
      })));
      return;
    }
    if (view === 'settings') {
      if (activeSection === 'models') {
        const [providerList, catalogList] = await Promise.all([settingsApi.providers.list(), settingsApi.providers.catalog()]);
        setProviders(providerList);
        setCatalog(catalogList);
      } else if (activeSection === 'access') {
        setTokens((await settingsApi.tokens.list()).filter((token) => !token.revoked_at));
      } else if (activeSection === 'audit') {
        setAudits(await settingsApi.audits.list());
      }
      return;
    }
    if (activeSection === 'http') {
      const page = await resourceSummaryApi.list('http-tools', { limit: 100 });
      setTools(page.items.map((item) => ({
        id: item.id,
        owner_id: 0,
        name: item.name,
        tool_type: item.resource_type ?? 'http',
        description: item.description ?? '',
        config_json: {},
        input_schema_json: {},
        output_schema_json: {},
        status: item.status ?? 1,
        created_at: item.updated_at,
        updated_at: item.updated_at,
      })));
    } else if (activeSection === 'mcp') {
      const servers = await settingsApi.tools.listMCPServers();
      setMcpServers(servers);
      setSelectedMcpId((current) => current || servers[0]?.id || 0);
    } else if (activeSection === 'packs') {
      const [toolList, packs] = await Promise.all([settingsApi.tools.list(), settingsApi.tools.listPacks()]);
      setTools(toolList);
      setToolPacks(packs);
      setSelectedPackId((current) => current || packs[0]?.id || 0);
    } else if (activeSection === 'policies') {
      setToolPolicies(await settingsApi.tools.listPolicies());
    }
  }

  useEffect(() => {
    void load().catch((err) => setError(friendlyErrorMessage(err, '加载设置失败')));
  }, [activeSection, view]);

  useEffect(() => {
    if (activeSection !== 'packs' || !selectedPackId) {
      setPackItems([]);
      return;
    }
    void settingsApi.tools.listPackItems(selectedPackId)
      .then(setPackItems)
      .catch((err) => setError(friendlyErrorMessage(err, '加载 Tool Pack 明细失败')));
  }, [activeSection, selectedPackId]);

  useEffect(() => {
    if (activeSection !== 'mcp' || !selectedMcpId) {
      setMcpTools([]);
      return;
    }
    void settingsApi.tools.listMCPTools(selectedMcpId)
      .then(setMcpTools)
      .catch((err) => setError(friendlyErrorMessage(err, '加载 MCP 工具缓存失败')));
  }, [activeSection, selectedMcpId]);

  const selectedCatalog = useMemo(
    () => catalog.find((item) => item.key === catalogKey),
    [catalog, catalogKey],
  );
  const isCustom = catalogKey === CUSTOM_PROVIDER;
  const chatModels = selectedCatalog?.models.filter((m) => m.model_type === 'chat') ?? [];
  const embeddingModels = selectedCatalog?.models.filter((m) => m.model_type === 'embedding') ?? [];

  function openProviderModal() {
    selectCatalog(catalog[0]?.key ?? CUSTOM_PROVIDER);
    setProviderOpen(true);
  }

  function selectCatalog(key: string) {
    setCatalogKey(key);
    if (key === CUSTOM_PROVIDER) {
      setProviderName('');
      setProviderType('openai_compatible');
      setBaseUrl('');
      setChatModel('');
      setEmbeddingModel('');
      return;
    }
    const item = catalog.find((c) => c.key === key);
    if (!item) return;
    setProviderName(item.name);
    setProviderType(item.provider_type);
    setBaseUrl(item.base_url);
    setChatModel(item.models.find((m) => m.model_type === 'chat')?.name ?? '');
    setEmbeddingModel(item.models.find((m) => m.model_type === 'embedding')?.name ?? '');
  }

  async function createProvider(event: FormEvent) {
    event.preventDefault();
    try {
      await settingsApi.providers.create({
        name: providerName,
        provider_type: providerType,
        base_url: baseUrl,
        api_key: apiKey,
        default_chat_model: chatModel,
        default_embedding_model: embeddingModel,
      });
      setProviderOpen(false);
      setMessage('Provider 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Provider 失败'));
    }
  }

  async function testProvider(id: number) {
    try {
      await settingsApi.providers.test(id);
      setMessage('');
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

  async function createToken(event: FormEvent) {
    event.preventDefault();
    try {
      const created = await settingsApi.tokens.create({ name: tokenName, scopes: ['*'] });
      setCreatedToken(maskSecret(created.token));
      setTokenName('');
      setTokenOpen(false);
      setMessage('API Token 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 API Token 失败'));
    }
  }

  async function removeToken(id: number) {
    try {
      await settingsApi.tokens.remove(id);
      setTokens((current) => current.filter((token) => token.id !== id));
      setCreatedToken('');
      setMessage('API Token 已撤销');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '撤销 API Token 失败'));
    }
  }

  async function createMemory(event: FormEvent) {
    event.preventDefault();
    try {
      await settingsApi.memories.create({ memory_type: memoryType, title: memoryTitle, content: memoryContent, importance: 0.5, source: 'manual' });
      setMemoryOpen(false);
      setMemoryTitle('');
      setMemoryContent('');
      setMessage('Memory 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Memory 失败'));
    }
  }

  const visibleTokens = tokens.filter((token) => !token.revoked_at);

  async function removeMemory(id: number) {
    try {
      await settingsApi.memories.remove(id);
      setMessage('Memory 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 Memory 失败'));
    }
  }

  async function createTool(event: FormEvent) {
    event.preventDefault();
    let config: Record<string, unknown>;
    try {
      config = parseJsonObject(toolConfig);
    } catch (err) {
      setError(friendlyErrorMessage(err, 'HTTP Tool 配置需要是合法 JSON 对象'));
      return;
    }
    try {
      if (editingToolId) {
        await settingsApi.tools.update(editingToolId, { name: toolName, description: toolDescription, config_json: config });
      } else {
        await settingsApi.tools.create({ name: toolName, tool_type: 'http', description: toolDescription, config_json: config });
      }
      setToolOpen(false);
      setEditingToolId(0);
      setToolName('');
      setToolDescription('');
      setMessage(editingToolId ? 'HTTP Tool 已更新' : 'HTTP Tool 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 HTTP Tool 失败'));
    }
  }

  async function editTool(summary: ToolDefinition) {
    try {
      const tool = await settingsApi.tools.get(summary.id);
      setEditingToolId(tool.id);
      setToolName(tool.name);
      setToolDescription(tool.description ?? '');
      setToolConfig(JSON.stringify(tool.config_json ?? {}, null, 2));
      setToolTestInput('{}');
      setToolTestResult('');
      setToolOpen(true);
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载 HTTP Tool 详情失败'));
    }
  }

  async function testTool(id: number) {
    try {
      const input = parseJsonObject(toolTestInput);
      const result = await settingsApi.tools.test(id, input);
      setToolTestResult(JSON.stringify(result, null, 2));
      setMessage('HTTP Tool 测试完成');
    } catch (err) {
      setError(friendlyErrorMessage(err, 'HTTP Tool 测试失败'));
    }
  }

  async function removeTool(id: number) {
    try {
      await settingsApi.tools.remove(id);
      setMessage('HTTP Tool 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 HTTP Tool 失败'));
    }
  }

  async function createSkill(event: FormEvent) {
    event.preventDefault();
    try {
      const body = {
        name: skillName,
        description: skillDescription,
        source_type: skillSourceType,
        content_md: skillSourceType === 'inline' ? skillContent : undefined,
        bundle_path: skillSourceType === 'local_path' ? skillBundlePath : undefined,
        tags: skillTags.split(',').map((item) => item.trim()).filter(Boolean),
      };
      if (editingSkillId) await settingsApi.skills.update(editingSkillId, body);
      else await settingsApi.skills.create(body);
      setSkillOpen(false);
      setEditingSkillId(0);
      setSkillName('');
      setSkillDescription('');
      setSkillBundlePath('');
      setSkillTags('');
      setMessage(editingSkillId ? 'Skill 已更新' : 'Skill 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Skill 失败'));
    }
  }

  async function editSkill(summary: Skill) {
    try {
      const item = await settingsApi.skills.get(summary.id);
      setEditingSkillId(item.id);
      setSkillName(item.name);
      setSkillDescription(item.description ?? '');
      setSkillSourceType(item.source_type);
      setSkillContent(item.content_md ?? '');
      setSkillBundlePath(item.bundle_path ?? '');
      setSkillTags(Array.isArray(item.tags_json) ? item.tags_json.join(', ') : '');
      setSkillOpen(true);
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载 Skill 详情失败'));
    }
  }

  async function removeSkill(id: number) {
    try {
      await settingsApi.skills.remove(id);
      setMessage('Skill 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 Skill 失败'));
    }
  }

  async function validateSkill(id: number) {
    try {
      const result = await settingsApi.skills.validate(id);
      setMessage(result.valid ? 'Skill 校验通过' : `Skill 校验失败：${result.error ?? '未知错误'}`);
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '校验 Skill 失败'));
    }
  }

  async function createPolicy(event: FormEvent) {
    event.preventDefault();
    try {
      await settingsApi.tools.createPolicy({
        name: policyName,
        require_approval_for_risk: policyRisks,
        max_timeout_ms: policyTimeoutMS,
        max_output_bytes: policyMaxOutputBytes,
        allowed_hosts: policyAllowedHosts.split(',').map((item) => item.trim()).filter(Boolean),
      });
      setPolicyOpen(false);
      setPolicyName('');
      setPolicyAllowedHosts('');
      setMessage('Tool Policy 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Tool Policy 失败'));
    }
  }

  async function removePolicy(id: number) {
    try {
      await settingsApi.tools.removePolicy(id);
      setMessage('Tool Policy 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 Tool Policy 失败'));
    }
  }

  async function createPack(event: FormEvent) {
    event.preventDefault();
    try {
      const created = await settingsApi.tools.createPack({ name: packName, description: packDescription });
      setPackOpen(false);
      setPackName('');
      setPackDescription('');
      setSelectedPackId(created.id);
      setMessage('Tool Pack 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Tool Pack 失败'));
    }
  }

  async function removePack(id: number) {
    try {
      await settingsApi.tools.removePack(id);
      setSelectedPackId((current) => (current === id ? 0 : current));
      setMessage('Tool Pack 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 Tool Pack 失败'));
    }
  }

  async function addToolToPack() {
    if (!selectedPackId || !packToolId) {
      setError('请选择 Tool Pack 和工具');
      return;
    }
    try {
      await settingsApi.tools.addPackItem(selectedPackId, packToolId);
      setPackToolId(0);
      setPackItems(await settingsApi.tools.listPackItems(selectedPackId));
      setMessage('工具已加入 Tool Pack');
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '添加工具到 Tool Pack 失败'));
    }
  }

  async function removeToolFromPack(toolId: number) {
    if (!selectedPackId) return;
    try {
      await settingsApi.tools.removePackItem(selectedPackId, toolId);
      setPackItems(await settingsApi.tools.listPackItems(selectedPackId));
      setMessage('工具已移出 Tool Pack');
    } catch (err) {
      setError(friendlyErrorMessage(err, '移除 Tool Pack 工具失败'));
    }
  }

  async function createMcpServer(event: FormEvent) {
    event.preventDefault();
    try {
      const created = await settingsApi.tools.createMCPServer({
        name: mcpName,
        transport: mcpTransport,
        endpoint_url: mcpTransport === 'sse' ? mcpEndpoint : undefined,
        command: mcpTransport === 'stdio' ? mcpCommand : undefined,
        args_json: mcpTransport === 'stdio' ? mcpArgs.split(/\s+/).map((item) => item.trim()).filter(Boolean) : [],
        env_json: {},
      });
      setMcpOpen(false);
      setMcpName('');
      setMcpEndpoint('');
      setMcpCommand('');
      setMcpArgs('');
      setSelectedMcpId(created.id);
      setMessage('MCP Server 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 MCP Server 失败'));
    }
  }

  async function refreshMcpServer(id: number) {
    try {
      const resp = await settingsApi.tools.refreshMCPServer(id);
      setMcpTools(resp.tools);
      setMessage(`MCP 工具已刷新：${resp.tools.length} 个`);
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '刷新 MCP 工具失败'));
    }
  }

  async function removeMcpServer(id: number) {
    try {
      await settingsApi.tools.removeMCPServer(id);
      setSelectedMcpId((current) => (current === id ? 0 : current));
      setMessage('MCP Server 已删除');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '删除 MCP Server 失败'));
    }
  }

  return (
    <div className="page management-page" data-management-view={view} data-active-section={activeSection}>
      <EditorialHeader
        word={view === 'settings' ? 'System' : view === 'tools' ? 'Tool' : 'Skill'}
        script={view === 'settings' ? 'Settings' : view === 'tools' ? 'Atelier' : 'Library'}
        kicker={view === 'settings' ? 'SYSTEM CONTROL / 07' : view === 'tools' ? 'TOOL GOVERNANCE / 05' : 'CAPABILITY LIBRARY / 06'}
        description={view === 'settings' ? '模型、访问与审计 · 保持系统配置清晰而专注。' : view === 'tools' ? 'HTTP、MCP 与工具治理 · 组合、测试并约束外部能力。' : '可复用能力 · 管理、校验并组织 Agent Skills。'}
      />
      {error ? <p className="error-text">{error}</p> : null}

      <nav className="management-nav glass" aria-label="管理分类">
        {tabs.map((tab) => (
          <button type="button" key={tab} className={activeSection === tab ? 'active' : ''} onClick={() => setActiveSection(tab)}>
            <span>{tab}</span>
            <small>{tab === 'models' ? 'Providers & Models' : tab === 'access' ? 'API Tokens' : tab === 'audit' ? 'Activity history' : tab === 'http' ? 'HTTP endpoints' : tab === 'mcp' ? 'Model Context Protocol' : tab === 'packs' ? 'Reusable tool sets' : tab === 'policies' ? 'Risk & approval' : 'Reusable instructions'}</small>
          </button>
        ))}
      </nav>

      <div className="dense-grid management-block management-system-block">
        <Panel className="management-panel section-models" title="模型服务" eyebrow="Models" action={<Button tone="primary" onClick={openProviderModal}><Plus size={16} />New</Button>}>
          <div className="stack">
            {providers.length === 0 ? (
              <EmptyState title="还没有模型服务" description="新增一个 Provider 后，Agent 就可以调用模型。" />
            ) : providers.map((provider) => (
              <article className="card" key={provider.id}>
                <div className="card-title">
                  <h3 className="truncate">{provider.name}</h3>
                  <StatusBadge tone={providerTestStatusTone(provider.last_test_status)}>{providerTestStatusLabel(provider.last_test_status)}</StatusBadge>
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

        <Panel className="management-panel section-access" title="访问令牌" eyebrow="Access" action={<Button tone="primary" onClick={() => setTokenOpen(true)}><KeyRound size={16} />Create</Button>}>
          <div className="stack">
            {visibleTokens.length === 0 ? (
              <EmptyState title="还没有访问令牌" description="创建令牌后，可用于外部服务访问当前 API。" />
            ) : visibleTokens.map((token) => (
              <article className="card" key={token.id}>
                <div className="card-title">
                  <h3 className="truncate">{token.name}</h3>
                  <StatusBadge tone="good">有效</StatusBadge>
                </div>
                <p className="muted">{maskTokenPrefix(token.token_prefix)} · {formatDate(token.created_at)}</p>
                <IconButton label="撤销 Token" onClick={() => void removeToken(token.id)}><Trash2 size={16} /></IconButton>
              </article>
            ))}
            {createdToken ? <pre className="code-box">{createdToken}</pre> : null}
          </div>
        </Panel>
      </div>

      <div className="dense-grid management-block management-resource-block">
        <Panel className="management-panel section-memory" title="长期记忆" eyebrow="Memory" action={<Button tone="primary" onClick={() => setMemoryOpen(true)}><BrainCircuit size={16} />New</Button>}>
          <div className="stack">
            {memories.length === 0 ? (
              <EmptyState title="还没有记忆" description="新增一条记忆后，Agent 就可以在流程中读取它。" />
            ) : memories.map((memory) => (
              <article className="card" key={memory.id}>
                <div className="card-title">
                  <h3 className="truncate">{memory.title || memory.memory_type}</h3>
                  <StatusBadge tone="info">{memoryTypeLabel(memory.memory_type)}</StatusBadge>
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

        <Panel className="management-panel section-http" title="HTTP 工具" eyebrow="HTTP Tools" action={<Button tone="primary" onClick={() => { setEditingToolId(0); setToolName(''); setToolDescription(''); setToolOpen(true); }}><Globe2 size={16} />New</Button>}>
          <div className="stack">
            {tools.length === 0 ? (
              <EmptyState title="还没有 HTTP 工具" description="新增工具后，Agent 可以在流程中调用外部接口。" />
            ) : tools.map((tool) => (
              <article className="card" key={tool.id}>
                <div className="card-title">
                  <h3 className="truncate">{tool.name}</h3>
                  <StatusBadge tone={tool.status === 1 ? 'good' : 'neutral'}>{tool.status === 1 ? 'Active' : 'Disabled'}</StatusBadge>
                </div>
                <p className="muted truncate">{tool.description || 'HTTP Tool'}</p>
                <div className="row-wrap">
                  <Button onClick={() => void editTool(tool)}><Pencil size={15} />编辑与测试</Button>
                  <IconButton label="删除 HTTP Tool" onClick={() => void removeTool(tool.id)}><Trash2 size={16} /></IconButton>
                </div>
              </article>
            ))}
          </div>
        </Panel>

        <Panel className="management-panel section-skills" title="Skills" eyebrow="Capability" action={<Button tone="primary" onClick={() => { setEditingSkillId(0); setSkillName(''); setSkillDescription(''); setSkillOpen(true); }}><Sparkles size={16} />New</Button>}>
          <div className="stack">
            {skills.length === 0 ? (
              <EmptyState title="还没有 Skill" description="新增 Skill 后，Agent 可以在运行时按需加载这些说明。" />
            ) : skills.map((item) => (
              <article className="card" key={item.id}>
                <div className="card-title">
                  <h3 className="truncate">{item.name}</h3>
                  <StatusBadge tone={item.last_validation_error ? 'bad' : item.status === 1 ? 'good' : 'neutral'}>{item.last_validation_error ? 'validation failed' : item.status === 1 ? 'active' : 'disabled'}</StatusBadge>
                </div>
                <p className="muted truncate">{item.description}</p>
                <p className="muted truncate">{item.source_type} · {item.entry_file}</p>
                <p className="muted truncate">{Array.isArray(item.tags_json) ? item.tags_json.join(', ') : '无标签'} · {item.last_validated_at ? formatDate(item.last_validated_at) : '未校验'}</p>
                {item.last_validation_error ? <p className="error-text clamp-2">{item.last_validation_error}</p> : null}
                <div className="row-wrap">
                  <Button onClick={() => void editSkill(item)}><Pencil size={15} />编辑</Button>
                  <Button onClick={() => void validateSkill(item.id)}><Zap size={16} />校验</Button>
                  <IconButton label="删除 Skill" onClick={() => void removeSkill(item.id)}><Trash2 size={16} /></IconButton>
                </div>
              </article>
            ))}
          </div>
        </Panel>
      </div>

      <div className="dense-grid management-block management-tools-block">
        <Panel className="management-panel section-policies" title="Tool Policy" eyebrow="Governance" action={<Button tone="primary" onClick={() => setPolicyOpen(true)}><ShieldCheck size={16} />New</Button>}>
          <div className="stack">
            {toolPolicies.length === 0 ? (
              <EmptyState title="还没有 Tool Policy" description="创建策略后，可统一治理高风险工具的审批、超时与输出上限。" />
            ) : toolPolicies.map((policy) => (
              <article className="card" key={policy.id}>
                <div className="card-title">
                  <h3 className="truncate">{policy.name}</h3>
                  <StatusBadge tone="warn">{(policy.require_approval_for_risk ?? []).join(',') || 'none'}</StatusBadge>
                </div>
                <p className="muted truncate">timeout {policy.max_timeout_ms}ms · output {policy.max_output_bytes} bytes</p>
                <p className="muted truncate">{(policy.allowed_hosts ?? []).join(', ') || '未限制 host'}</p>
                <IconButton label="删除 Tool Policy" onClick={() => void removePolicy(policy.id)}><Trash2 size={16} /></IconButton>
              </article>
            ))}
          </div>
        </Panel>

        <Panel className="management-panel section-packs" title="Tool Pack" eyebrow="Collections" action={<Button tone="primary" onClick={() => setPackOpen(true)}><Boxes size={16} />New</Button>}>
          <div className="stack">
            {toolPacks.length === 0 ? (
              <EmptyState title="还没有 Tool Pack" description="把常用工具组合成工具包，便于后续绑定到 Workflow Profile。" />
            ) : (
              <>
                <Field label="当前 Pack">
                  <Select value={selectedPackId} onChange={(event) => setSelectedPackId(Number(event.target.value))}>
                    <option value={0}>选择 Tool Pack</option>
                    {toolPacks.map((pack) => <option key={pack.id} value={pack.id}>{pack.name}</option>)}
                  </Select>
                </Field>
                <div className="inline-form">
                  <Select value={packToolId} onChange={(event) => setPackToolId(Number(event.target.value))}>
                    <option value={0}>选择工具</option>
                    {tools.map((tool) => <option key={tool.id} value={tool.id}>{tool.name}</option>)}
                  </Select>
                  <Button onClick={() => void addToolToPack()}>加入</Button>
                </div>
                {toolPacks.map((pack) => (
                  <article className="card" key={pack.id}>
                    <div className="card-title">
                      <h3 className="truncate">{pack.name}</h3>
                      <StatusBadge tone={pack.id === selectedPackId ? 'info' : 'neutral'}>{pack.id === selectedPackId ? 'selected' : 'pack'}</StatusBadge>
                    </div>
                    <p className="muted clamp-2">{pack.description || '无描述'}</p>
                    <IconButton label="删除 Tool Pack" onClick={() => void removePack(pack.id)}><Trash2 size={16} /></IconButton>
                  </article>
                ))}
                {selectedPackId ? (
                  <div className="trace-list">
                    {packItems.length === 0 ? <p className="muted">当前 Pack 暂无工具。</p> : null}
                    {packItems.map((item) => {
                      const packedTool = tools.find((tool) => tool.id === item.tool_id);
                      return (
                        <article className="trace-item" key={item.id}>
                          <div className="trace-item-head">
                            <strong>{packedTool?.name ?? `Tool #${item.tool_id}`}</strong>
                            <IconButton label="移出 Tool Pack" onClick={() => void removeToolFromPack(item.tool_id)}><Trash2 size={15} /></IconButton>
                          </div>
                        </article>
                      );
                    })}
                  </div>
                ) : null}
              </>
            )}
          </div>
        </Panel>
      </div>

      <Panel className="management-panel section-mcp" title="MCP Server" eyebrow="External Tools" action={<Button tone="primary" onClick={() => setMcpOpen(true)}><PlugZap size={16} />New</Button>}>
        <div className="stack">
          {mcpServers.length === 0 ? (
            <EmptyState title="还没有 MCP Server" description="接入 MCP 后，Agent 可以通过统一工具协议扩展能力。" />
          ) : (
            <>
              <Field label="当前 Server">
                <Select value={selectedMcpId} onChange={(event) => setSelectedMcpId(Number(event.target.value))}>
                  <option value={0}>选择 MCP Server</option>
                  {mcpServers.map((server) => <option key={server.id} value={server.id}>{server.name}</option>)}
                </Select>
              </Field>
              <div className="grid">
                {mcpServers.map((server) => (
                  <article className="card" key={server.id}>
                    <div className="card-title">
                      <h3 className="truncate">{server.name}</h3>
                      <StatusBadge tone={server.last_error ? 'bad' : server.discovered_at ? 'good' : 'neutral'}>{server.last_error ? '错误' : server.discovered_at ? '已发现' : '未刷新'}</StatusBadge>
                    </div>
                    <p className="muted truncate">{server.transport === 'sse' ? server.endpoint_url : server.command}</p>
                    <p className="muted">发现时间 {formatDate(server.discovered_at)}</p>
                    {server.last_error ? <p className="error-text clamp-2">{server.last_error}</p> : null}
                    <div className="row-wrap">
                      <Button onClick={() => void refreshMcpServer(server.id)}><Zap size={16} />刷新</Button>
                      <IconButton label="删除 MCP Server" onClick={() => void removeMcpServer(server.id)}><Trash2 size={16} /></IconButton>
                    </div>
                  </article>
                ))}
              </div>
              {selectedMcpId ? (
                <div className="trace-list">
                  {mcpTools.length === 0 ? <p className="muted">当前 Server 暂无工具缓存，刷新后会显示工具列表。</p> : null}
                  {mcpTools.map((item) => (
                    <article className="trace-item" key={item.id}>
                      <div className="trace-item-head">
                        <strong>{item.tool_name}</strong>
                        <StatusBadge tone="neutral">mcp</StatusBadge>
                      </div>
                      <p className="muted clamp-2">{item.description || '无描述'}</p>
                    </article>
                  ))}
                </div>
              ) : null}
            </>
          )}
        </div>
      </Panel>

      <Panel className="management-panel section-audit" title="审计日志" eyebrow="Audit Trail">
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
          <Field label="供应商" hint="请选择一个供应商模板，或选择自定义手动填写。">
            <Select value={catalogKey} onChange={(event) => selectCatalog(event.target.value)}>
              {catalog.map((item) => (
                <option key={item.key} value={item.key}>{item.name}</option>
              ))}
              <option value={CUSTOM_PROVIDER}>自定义</option>
            </Select>
          </Field>
          {selectedCatalog?.doc_url ? (
            <p className="muted">
              文档：<a href={selectedCatalog.doc_url} target="_blank" rel="noreferrer">{selectedCatalog.doc_url}</a>
            </p>
          ) : null}
          <Field label="名称"><TextInput value={providerName} onChange={(event) => setProviderName(event.target.value)} required /></Field>
          {isCustom ? (
            <>
              <Field label="类型">
                <Select value={providerType} onChange={(event) => setProviderType(event.target.value as ProviderType)}>
                  {providerTypes.map((type) => <option key={type} value={type}>{type}</option>)}
                </Select>
              </Field>
              <Field label="Base URL"><TextInput value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} /></Field>
            </>
          ) : (
            <Field label="Base URL"><TextInput value={baseUrl} readOnly /></Field>
          )}
          <Field label="API Key"><TextInput type="password" value={apiKey} onChange={(event) => setApiKey(event.target.value)} /></Field>
          {isCustom ? (
            <>
              <Field label="默认 Chat 模型"><TextInput value={chatModel} onChange={(event) => setChatModel(event.target.value)} /></Field>
              <Field label="默认 Embedding 模型"><TextInput value={embeddingModel} onChange={(event) => setEmbeddingModel(event.target.value)} /></Field>
            </>
          ) : (
            <>
              <Field label="默认 Chat 模型">
                <Select value={chatModel} onChange={(event) => setChatModel(event.target.value)}>
                  <option value="">不设置</option>
                  {chatModels.map((m) => <option key={m.name} value={m.name}>{m.name}</option>)}
                </Select>
              </Field>
              <Field label="默认 Embedding 模型">
                <Select value={embeddingModel} onChange={(event) => setEmbeddingModel(event.target.value)}>
                  <option value="">不设置</option>
                  {embeddingModels.map((m) => <option key={m.name} value={m.name}>{m.name}</option>)}
                </Select>
              </Field>
            </>
          )}
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
              {memoryTypeOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
            </Select>
          </Field>
          <Field label="标题"><TextInput value={memoryTitle} onChange={(event) => setMemoryTitle(event.target.value)} /></Field>
          <Field label="内容"><TextArea value={memoryContent} onChange={(event) => setMemoryContent(event.target.value)} required /></Field>
        </form>
      </Modal>

      <Modal
        open={toolOpen}
        title={editingToolId ? '编辑 HTTP Tool' : '新增 HTTP Tool'}
        onClose={() => { setToolOpen(false); setEditingToolId(0); }}
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
          {editingToolId ? (
            <>
              <Field label="测试输入 JSON"><TextArea value={toolTestInput} onChange={(event) => setToolTestInput(event.target.value)} /></Field>
              <Button type="button" onClick={() => void testTool(editingToolId)}><FlaskConical size={15} />运行测试</Button>
              {toolTestResult ? <pre className="code-box">{toolTestResult}</pre> : null}
            </>
          ) : null}
        </form>
      </Modal>

      <Modal
        open={skillOpen}
        title={editingSkillId ? '编辑 Skill' : '新增 Skill'}
        onClose={() => { setSkillOpen(false); setEditingSkillId(0); }}
        footer={
          <>
            <Button type="button" onClick={() => setSkillOpen(false)}>取消</Button>
            <Button form="create-skill-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-skill-form" className="form-stack" onSubmit={(event) => void createSkill(event)}>
          <Field label="名称"><TextInput value={skillName} onChange={(event) => setSkillName(event.target.value)} required /></Field>
          <Field label="描述"><TextArea value={skillDescription} onChange={(event) => setSkillDescription(event.target.value)} required /></Field>
          <Field label="Source Type">
            <Select value={skillSourceType} onChange={(event) => setSkillSourceType(event.target.value as 'inline' | 'local_path')}>
              <option value="inline">inline</option>
              <option value="local_path">local_path</option>
            </Select>
          </Field>
          {skillSourceType === 'inline' ? (
            <Field label="SKILL.md 内容"><TextArea value={skillContent} onChange={(event) => setSkillContent(event.target.value)} required /></Field>
          ) : (
            <Field label="Bundle Path"><TextInput value={skillBundlePath} onChange={(event) => setSkillBundlePath(event.target.value)} required /></Field>
          )}
          <Field label="Tags" hint="多个标签用英文逗号分隔"><TextInput value={skillTags} onChange={(event) => setSkillTags(event.target.value)} placeholder="review, repo, safety" /></Field>
        </form>
      </Modal>

      <Modal
        open={policyOpen}
        title="新增 Tool Policy"
        onClose={() => setPolicyOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setPolicyOpen(false)}>取消</Button>
            <Button form="create-policy-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-policy-form" className="form-stack" onSubmit={(event) => void createPolicy(event)}>
          <Field label="名称"><TextInput value={policyName} onChange={(event) => setPolicyName(event.target.value)} required /></Field>
          <Field label="需审批风险等级">
            <Select
              multiple
              value={policyRisks}
              onChange={(event) => setPolicyRisks(Array.from(event.target.selectedOptions).map((option) => option.value))}
            >
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
            </Select>
          </Field>
          <Field label="Host Allowlist" hint="多个 host 用英文逗号分隔">
            <TextInput value={policyAllowedHosts} onChange={(event) => setPolicyAllowedHosts(event.target.value)} placeholder="api.example.com" />
          </Field>
          <Field label="超时毫秒"><TextInput type="number" min={1000} max={600000} value={policyTimeoutMS} onChange={(event) => setPolicyTimeoutMS(Number(event.target.value))} /></Field>
          <Field label="最大输出字节"><TextInput type="number" min={1024} value={policyMaxOutputBytes} onChange={(event) => setPolicyMaxOutputBytes(Number(event.target.value))} /></Field>
        </form>
      </Modal>

      <Modal
        open={packOpen}
        title="新增 Tool Pack"
        onClose={() => setPackOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setPackOpen(false)}>取消</Button>
            <Button form="create-pack-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-pack-form" className="form-stack" onSubmit={(event) => void createPack(event)}>
          <Field label="名称"><TextInput value={packName} onChange={(event) => setPackName(event.target.value)} required /></Field>
          <Field label="描述"><TextArea value={packDescription} onChange={(event) => setPackDescription(event.target.value)} /></Field>
        </form>
      </Modal>

      <Modal
        open={mcpOpen}
        title="新增 MCP Server"
        onClose={() => setMcpOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setMcpOpen(false)}>取消</Button>
            <Button form="create-mcp-form" tone="primary">保存</Button>
          </>
        }
      >
        <form id="create-mcp-form" className="form-stack" onSubmit={(event) => void createMcpServer(event)}>
          <Field label="名称"><TextInput value={mcpName} onChange={(event) => setMcpName(event.target.value)} required /></Field>
          <Field label="Transport">
            <Select value={mcpTransport} onChange={(event) => setMcpTransport(event.target.value as 'sse' | 'stdio')}>
              <option value="sse">SSE</option>
              <option value="stdio">stdio</option>
            </Select>
          </Field>
          {mcpTransport === 'sse' ? (
            <Field label="Endpoint URL"><TextInput value={mcpEndpoint} onChange={(event) => setMcpEndpoint(event.target.value)} placeholder="http://localhost:3333" required /></Field>
          ) : (
            <>
              <Field label="Command"><TextInput value={mcpCommand} onChange={(event) => setMcpCommand(event.target.value)} placeholder="node" required /></Field>
              <Field label="Args"><TextInput value={mcpArgs} onChange={(event) => setMcpArgs(event.target.value)} placeholder="server.js --stdio" /></Field>
            </>
          )}
        </form>
      </Modal>

      <Toast message={message} tone="good" />
    </div>
  );
}

export function SettingsPage() { return <ManagementPage view="settings" />; }
export function ToolsPage() { return <ManagementPage view="tools" />; }
export function SkillsPage() { return <ManagementPage view="skills" />; }
