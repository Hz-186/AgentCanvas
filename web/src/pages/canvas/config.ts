import type React from 'react';
import {
  Bot,
  BrainCircuit,
  Braces,
  Database,
  GitBranch,
  Globe2,
  MessageSquare,
  PlugZap,
  Save,
  Send,
  ShieldCheck,
  Sparkles,
  Workflow as WorkflowIcon,
} from 'lucide-react';
import type { NodeType } from '../../types/flow';
import type { AgentMode, CanvasNodeData } from './types';

export const DEFAULT_REFLECTION_POLICY = {
  enabled: true,
  runtime_mode: 'active',
  inline_on_hard_failure: true,
  terminal_async: true,
  max_inline_per_run: 2,
  recall_top_k: 3,
  recall_token_budget: 800,
  min_importance: 0.65,
  min_confidence: 0.7,
  allow_validated_global_fallback: true,
  reflect_on_success: 'external_or_novel',
} as const;

export const nodeMeta: Record<NodeType, { label: string; icon: React.ElementType; description: string }> = {
  begin: { label: 'Begin', icon: Sparkles, description: '读取运行输入' },
  knowledge_retrieval: { label: 'Retrieval', icon: Database, description: '从知识库检索上下文' },
  prompt: { label: 'Prompt', icon: MessageSquare, description: '组装提示词' },
  llm: { label: 'LLM', icon: BrainCircuit, description: '调用模型生成内容' },
  agent_loop: { label: 'Agent Loop', icon: Bot, description: '自治 ReAct Agent，自动调用工具并可委托子 Agent' },
  agent_call: { label: 'Agent Call', icon: WorkflowIcon, description: '静态调用另一个 Agent' },
  workflow_call: { label: 'Workflow Call', icon: WorkflowIcon, description: '兼容旧 DSL 的静态 Agent 调用' },
  team_call: { label: 'Team Call', icon: WorkflowIcon, description: '调用一个 Workflow Team 的 supervisor' },
  code_sandbox: { label: 'Code Sandbox', icon: Braces, description: '隔离执行 Python 代码' },
  message: { label: 'Message', icon: Send, description: '输出或写入会话消息' },
  memory_read: { label: 'Memory Read', icon: BrainCircuit, description: '读取长期记忆' },
  memory_write: { label: 'Memory Write', icon: Save, description: '写入或更新记忆' },
  http_tool: { label: 'HTTP Tool', icon: Globe2, description: '调用受控 HTTP 工具' },
  mcp_tool: { label: 'MCP Tool', icon: PlugZap, description: '显式调用 MCP Server 工具' },
  switch: { label: 'Switch', icon: GitBranch, description: '按条件选择分支' },
  json_output: { label: 'JSON Output', icon: Braces, description: '校验结构化输出' },
  guardrail: { label: 'Guardrail', icon: ShieldCheck, description: '检查输出规则' },
};

export const paletteNodeTypes = (Object.keys(nodeMeta) as NodeType[]).filter((type) => type !== 'workflow_call');

export function isAgentNodeType(type: NodeType) {
  return type === 'agent_loop';
}

export function isStaticAgentCallNodeType(type: NodeType) {
  return type === 'agent_call' || type === 'workflow_call';
}

export function numberArray(value: unknown): number[] {
  return Array.isArray(value) ? value.map((item) => Number(item)).filter((item) => Number.isFinite(item)) : [];
}

export function stringArray(value: unknown, fallback: string[] = []): string[] {
  return Array.isArray(value) ? value.map((item) => String(item)).filter(Boolean) : fallback;
}

