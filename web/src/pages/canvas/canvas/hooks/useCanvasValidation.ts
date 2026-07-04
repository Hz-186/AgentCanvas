import type { Edge } from '@xyflow/react';
import { isAgentNodeType, isStaticAgentCallNodeType } from '../../config';
import type { CanvasNode } from '../../types';

export function validateLocal(nodes: CanvasNode[], edges: Edge[]): string {
  const beginCount = nodes.filter((node) => node.data.nodeType === 'begin').length;
  if (beginCount !== 1) return '画布必须且只能包含一个 Begin 节点';
  const ids = new Set(nodes.map((node) => node.id));
  const adjacency = new Map<string, string[]>();
  const indegree = new Map<string, number>();
  for (const node of nodes) {
    adjacency.set(node.id, []);
    indegree.set(node.id, 0);
  }
  for (const edge of edges) {
    if (!ids.has(edge.source) || !ids.has(edge.target) || edge.source === edge.target) return '连线包含无效节点';
    adjacency.get(edge.source)?.push(edge.target);
    indegree.set(edge.target, (indegree.get(edge.target) ?? 0) + 1);
  }
  for (const node of nodes) {
    const nexts = adjacency.get(node.id) ?? [];
    if (node.data.nodeType !== 'switch' && nexts.length > 1) return `${node.data.label} 不能连接多个后续节点，只有 Switch 支持多分支`;
    if (node.data.nodeType === 'switch') {
      const conditions = (node.data.config as Record<string, unknown>).conditions;
      if (Array.isArray(conditions)) {
        for (const condition of conditions) {
          const target = String((condition as Record<string, unknown>).target ?? '').trim();
          if (!target || !nexts.includes(target)) return `Switch 分支目标 ${target || '-'} 必须是它的出边节点`;
        }
      }
    }
  }
  const queue = [...Array.from(indegree.entries()).filter(([, degree]) => degree === 0).map(([id]) => id)];
  let visitedCount = 0;
  const indegreeCopy = new Map(indegree);
  while (queue.length > 0) {
    const id = queue.shift()!;
    visitedCount += 1;
    for (const next of adjacency.get(id) ?? []) {
      indegreeCopy.set(next, (indegreeCopy.get(next) ?? 0) - 1);
      if ((indegreeCopy.get(next) ?? 0) === 0) queue.push(next);
    }
  }
  if (visitedCount !== nodes.length) return '画布存在循环连线，请把循环逻辑放进 Agent Loop 节点内部';
  const begin = nodes.find((node) => node.data.nodeType === 'begin');
  const reachable = new Set<string>();
  const reachQueue = begin ? [begin.id] : [];
  while (reachQueue.length > 0) {
    const id = reachQueue.shift()!;
    if (reachable.has(id)) continue;
    reachable.add(id);
    reachQueue.push(...(adjacency.get(id) ?? []));
  }
  if (reachable.size !== nodes.length) {
    const missing = nodes.find((node) => !reachable.has(node.id));
    return `${missing?.data.label ?? '存在节点'} 没有从 Begin 连通`;
  }
  for (const node of nodes) {
    const config = node.data.config as Record<string, unknown>;
    if (node.data.nodeType === 'knowledge_retrieval' && (!Array.isArray(config.kb_ids) || config.kb_ids.length === 0)) return 'Retrieval 节点需要选择知识库';
    if (node.data.nodeType === 'prompt' && !String(config.template ?? '').trim()) return 'Prompt 节点需要模板';
    if (node.data.nodeType === 'llm' && Number(config.provider_id ?? 0) <= 0) return 'LLM 节点需要选择 Provider';
    if (isAgentNodeType(node.data.nodeType) && !String(config.task_template ?? '').trim()) return 'Agent 节点需要任务模板';
    if (isStaticAgentCallNodeType(node.data.nodeType) && Number(config.workflow_id ?? 0) <= 0) return 'Agent Call 节点需要选择 Agent';
    if (node.data.nodeType === 'team_call' && Number(config.team_id ?? 0) <= 0) return 'Team Call 节点需要选择 Team';
    if (node.data.nodeType === 'code_sandbox' && !String(config.code ?? '').trim()) return 'Code Sandbox 节点需要代码';
    if (node.data.nodeType === 'message' && !String(config.content ?? '').trim()) return 'Message 节点需要内容';
    if (node.data.nodeType === 'memory_write' && (!String(config.memory_type ?? '').trim() || !String(config.content ?? '').trim())) return 'Memory Write 节点需要类型和内容';
    if (node.data.nodeType === 'http_tool' && Number(config.tool_id ?? 0) <= 0) return 'HTTP Tool 节点需要选择 Tool';
    if (node.data.nodeType === 'switch' && !Array.isArray(config.conditions)) return 'Switch 节点需要 conditions';
    if (node.data.nodeType === 'json_output' && !String(config.value ?? '').trim()) return 'JSON Output 节点需要 value';
    if (node.data.nodeType === 'guardrail' && !String(config.source ?? '').trim()) return 'Guardrail 节点需要 source';
  }
  return '';
}
