import { useMemo, useState, type ReactNode } from 'react';
import { Bot, ChevronDown, ChevronRight, Crosshair, Database, FileJson, GitBranch, Layers, ListChecks, MessageSquare, PlugZap, Plus, ShieldCheck, Sparkles, Trash2, Wrench } from 'lucide-react';
import { Button, Field, Select, StatusBadge, TextArea, TextInput } from '../../../../components/ui';
import type { KnowledgeBase, MCPServer, ModelProvider, Skill, ToolDefinition, Workflow } from '../../../../types/api';
import { prettyJson } from '../../../../utils/format';
import { agentModeFromConfig, patchAgentMode, stringArray } from '../../config';
import type { AgentMode } from '../../types';
import { useAgentFormValues } from './useAgentFormValues';
import { useWatchAgentFormChange } from './useWatchAgentFormChange';

type ModuleId = 'mode' | 'model' | 'prompt' | 'tools' | 'skills' | 'knowledge' | 'mcp' | 'sub_agents' | 'memory' | 'planning' | 'reflection' | 'policy' | 'output';

interface AgentLoopFormProps {
  config: Record<string, unknown>;
  providers: ModelProvider[];
  tools: ToolDefinition[];
  skills: Skill[];
  knowledgeBases: KnowledgeBase[];
  mcpServers: MCPServer[];
  callableWorkflows: Workflow[];
  updateConfig: (patch: Record<string, unknown>) => void;
  updateJSON: (key: string, raw: string) => void;
  addReferencedNode?: (kind: 'http_tool' | 'mcp_tool' | 'knowledge_retrieval' | 'agent_loop', id: number) => void;
}

const modeOptions: Array<{ value: AgentMode; label: string; group: 'recommended' | 'advanced'; summary: string }> = [
  { value: 'react', label: 'ReAct', group: 'recommended', summary: '多轮 Thought/Action/Observation 工具调用' },
  { value: 'plan_execute', label: 'Plan & Execute', group: 'recommended', summary: '先规划，再按步骤执行并记录 plan' },
  { value: 'reflect', label: 'Reflection', group: 'recommended', summary: '输出前或失败后启用自我修正' },
  { value: 'action', label: 'Action', group: 'advanced', summary: '前端映射为 react + max_iterations=1' },
  { value: 'reasoning_action', label: 'Reasoning Action', group: 'advanced', summary: '前端映射为 react，并保存 reasoning_profile' },
  { value: 'supervisor', label: 'Supervisor', group: 'advanced', summary: '面向子 Agent 和 workflow team 调度' },
];

function moduleTone(enabled: boolean) {
  return enabled ? 'info' : 'neutral';
}

function selectedNames<T extends { id: number; name: string }>(items: T[], ids: number[]) {
  if (ids.length === 0) return '未选择';
  return ids.map((id) => items.find((item) => item.id === id)?.name ?? `#${id}`).join(', ');
}

function toggleNumber(list: number[], id: number) {
  return list.includes(id) ? list.filter((item) => item !== id) : [...list, id];
}