export function defaultConfig(type: NodeType): CanvasNodeData['config'] {
  if (type === 'begin') return { input_schema: { query: 'string' } };
  if (type === 'knowledge_retrieval') return { kb_ids: [], top_k: 5, mode: 'keyword', query: '{{sys.query}}' };
  if (type === 'prompt') return { template: '请根据以下上下文回答用户问题：\n\n{{retrieval.context}}\n\n问题：{{sys.query}}' };
  if (type === 'llm') return { provider_id: 0, model: '', temperature: 0.7, stream: true };
  if (type === 'agent_loop') {
    return {
      mode: 'react',
      provider_id: 0,
      model: '',
      system_prompt: '你是一个严谨的 Agent。必要时调用可用工具，看到工具结果后再继续推理并给出最终答案。',
      task_template: '{{sys.query}}',
      tool_ids: [],
      skill_ids: [],
      skill_loading_mode: 'metadata_only',
      knowledge_ids: [],
      mcp_server_ids: [],
      knowledge_top_k: 5,
      knowledge_mode: 'keyword',
      call_workflow_ids: [],
      max_workflow_call_depth: 3,
      code_execution_enabled: false,
      memory_enabled: false,
      max_iterations: 8,
      max_tool_calls: 16,
      max_execution_time_ms: 120000,
		allow_inline_agents: true,
		max_parallel_sub_agents: 8,
      max_input_chars: 96000,
      temperature: 0.2,
      reflection_enabled: true,
      reflection_policy_json: { ...DEFAULT_REFLECTION_POLICY },
      require_approval_for_risk: ['high'],
      max_tool_timeout_ms: 30000,
      max_tool_output_bytes: 524288,
      allowed_hosts: [],
      tool_policy_json: {},
      memory_policy_json: {},
      context_policy_json: {},
      output_schema_json: {},
      return_intermediate_steps: true,
      output_mode: 'final_answer',
    };
  }
  if (isStaticAgentCallNodeType(type)) return { workflow_id: 0, flow_version_id: 0, input: { query: '{{sys.query}}' }, max_depth: 3 };
  if (type === 'team_call') return { team_id: 0, input: { query: '{{sys.query}}' }, max_depth: 3 };
  if (type === 'code_sandbox') return { language: 'python', code: 'print("hello from sandbox")', timeout_ms: 5000, max_output_bytes: 65536, network_enabled: false, memory_limit_mb: 128 };
  if (type === 'message') return { content: '{{llm.content}}', with_citation: true };
  if (type === 'memory_read') return { memory_types: ['profile_memory', 'summary_memory'], limit: 5 };
  if (type === 'memory_write') return { memory_type: 'summary_memory', content: '{{llm.content}}', importance: 0.5, source: 'workflow' };
  if (type === 'http_tool') return { tool_id: 0, input: { query: '{{sys.query}}' } };
  if (type === 'mcp_tool') return { server_id: 0, tool_name: '', input: { query: '{{sys.query}}' } };
  if (type === 'switch') return { conditions: [{ expr: '{{retrieval.result_count}} > 0', target: 'llm' }, { expr: 'default', target: 'message' }] };
  if (type === 'json_output') return { value: '{{llm.content}}', schema: { type: 'object' } };
  return { source: '{{llm.content}}', max_length: 4000, banned_terms: [], require_citation: false, require_json: false };
}

export function agentModeFromConfig(config: Record<string, unknown>): AgentMode {
  const mode = String(config.mode ?? 'react');
  return mode === 'plan_execute' ? 'plan_execute' : 'react';
}

export function patchAgentMode(mode: AgentMode, current: Record<string, unknown>) {
  const ui = current._ui && typeof current._ui === 'object' ? current._ui as Record<string, unknown> : {};
  const { agent_mode: _agentMode, reasoning_profile: _reasoningProfile, ...nextUI } = ui;
  return { mode, _ui: nextUI };
}

export function nodeSummaryItems(data: CanvasNodeData) {
  const config = data.config as Record<string, unknown>;
  if (data.nodeType === 'agent_loop') {
    const tools = numberArray(config.tool_ids).length;
    const skills = numberArray(config.skill_ids).length;
    const kb = numberArray(config.knowledge_ids).length;
    const mcp = numberArray(config.mcp_server_ids).length;
    const callable = numberArray(config.call_workflow_ids).length;
    return [agentModeFromConfig(config), String(config.model || 'inherit'), `${tools + kb + mcp + callable} tools`, `${skills} skills`, `${Number(config.max_iterations ?? 8)} loops`];
  }
  if (data.nodeType === 'knowledge_retrieval') return [`Top ${Number(config.top_k ?? 5)}`, String(config.mode ?? 'keyword'), `${numberArray(config.kb_ids).length} KB`];
  if (data.nodeType === 'llm') return [String(config.model || 'default'), `T ${Number(config.temperature ?? 0.7)}`, config.stream === false ? 'sync' : 'stream'];
  if (isStaticAgentCallNodeType(data.nodeType)) return [`Agent #${Number(config.workflow_id ?? 0) || '-'}`, `depth ${Number(config.max_depth ?? 3)}`];
  if (data.nodeType === 'team_call') return [`Team #${Number(config.team_id ?? 0) || '-'}`, `depth ${Number(config.max_depth ?? 3)}`];
  if (data.nodeType === 'code_sandbox') return [String(config.language ?? 'python'), `${Number(config.timeout_ms ?? 5000)}ms`, config.network_enabled ? 'network' : 'isolated'];
  if (data.nodeType === 'switch') return [`${Array.isArray(config.conditions) ? config.conditions.length : 0} routes`, 'branch'];
  if (data.nodeType === 'guardrail') return [config.require_json ? 'JSON' : 'rules', `${Number(config.max_length ?? 4000)} chars`];
  if (data.nodeType === 'json_output') return ['schema', 'structured'];
  if (data.nodeType === 'memory_read') return ['read', `${Number(config.limit ?? 5)} items`];
  if (data.nodeType === 'memory_write') return ['write', String(config.memory_type ?? 'memory')];
  if (data.nodeType === 'http_tool') return [`Tool #${Number(config.tool_id ?? 0) || '-'}`, 'HTTP'];
  if (data.nodeType === 'mcp_tool') return [`Server #${Number(config.server_id ?? 0) || '-'}`, String(config.tool_name || 'tool')];
  if (data.nodeType === 'prompt') return ['template', 'prompt'];
  if (data.nodeType === 'message') return ['output', config.with_citation ? 'citation' : 'plain'];
  return ['input', 'start'];
}
