import { ChevronRight, Maximize2, Plus, Trash2, X } from 'lucide-react';
import { Button, Field, IconButton, Select, TextArea, TextInput } from '@/components/ui';
import type { KnowledgeBase, MCPServer, ModelProvider, ToolDefinition, Workflow, WorkflowTeam } from '@/types/api';
import type { CanvasNode } from '../types';

type UpdateConfig = (patch: Record<string, unknown>) => void;

export interface InspectorResources {
  providers: ModelProvider[];
  knowledgeBases: KnowledgeBase[];
  tools: ToolDefinition[];
  mcpServers: MCPServer[];
  workflows: Workflow[];
  teams: WorkflowTeam[];
}

function SwitchBuilder({ node, nodes, updateConfig }: { node: CanvasNode; nodes: CanvasNode[]; updateConfig: UpdateConfig }) {
  const config = node.data.config as Record<string, unknown>;
  const conditions = Array.isArray(config.conditions) ? config.conditions as Array<{ expr: string; target: string }> : [];
  const targets = nodes.filter((item) => item.id !== node.id);
  const setRow = (index: number, patch: Partial<{ expr: string; target: string }>) => updateConfig({ conditions: conditions.map((row, i) => i === index ? { ...row, ...patch } : row) });
  const add = () => updateConfig({ conditions: [...conditions, { expr: 'default', target: targets[0]?.id ?? '' }] });
  const remove = (index: number) => updateConfig({ conditions: conditions.filter((_, i) => i !== index) });
  return (
    <div style={{ display: 'grid', gap: 10 }}>
      {conditions.map((row, index) => (
        <div className="condition-row" key={`${row.expr}-${index}`}>
          <TextInput value={row.expr} onChange={(e) => setRow(index, { expr: e.target.value })} placeholder={'{{sys.query}} != "" 或 default'} />
          <Select value={row.target} onChange={(e) => setRow(index, { target: e.target.value })}>
            <option value="">选择目标</option>
            {targets.map((target) => <option key={target.id} value={target.id}>{target.data.label} · {target.id}</option>)}
          </Select>
          <IconButton aria-label="删除条件" onClick={() => remove(index)}><Trash2 size={15} /></IconButton>
        </div>
      ))}
      <Button type="button" onClick={add}><Plus size={16} /> 添加条件</Button>
    </div>
  );
}

function ChipArrayField({ values, onChange }: { values: string[]; onChange: (values: string[]) => void }) {
  const add = () => { const value = window.prompt('输入新的条目'); if (value?.trim()) onChange([...values, value.trim()]); };
  return <div className="chip-editor">{values.map((value, index) => <span className="chip" key={`${value}-${index}`}>{value}<button type="button" aria-label={`删除 ${value}`} onClick={() => onChange(values.filter((_, i) => i !== index))}><X size={12} /></button></span>)}<button type="button" className="chip chip-add" onClick={add}><Plus size={12} /> 添加</button></div>;
}

function KVEditor({ value, onChange }: { value: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void }) {
  const rows = Object.entries(value);
  const update = (index: number, key: string, nextValue: string) => onChange(Object.fromEntries(rows.map(([k,v], i) => i === index ? [key, nextValue] : [k,v])));
  return <div className="structured-editor">{rows.map(([key, val], index) => <div className="kv-row" key={`${key}-${index}`}><TextInput value={key} onChange={(e) => update(index, e.target.value, String(val))} placeholder="key" /><TextInput value={String(val)} onChange={(e) => update(index, key, e.target.value)} placeholder="{{variable}}" /><IconButton type="button" onClick={() => onChange(Object.fromEntries(rows.filter((_, i) => i !== index)))}><Trash2 size={14} /></IconButton></div>)}<Button type="button" size="small" onClick={() => onChange({ ...value, [`field_${rows.length + 1}`]: '' })}><Plus size={14} /> 添加映射</Button></div>;
}

function SchemaBuilder({ value, onChange }: { value: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void }) {
  const properties = (value.properties && typeof value.properties === 'object' ? value.properties : {}) as Record<string, { type?: string }>;
  const required = Array.isArray(value.required) ? value.required as string[] : [];
  const rows = Object.entries(properties);
  const commit = (nextRows: Array<[string, { type?: string }]>, nextRequired = required) => onChange({ ...value, type: 'object', properties: Object.fromEntries(nextRows), required: nextRequired });
  return <div className="structured-editor">{rows.map(([name, schema], index) => <div className="schema-row" key={`${name}-${index}`}><TextInput value={name} onChange={(e) => commit(rows.map((row,i) => i === index ? [e.target.value, row[1]] : row), required.map(r => r === name ? e.target.value : r))} placeholder="字段名" /><Select value={schema.type ?? 'string'} onChange={(e) => commit(rows.map((row,i) => i === index ? [row[0], { type: e.target.value }] : row))}><option>string</option><option>number</option><option>boolean</option><option>array</option><option>object</option></Select><label className="required-check"><input type="checkbox" checked={required.includes(name)} onChange={(e) => commit(rows, e.target.checked ? [...required, name] : required.filter(r => r !== name))} /> 必填</label><IconButton type="button" onClick={() => commit(rows.filter((_,i) => i !== index), required.filter(r => r !== name))}><Trash2 size={14} /></IconButton></div>)}<Button type="button" size="small" onClick={() => commit([...rows, [`field_${rows.length + 1}`, { type: 'string' }]])}><Plus size={14} /> 添加字段</Button></div>;
}

