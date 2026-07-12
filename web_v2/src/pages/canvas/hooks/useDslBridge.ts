import type { CanvasEdge, CanvasNode } from '../types';
import { defaultConfig, isResourceNode, nodeMeta } from '../config';
import type { FlowDSL, NodeConfig, NodeType } from '@/types/flow';

function isNodeType(value: unknown): value is NodeType {
  return typeof value === 'string' && value in nodeMeta;
}

export function createCanvasNode(type: NodeType, position: { x: number; y: number }, index: number): CanvasNode {
  const meta = nodeMeta[type];
  const config = defaultConfig(type) as NodeConfig & { _ui?: { x: number; y: number } };
  config._ui = position;
  return {
    id: `${type}_${Date.now()}_${index}`,
    type: 'agentNode',
    position,
    data: { nodeType: type, label: meta.label, description: meta.description, config },
  };
}

export function emptyWorkflow(workflowId: number): { nodes: CanvasNode[]; edges: CanvasEdge[] } {
  return {
    nodes: [
      { id: 'begin', type: 'agentNode', position: { x: 80, y: 120 }, data: { nodeType: 'begin', label: 'Begin', description: nodeMeta.begin.description, config: { ...defaultConfig('begin'), _ui: { x: 80, y: 120 } } } },
      { id: 'agent_loop', type: 'agentNode', position: { x: 390, y: 120 }, data: { nodeType: 'agent_loop', label: 'Agent Loop', description: nodeMeta.agent_loop.description, config: { ...defaultConfig('agent_loop'), _ui: { x: 390, y: 120 } } } },
      { id: 'message', type: 'agentNode', position: { x: 700, y: 120 }, data: { nodeType: 'message', label: 'Message', description: nodeMeta.message.description, config: { ...defaultConfig('message'), _ui: { x: 700, y: 120 } } } },
    ],
    edges: [{ id: `begin-agent_loop-${workflowId}`, source: 'begin', target: 'agent_loop' }, { id: `agent_loop-message-${workflowId}`, source: 'agent_loop', target: 'message' }],
  };
}

export function fromDSL(input: unknown, workflowId: number): { nodes: CanvasNode[]; edges: CanvasEdge[] } {
  const dsl = input as Partial<FlowDSL> | null;
  if (!dsl || !Array.isArray(dsl.nodes)) return emptyWorkflow(workflowId);
  const nodes: CanvasNode[] = dsl.nodes.map((node, index) => {
    const type = isNodeType(node.type) ? node.type : 'prompt';
    const config = (node.config ?? {}) as NodeConfig & { _ui?: { x?: number; y?: number } };
    const position = { x: Number(config._ui?.x ?? 80 + index * 280), y: Number(config._ui?.y ?? 120 + (index % 3) * 140) };
    return { id: node.id, type: 'agentNode', position, data: { nodeType: type, label: node.name || nodeMeta[type].label, description: nodeMeta[type].description, config: { ...defaultConfig(type), ...config, _ui: position } } };
  });
  const typeById = new Map(nodes.map((node) => [node.id, node.data.nodeType]));
  const edges = Array.isArray(dsl.edges) ? dsl.edges.map((edge, index) => {
    const sourceType = typeById.get(edge.from);
    const targetType = typeById.get(edge.to);
    const sourceNode = nodes.find((node) => node.id === edge.from);
    const conditions = sourceNode?.data.nodeType === 'switch' && Array.isArray((sourceNode.data.config as Record<string, unknown>).conditions)
      ? (sourceNode.data.config as Record<string, unknown>).conditions as Array<{ target?: string; expr?: string }>
      : [];
    const branchIndex = conditions.findIndex((condition) => condition.target === edge.to);
    return {
      id: `${edge.from}-${edge.to}-${index}`,
      source: edge.from,
      target: edge.to,
      sourceHandle: sourceType === 'switch' && branchIndex >= 0 ? `branch-${branchIndex}` : sourceType === 'agent_loop' && targetType && isResourceNode(targetType) ? 'dependency' : undefined,
      targetHandle: targetType && isResourceNode(targetType) ? 'dependency' : undefined,
      label: sourceType === 'switch' && branchIndex >= 0 ? conditions[branchIndex]?.expr : undefined,
    };
  }) : [];
  return { nodes, edges };
}

export function toDSL(workflowId: number, nodes: CanvasNode[], edges: CanvasEdge[]): FlowDSL {
  return {
    schema_version: 'v1',
    flow_id: `workflow-${workflowId}`,
    nodes: nodes.map((node) => ({ id: node.id, type: node.data.nodeType, name: node.data.label, config: { ...node.data.config, _ui: { x: Math.round(node.position.x), y: Math.round(node.position.y) } } })),
    edges: edges.map((edge) => ({ from: edge.source, to: edge.target })),
  };
}
