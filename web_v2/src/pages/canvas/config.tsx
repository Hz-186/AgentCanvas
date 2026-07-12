import { Bot, BrainCircuit, Braces, Database, GitBranch, Globe2, MessageSquare, PlugZap, Save, Send, ShieldCheck, Sparkles, Workflow, type LucideIcon } from 'lucide-react';
import type { NodeType, NodeConfig } from '@/types/flow';

export interface NodeMeta {
  type: NodeType;
  label: string;
  description: string;
  icon: LucideIcon;
}

export const nodeMeta: Record<NodeType, NodeMeta> = {
  begin: { type: 'begin', label: 'Begin', description: '读取运行输入', icon: Sparkles },
  knowledge_retrieval: { type: 'knowledge_retrieval', label: 'Retrieval', description: '从知识库检索上下文', icon: Database },
  prompt: { type: 'prompt', label: 'Prompt', description: '组装提示词', icon: MessageSquare },
  llm: { type: 'llm', label: 'LLM', description: '调用模型生成内容', icon: BrainCircuit },
  agent_loop: { type: 'agent_loop', label: 'Agent Loop', description: '自治 ReAct Agent', icon: Bot },
  agent_call: { type: 'agent_call', label: 'Agent Call', description: '调用另一个工作流', icon: Workflow },
  workflow_call: { type: 'workflow_call', label: 'Workflow Call', description: '兼容旧 DSL 调用', icon: Workflow },
  team_call: { type: 'team_call', label: 'Team Call', description: '调用 Workflow Team', icon: Workflow },
  code_sandbox: { type: 'code_sandbox', label: 'Code', description: '隔离执行 Python', icon: Braces },
  message: { type: 'message', label: 'Message', description: '输出消息', icon: Send },
  memory_read: { type: 'memory_read', label: 'Memory Read', description: '读取长期记忆', icon: BrainCircuit },
  memory_write: { type: 'memory_write', label: 'Memory Write', description: '写入长期记忆', icon: Save },
  http_tool: { type: 'http_tool', label: 'HTTP Tool', description: '调用 HTTP 工具', icon: Globe2 },
  mcp_tool: { type: 'mcp_tool', label: 'MCP Tool', description: '调用 MCP 工具', icon: PlugZap },
  switch: { type: 'switch', label: 'Switch', description: '按条件选择分支', icon: GitBranch },
  json_output: { type: 'json_output', label: 'JSON Output', description: '校验结构化输出', icon: Braces },
  guardrail: { type: 'guardrail', label: 'Guardrail', description: '检查输出规则', icon: ShieldCheck },
};

export const paletteNodes: NodeType[] = ['begin', 'agent_loop', 'knowledge_retrieval', 'prompt', 'llm', 'switch', 'message', 'json_output', 'guardrail', 'http_tool', 'mcp_tool', 'memory_read', 'memory_write', 'agent_call', 'team_call', 'code_sandbox'];

export const resourceNodeTypes: NodeType[] = ['knowledge_retrieval', 'memory_read', 'memory_write', 'http_tool', 'mcp_tool'];

export function isResourceNode(type: NodeType): boolean {
  return resourceNodeTypes.includes(type);
}

export function defaultConfig(type: NodeType): NodeConfig {
  switch (type) {
    case 'begin': return { input_schema: { query: 'string' } };
    case 'knowledge_retrieval': return { kb_ids: [], query: '{{sys.query}}', top_k: 5, mode: 'hybrid' };
    case 'prompt': return { template: '基于以下输入回答: {{sys.query}}' };
    case 'llm': return { provider_id: 0, model: '', temperature: 0.3, stream: true };
    case 'agent_loop': return { mode: 'react', task_template: '{{sys.query}}', max_iterations: 8, output_mode: 'final_answer' };
    case 'agent_call':
    case 'workflow_call': return { workflow_id: 0, input: { query: '{{sys.query}}' }, max_depth: 2 };
    case 'team_call': return { team_id: 0, input: { query: '{{sys.query}}' }, max_depth: 2 };
    case 'code_sandbox': return { language: 'python', code: 'print("hello")', timeout_ms: 5000, max_output_bytes: 4096, memory_limit_mb: 128 };
    case 'message': return { content: '{{llm.text}}', with_citation: false };
    case 'memory_read': return { memory_types: ['profile', 'summary'], limit: 5 };
    case 'memory_write': return { memory_type: 'summary', content: '{{message.content}}', importance: 0.5 };
    case 'http_tool': return { tool_id: 0, input: {} };
    case 'mcp_tool': return { server_id: 0, tool_name: '', input: {} };
    case 'switch': return { conditions: [{ expr: '{{sys.query}} != ""', target: '' }, { expr: 'default', target: '' }] };
    case 'json_output': return { value: '{{llm.text}}', schema: { type: 'object' } };
    case 'guardrail': return { source: '{{llm.text}}', max_length: 4000, banned_terms: [] };
  }
}

export function summary(config: NodeConfig): string[] {
  return Object.entries(config).filter(([k, v]) => !k.startsWith('_') && v !== '' && v !== undefined && !(Array.isArray(v) && v.length === 0)).slice(0, 3).map(([k]) => k);
}