function numberValue(value: unknown): string {
  return typeof value === 'number' && value > 0 ? String(value) : '';
}

export function InspectorPanel({ node, nodes, resources, onRename, onUpdateConfig, onFocus, onCollapse }: { node: CanvasNode | null; nodes: CanvasNode[]; resources: InspectorResources; onRename: (label: string) => void; onUpdateConfig: UpdateConfig; onFocus: () => void; onCollapse?: () => void }) {
  if (!node) return <aside className="canvas-panel canvas-inspector deckle-paper"><div className="panel-header"><h3>Inspector</h3>{onCollapse ? <button className="ink-collapse" onClick={onCollapse} aria-label="折叠检查器">›</button> : null}</div><div className="panel-body"><p style={{ color: 'var(--text-muted)' }}>选择一个节点后编辑配置。</p></div></aside>;
  const config = node.data.config;
  const configRecord = config as Record<string, unknown>;
  const setField = (key: string, value: unknown) => onUpdateConfig({ [key]: value });
  return (
    <aside className="canvas-panel canvas-inspector deckle-paper pinned-paper">
      <span className="paperclip inspector-clip" aria-hidden="true" /><div className="paper-ribbon inspector-ribbon">Nota del modulo</div>
      <div className="panel-header"><div><span className="folio-index">Inspector · {node.data.nodeType}</span><h3>{node.data.label}</h3></div><div className="panel-actions"><IconButton aria-label="放大配置" onClick={onFocus}><Maximize2 size={16} /></IconButton>{onCollapse ? <button className="ink-collapse" onClick={onCollapse} aria-label="折叠检查器">›</button> : null}</div></div>
      <div className="panel-body scroll-surface" style={{ display: 'grid', gap: 14 }}>
        <Field label="节点名称"><TextInput value={node.data.label} onChange={(e) => onRename(e.target.value)} /></Field>
        {node.data.nodeType === 'agent_loop' ? <>
          <Field label="Agent 模式"><Select value={String((config as Record<string, unknown>).mode ?? 'react')} onChange={(e) => setField('mode', e.target.value)}><option value="react">ReAct</option><option value="plan_execute">Plan & Execute</option></Select></Field>
          <Field label="最大迭代"><TextInput type="number" value={Number((config as Record<string, unknown>).max_iterations ?? 8)} onChange={(e) => setField('max_iterations', Number(e.target.value))} /></Field>
          <Field label="记忆"><Select value={String(Boolean((config as Record<string, unknown>).memory_enabled))} onChange={(e) => setField('memory_enabled', e.target.value === 'true')}><option value="false">关闭</option><option value="true">开启</option></Select></Field>
          <Field label="反思"><Select value={String(Boolean((config as Record<string, unknown>).reflection_enabled))} onChange={(e) => setField('reflection_enabled', e.target.value === 'true')}><option value="false">关闭</option><option value="true">开启</option></Select></Field>
          <Field label="代码执行"><Select value={String(Boolean((config as Record<string, unknown>).code_execution_enabled))} onChange={(e) => setField('code_execution_enabled', e.target.value === 'true')}><option value="false">关闭</option><option value="true">开启</option></Select></Field>
        </> : null}
        {'provider_id' in config ? <Field label="模型供应商"><Select value={numberValue(config.provider_id)} onChange={(e) => setField('provider_id', Number(e.target.value || 0))}><option value="">选择 Provider</option>{resources.providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</Select></Field> : null}
        {node.data.nodeType === 'knowledge_retrieval' || node.data.nodeType === 'agent_loop' ? <Field label="知识库"><Select value={String((Array.isArray((config as Record<string, unknown>).kb_ids) ? ((config as Record<string, unknown>).kb_ids as number[])[0] : Array.isArray((config as Record<string, unknown>).knowledge_ids) ? ((config as Record<string, unknown>).knowledge_ids as number[])[0] : '') ?? '')} onChange={(e) => setField(node.data.nodeType === 'agent_loop' ? 'knowledge_ids' : 'kb_ids', e.target.value ? [Number(e.target.value)] : [])}><option value="">选择知识库</option>{resources.knowledgeBases.map((kb) => <option key={kb.id} value={kb.id}>{kb.name}</option>)}</Select></Field> : null}
        {node.data.nodeType === 'http_tool' ? <Field label="HTTP 工具"><Select value={numberValue((config as Record<string, unknown>).tool_id)} onChange={(e) => setField('tool_id', Number(e.target.value || 0))}><option value="">选择工具</option>{resources.tools.map((tool) => <option key={tool.id} value={tool.id}>{tool.name}</option>)}</Select></Field> : null}
        {node.data.nodeType === 'mcp_tool' || node.data.nodeType === 'agent_loop' ? <Field label="MCP Server"><Select value={String((node.data.nodeType === 'agent_loop' && Array.isArray((config as Record<string, unknown>).mcp_server_ids) ? ((config as Record<string, unknown>).mcp_server_ids as number[])[0] : (config as Record<string, unknown>).server_id) ?? '')} onChange={(e) => setField(node.data.nodeType === 'agent_loop' ? 'mcp_server_ids' : 'server_id', node.data.nodeType === 'agent_loop' ? (e.target.value ? [Number(e.target.value)] : []) : Number(e.target.value || 0))}><option value="">选择 MCP Server</option>{resources.mcpServers.map((server) => <option key={server.id} value={server.id}>{server.name}</option>)}</Select></Field> : null}
        {node.data.nodeType === 'agent_call' || node.data.nodeType === 'workflow_call' ? <Field label="目标工作流"><Select value={numberValue((config as Record<string, unknown>).workflow_id)} onChange={(e) => setField('workflow_id', Number(e.target.value || 0))}><option value="">选择工作流</option>{resources.workflows.map((workflow) => <option key={workflow.id} value={workflow.id}>{workflow.name}</option>)}</Select></Field> : null}
        {node.data.nodeType === 'team_call' ? <Field label="目标 Team"><Select value={numberValue((config as Record<string, unknown>).team_id)} onChange={(e) => setField('team_id', Number(e.target.value || 0))}><option value="">选择 Team</option>{resources.teams.map((team) => <option key={team.id} value={team.id}>{team.name}</option>)}</Select></Field> : null}
        {node.data.nodeType === 'switch' ? <Field label="分支条件"><SwitchBuilder node={node} nodes={nodes} updateConfig={onUpdateConfig} /></Field> : null}
        {'memory_types' in config ? <Field label="记忆类型"><ChipArrayField values={Array.isArray(config.memory_types) ? config.memory_types.map(String) : []} onChange={(value) => setField('memory_types', value)} /></Field> : null}
        {'banned_terms' in config ? <Field label="禁用词"><ChipArrayField values={Array.isArray(config.banned_terms) ? config.banned_terms.map(String) : []} onChange={(value) => setField('banned_terms', value)} /></Field> : null}
        {'input' in config && config.input && typeof config.input === 'object' ? <Field label="输入映射"><KVEditor value={config.input as Record<string, unknown>} onChange={(value) => setField('input', value)} /></Field> : null}
        {node.data.nodeType === 'json_output' ? <Field label="输出结构"><SchemaBuilder value={configRecord.schema && typeof configRecord.schema === 'object' ? configRecord.schema as Record<string, unknown> : {}} onChange={(value) => setField('schema', value)} /></Field> : null}
        {'template' in config ? <Field label="模板"><TextArea value={String(config.template ?? '')} onChange={(e) => setField('template', e.target.value)} /></Field> : null}
        {'content' in config ? <Field label="内容"><TextArea value={String(config.content ?? '')} onChange={(e) => setField('content', e.target.value)} /></Field> : null}
        {'task_template' in config ? <Field label="任务模板"><TextArea value={String(config.task_template ?? '')} onChange={(e) => setField('task_template', e.target.value)} /></Field> : null}
        {'query' in config ? <Field label="查询模板"><TextInput value={String(config.query ?? '')} onChange={(e) => setField('query', e.target.value)} /></Field> : null}
        {'model' in config ? <Field label="模型"><TextInput value={String(config.model ?? '')} onChange={(e) => setField('model', e.target.value)} /></Field> : null}
        {'temperature' in config ? <Field label="Temperature"><TextInput type="number" step="0.1" value={Number(config.temperature ?? 0)} onChange={(e) => setField('temperature', Number(e.target.value))} /></Field> : null}
        <details className="raw-config"><summary><ChevronRight size={14} /> 原始配置 JSON <span>高级</span></summary><TextArea className="mono" value={JSON.stringify(config, null, 2)} onChange={(e) => { try { onUpdateConfig(JSON.parse(e.target.value) as Record<string, unknown>); } catch { /* keep local typing forgiving */ } }} /></details>
      </div>
    </aside>
  );
}