function ModuleCard({
  id,
  title,
  summary,
  icon,
  status,
  expanded,
  onToggle,
  children,
}: {
  id: ModuleId;
  title: string;
  summary: string;
  icon: ReactNode;
  status: ReactNode;
  expanded: boolean;
  onToggle: (id: ModuleId) => void;
  children: ReactNode;
}) {
  return (
    <section className={`agent-module-card ${expanded ? 'expanded' : ''}`}>
      <button className="agent-module-head" type="button" onClick={() => onToggle(id)}>
        <span className="agent-module-icon">{icon}</span>
        <span className="agent-module-copy min-w-0">
          <strong className="truncate">{title}</strong>
          <span className="truncate">{summary}</span>
        </span>
        <span className="agent-module-status">{status}</span>
        {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
      </button>
      {expanded ? <div className="agent-module-body">{children}</div> : null}
    </section>
  );
}

function CardSelectList<T extends { id: number; name: string; description?: string; last_validation_error?: string }>({
  items,
  selectedIds,
  emptyLabel,
  onChange,
  onLocate,
}: {
  items: T[];
  selectedIds: number[];
  emptyLabel: string;
  onChange: (ids: number[]) => void;
  onLocate?: (id: number) => void;
}) {
  const selected = selectedIds.map((id) => {
    const item = items.find((entry) => entry.id === id);
    return {
      id,
      name: item?.name ?? `#${id}`,
      description: item?.description ?? '',
      validationError: item?.last_validation_error ?? '',
    };
  });
  return (
    <div className="module-card-list">
      {selected.length === 0 ? <p className="muted">{emptyLabel}</p> : null}
      {selected.map((item) => (
        <article className="module-mini-card" key={item.id}>
          <div className="min-w-0">
            <strong className="truncate">{item.name}</strong>
            <span>{item.description || `#${item.id}`}</span>
            {item.validationError ? <span className="module-error-text">{item.validationError}</span> : null}
          </div>
          <div className="module-mini-actions">
            {onLocate ? <button type="button" title="生成并定位独立节点" onClick={() => onLocate(item.id)}><Crosshair size={14} /></button> : null}
            <button type="button" title="移除" onClick={() => onChange(selectedIds.filter((id) => id !== item.id))}><Trash2 size={14} /></button>
          </div>
        </article>
      ))}
      <Select value="" onChange={(event) => {
        const id = Number(event.target.value);
        if (id > 0) onChange(toggleNumber(selectedIds, id));
      }}>
        <option value="">添加...</option>
        {items.map((item) => <option key={item.id} value={item.id}>{selectedIds.includes(item.id) ? '移除 ' : '添加 '}{item.name}</option>)}
      </Select>
    </div>
  );
}

export function AgentLoopForm({ config, providers, tools, skills, knowledgeBases, mcpServers, callableWorkflows, updateConfig, updateJSON, addReferencedNode }: AgentLoopFormProps) {
  const values = useAgentFormValues(config);
  const onChange = useWatchAgentFormChange(updateConfig);
  const [expandedModules, setExpandedModules] = useState<Set<ModuleId>>(() => new Set(['mode', 'model', 'tools']));
  const [schemaDraft, setSchemaDraft] = useState(() => prettyJson(config.output_schema_json ?? {}));
  const [schemaError, setSchemaError] = useState('');
  const mode = values.mode;
  const modeMeta = modeOptions.find((item) => item.value === mode) ?? modeOptions[0];
  const toolIds = values.toolIds;
  const skillIds = values.skillIds;
  const knowledgeIds = values.knowledgeIds;
  const mcpIds = values.mcpServerIds;
  const subAgentIds = values.callWorkflowIds;
  const totalCallable = toolIds.length + knowledgeIds.length + mcpIds.length + subAgentIds.length;
  const selectedProvider = providers.find((provider) => provider.id === values.providerId);
  const moduleOpen = useMemo(() => ({
    mode: expandedModules.has('mode'),
    model: expandedModules.has('model'),
    prompt: expandedModules.has('prompt'),
    tools: expandedModules.has('tools'),
    skills: expandedModules.has('skills'),
    knowledge: expandedModules.has('knowledge'),
    mcp: expandedModules.has('mcp'),
    sub_agents: expandedModules.has('sub_agents'),
    memory: expandedModules.has('memory'),
    planning: expandedModules.has('planning'),
    reflection: expandedModules.has('reflection'),
    policy: expandedModules.has('policy'),
    output: expandedModules.has('output'),
  }), [expandedModules]);

  function toggleModule(id: ModuleId) {
    setExpandedModules((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
    onChange({ _ui: { ...((config._ui as Record<string, unknown> | undefined) ?? {}), selectedModuleId: id } });
  }

  function applyMode(nextMode: AgentMode) {
    onChange(patchAgentMode(nextMode, config));
    if (nextMode === 'plan_execute') setExpandedModules((current) => new Set([...current, 'planning']));
    if (nextMode === 'reflect') setExpandedModules((current) => new Set([...current, 'reflection']));
    if (nextMode === 'supervisor') setExpandedModules((current) => new Set([...current, 'sub_agents']));
  }

  function updateSchema(raw: string) {
    setSchemaDraft(raw);
    try {
      const value = raw.trim() ? JSON.parse(raw) as unknown : {};
      onChange({ output_schema_json: value });
      setSchemaError('');
    } catch {
      setSchemaError('当前 Output 模块的 JSON Schema 格式不正确');
    }
  }

  return (
    <div className="agent-module-form">
      <ModuleCard id="mode" title="Profile & Mode" summary={modeMeta.summary} icon={<Sparkles size={16} />} status={<StatusBadge tone="info">{modeMeta.label}</StatusBadge>} expanded={moduleOpen.mode} onToggle={toggleModule}>
        <div className="agent-mode-grid">
          {modeOptions.map((option) => (
            <button key={option.value} className={`agent-mode-option ${mode === option.value ? 'active' : ''}`} type="button" onClick={() => applyMode(option.value)}>
              <strong>{option.label}</strong>
              <span>{option.summary}</span>
              <small>{option.group === 'recommended' ? '推荐' : 'Advanced'}</small>
            </button>
          ))}
        </div>
      </ModuleCard>

      <ModuleCard id="model" title="Model" summary={`${selectedProvider?.name ?? '继承 Profile'} / ${values.model || '默认模型'}`} icon={<Bot size={16} />} status={<StatusBadge tone={values.providerId > 0 || values.model ? 'info' : 'neutral'}>{values.providerId > 0 ? 'override' : 'inherit'}</StatusBadge>} expanded={moduleOpen.model} onToggle={toggleModule}>
        <Field label="Provider">
          <Select value={values.providerId} onChange={(event) => onChange({ provider_id: Number(event.target.value) })}>
            <option value={0}>继承 Profile 默认 Provider</option>
            {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
          </Select>
        </Field>
        <Field label="模型">
          <TextInput value={values.model} onChange={(event) => onChange({ model: event.target.value })} placeholder="留空使用默认模型" />
        </Field>
        <Field label="Temperature">
          <TextInput type="number" min={0} max={2} step={0.1} value={values.temperature} onChange={(event) => onChange({ temperature: Number(event.target.value) })} />
        </Field>
      </ModuleCard>

      <ModuleCard id="prompt" title="Prompt" summary={values.taskTemplate || '未设置任务模板'} icon={<MessageSquare size={16} />} status={<StatusBadge tone={values.taskTemplate ? 'info' : 'warn'}>{values.taskTemplate ? 'ready' : 'missing'}</StatusBadge>} expanded={moduleOpen.prompt} onToggle={toggleModule}>
        <Field label="System Prompt">
          <TextArea value={values.systemPrompt} onChange={(event) => onChange({ system_prompt: event.target.value })} />
        </Field>
        <Field label="任务模板" hint="支持 {{sys.query}} 和 {{node_id.field}}">
          <TextArea value={values.taskTemplate} onChange={(event) => onChange({ task_template: event.target.value })} />
        </Field>
      </ModuleCard>

      <ModuleCard id="tools" title="Tools" summary={selectedNames(tools, toolIds)} icon={<Wrench size={16} />} status={<StatusBadge tone={moduleTone(toolIds.length > 0)}>{toolIds.length}</StatusBadge>} expanded={moduleOpen.tools} onToggle={toggleModule}>
        <CardSelectList items={tools} selectedIds={toolIds} emptyLabel="还没有内部 HTTP Tool。" onChange={(ids) => onChange({ tool_ids: ids })} onLocate={(id) => addReferencedNode?.('http_tool', id)} />
        <Field label="代码执行工具">
          <Select value={config.code_execution_enabled ? 'enabled' : 'disabled'} onChange={(event) => onChange({ code_execution_enabled: event.target.value === 'enabled' })}>
            <option value="disabled">Disabled</option>
            <option value="enabled">Enabled</option>
          </Select>
        </Field>
      </ModuleCard>

      <ModuleCard id="skills" title="Skills" summary={selectedNames(skills, skillIds)} icon={<Sparkles size={16} />} status={<StatusBadge tone={moduleTone(skillIds.length > 0)}>{skillIds.length}</StatusBadge>} expanded={moduleOpen.skills} onToggle={toggleModule}>
        <CardSelectList items={skills} selectedIds={skillIds} emptyLabel="还没有可用 Skill。" onChange={(ids) => onChange({ skill_ids: ids })} />
        <Field label="加载模式">
          <Select value={values.skillLoadingMode} onChange={(event) => onChange({ skill_loading_mode: event.target.value })}>
            <option value="metadata_only">Metadata Only</option>
            <option value="search">Search</option>
          </Select>
        </Field>
      </ModuleCard>

      <ModuleCard id="knowledge" title="Knowledge" summary={selectedNames(knowledgeBases, knowledgeIds)} icon={<Database size={16} />} status={<StatusBadge tone={moduleTone(knowledgeIds.length > 0)}>{knowledgeIds.length}</StatusBadge>} expanded={moduleOpen.knowledge} onToggle={toggleModule}>
        <CardSelectList items={knowledgeBases} selectedIds={knowledgeIds} emptyLabel="还没有知识库工具。" onChange={(ids) => onChange({ knowledge_ids: ids })} onLocate={(id) => addReferencedNode?.('knowledge_retrieval', id)} />
        <Field label="知识库 Top K">
          <TextInput type="number" min={1} max={20} value={Number(config.knowledge_top_k ?? 5)} onChange={(event) => onChange({ knowledge_top_k: Number(event.target.value) })} />
        </Field>
        <Field label="知识库模式">
          <Select value={String(config.knowledge_mode ?? 'keyword')} onChange={(event) => onChange({ knowledge_mode: event.target.value })}>
            <option value="keyword">Keyword</option>
            <option value="vector">Vector</option>
            <option value="hybrid">Hybrid</option>
          </Select>
        </Field>
      </ModuleCard>

      <ModuleCard id="mcp" title="MCP" summary={selectedNames(mcpServers, mcpIds)} icon={<PlugZap size={16} />} status={<StatusBadge tone={moduleTone(mcpIds.length > 0)}>{mcpIds.length}</StatusBadge>} expanded={moduleOpen.mcp} onToggle={toggleModule}>
        <CardSelectList items={mcpServers} selectedIds={mcpIds} emptyLabel="还没有 MCP Server。" onChange={(ids) => onChange({ mcp_server_ids: ids })} onLocate={(id) => addReferencedNode?.('mcp_tool', id)} />
      </ModuleCard>

      <ModuleCard id="sub_agents" title="Sub Agents" summary={selectedNames(callableWorkflows, subAgentIds)} icon={<GitBranch size={16} />} status={<StatusBadge tone={moduleTone(subAgentIds.length > 0)}>{subAgentIds.length}</StatusBadge>} expanded={moduleOpen.sub_agents} onToggle={toggleModule}>
        <CardSelectList items={callableWorkflows} selectedIds={subAgentIds} emptyLabel="还没有可调用 Agent。" onChange={(ids) => onChange({ call_workflow_ids: ids })} onLocate={(id) => addReferencedNode?.('agent_loop', id)} />
		<Field label="动态子 Agent">
			<Select value={config.allow_inline_agents ? 'enabled' : 'disabled'} onChange={(event) => onChange({ allow_inline_agents: event.target.value === 'enabled' })}>
				<option value="disabled">Disabled</option>
				<option value="enabled">Enabled</option>
			</Select>
		</Field>
		<Field label="最大并发子 Agent">
			<TextInput type="number" min={1} max={64} value={Number(config.max_parallel_sub_agents ?? 8)} onChange={(event) => onChange({ max_parallel_sub_agents: Number(event.target.value) })} />
		</Field>
        <Field label="Agent 调用深度">
          <TextInput type="number" min={1} max={5} value={Number(config.max_workflow_call_depth ?? 3)} onChange={(event) => onChange({ max_workflow_call_depth: Number(event.target.value) })} />
        </Field>
      </ModuleCard>

      <ModuleCard id="memory" title="Memory" summary={config.memory_enabled ? '读写长期记忆' : '关闭'} icon={<Layers size={16} />} status={<StatusBadge tone={moduleTone(Boolean(config.memory_enabled))}>{config.memory_enabled ? 'on' : 'off'}</StatusBadge>} expanded={moduleOpen.memory} onToggle={toggleModule}>
        <Field label="记忆工具">
          <Select value={config.memory_enabled ? 'enabled' : 'disabled'} onChange={(event) => onChange({ memory_enabled: event.target.value === 'enabled' })}>
            <option value="disabled">Disabled</option>
            <option value="enabled">Enabled</option>
          </Select>
        </Field>
        <Field label="Memory Policy JSON">
          <TextArea value={prettyJson(config.memory_policy_json ?? {})} onChange={(event) => updateJSON('memory_policy_json', event.target.value)} />
        </Field>
      </ModuleCard>

      <ModuleCard id="planning" title="Planning" summary={agentModeFromConfig(config) === 'plan_execute' ? '启用 plan step' : '未启用'} icon={<ListChecks size={16} />} status={<StatusBadge tone={moduleTone(mode === 'plan_execute')}>{mode === 'plan_execute' ? 'on' : 'off'}</StatusBadge>} expanded={moduleOpen.planning} onToggle={toggleModule}>
        <Field label="规划">
          <Select value={mode === 'plan_execute' ? 'enabled' : 'disabled'} onChange={(event) => applyMode(event.target.value === 'enabled' ? 'plan_execute' : 'react')}>
            <option value="disabled">Disabled</option>
            <option value="enabled">Enabled</option>
          </Select>
        </Field>
        <Field label="最大轮次">
          <TextInput type="number" min={1} max={50} value={Number(config.max_iterations ?? 8)} onChange={(event) => onChange({ max_iterations: Number(event.target.value) })} />
        </Field>
      </ModuleCard>

      <ModuleCard id="reflection" title="Reflection" summary={config.reflection_enabled ? '失败或输出前修正' : '关闭'} icon={<ShieldCheck size={16} />} status={<StatusBadge tone={moduleTone(Boolean(config.reflection_enabled))}>{config.reflection_enabled ? 'on' : 'off'}</StatusBadge>} expanded={moduleOpen.reflection} onToggle={toggleModule}>
        <Field label="反思修正">
          <Select value={config.reflection_enabled ? 'enabled' : 'disabled'} onChange={(event) => onChange({ reflection_enabled: event.target.value === 'enabled' })}>
            <option value="disabled">Disabled</option>
            <option value="enabled">Enabled</option>
          </Select>
        </Field>
      </ModuleCard>

      <ModuleCard id="policy" title="Policy" summary={`${stringArray(config.require_approval_for_risk, ['high']).join(', ')} risk approval`} icon={<ShieldCheck size={16} />} status={<StatusBadge tone="warn">audit</StatusBadge>} expanded={moduleOpen.policy} onToggle={toggleModule}>
        <Field label="需审批风险等级">
          <Select multiple value={stringArray(config.require_approval_for_risk, ['high'])} onChange={(event) => onChange({ require_approval_for_risk: Array.from(event.target.selectedOptions).map((option) => option.value) })}>
            <option value="low">Low</option>
            <option value="medium">Medium</option>
            <option value="high">High</option>
          </Select>
        </Field>
        <Field label="工具超时毫秒">
          <TextInput type="number" min={1000} max={600000} step={1000} value={Number(config.max_tool_timeout_ms ?? 30000)} onChange={(event) => onChange({ max_tool_timeout_ms: Number(event.target.value) })} />
        </Field>
        <Field label="工具输出字节">
          <TextInput type="number" min={1024} max={2097152} step={1024} value={Number(config.max_tool_output_bytes ?? 524288)} onChange={(event) => onChange({ max_tool_output_bytes: Number(event.target.value) })} />
        </Field>
        <Field label="允许 Host">
          <TextInput value={stringArray(config.allowed_hosts).join(', ')} onChange={(event) => onChange({ allowed_hosts: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) })} placeholder="api.example.com, mcp.example.com" />
        </Field>
        <Field label="Tool Policy JSON">
          <TextArea value={prettyJson(config.tool_policy_json ?? {})} onChange={(event) => updateJSON('tool_policy_json', event.target.value)} />
        </Field>
      </ModuleCard>

      <ModuleCard id="output" title="Output" summary={String(config.output_mode ?? 'final_answer')} icon={<FileJson size={16} />} status={<StatusBadge tone={schemaError ? 'bad' : 'info'}>{schemaError ? 'invalid' : 'schema'}</StatusBadge>} expanded={moduleOpen.output} onToggle={toggleModule}>
        <Field label="输出模式">
          <Select value={String(config.output_mode ?? 'final_answer')} onChange={(event) => onChange({ output_mode: event.target.value, return_intermediate_steps: event.target.value === 'full' })}>
            <option value="final_answer">Final Answer</option>
            <option value="full">Full Trace</option>
          </Select>
        </Field>
        <Field label="输出 Schema JSON">
          <TextArea value={schemaDraft} onChange={(event) => updateSchema(event.target.value)} />
        </Field>
        {schemaError ? <p className="module-error-text">{schemaError}</p> : null}
      </ModuleCard>

      <Button className="agent-module-add" onClick={() => setExpandedModules(new Set(['mode', 'model', 'prompt', 'tools', 'skills', 'knowledge', 'mcp', 'sub_agents', 'memory', 'planning', 'reflection', 'policy', 'output']))}>
        <Plus size={15} />
        展开全部模块
      </Button>
      <div className="agent-module-summary">
        <StatusBadge tone="info">{totalCallable} callable</StatusBadge>
        <StatusBadge tone={skillIds.length > 0 ? 'good' : 'neutral'}>{skillIds.length} skills</StatusBadge>
        <StatusBadge tone={modeMeta.group === 'recommended' ? 'good' : 'warn'}>{modeMeta.group}</StatusBadge>
      </div>
    </div>
  );
}
