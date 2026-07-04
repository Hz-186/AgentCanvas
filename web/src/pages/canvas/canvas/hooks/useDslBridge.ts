import type { Edge } from '@xyflow/react';
import type { DSLEdge, DSLNode, FlowDSL } from '../../../../types/flow';
import { defaultConfig, nodeMeta } from '../../config';
import type { AgentUIState, CanvasNode, CanvasNodeData } from '../../types';

export function normalizeDSL(raw: unknown): FlowDSL | null {
  if (!raw) return null;
  const value = typeof raw === 'string' ? JSON.parse(raw) : raw;
  if (!value || typeof value !== 'object') return null;
  const maybe = value as Partial<FlowDSL>;
  if (!Array.isArray(maybe.nodes) || !Array.isArray(maybe.edges)) return null;
  return maybe as FlowDSL;
}

function stableRuntimeValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableRuntimeValue);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, child]) => [key, stableRuntimeValue(child)]),
  );
}

function runtimeConfig(config: unknown): unknown {
  if (!config || typeof config !== 'object' || Array.isArray(config)) return stableRuntimeValue(config ?? {});
  const rest = { ...(config as Record<string, unknown>) };
  delete rest._ui;
  return stableRuntimeValue(rest);
}

export function runtimeDSLKey(raw: unknown): string {
  const parsed = normalizeDSL(raw);
  if (!parsed) return '';
  return JSON.stringify({
    schema_version: parsed.schema_version,
    flow_id: parsed.flow_id,
    nodes: [...parsed.nodes]
      .map((node) => ({ ...node, config: runtimeConfig(node.config) }))
      .sort((left, right) => `${left.id}:${left.type}:${left.name}`.localeCompare(`${right.id}:${right.type}:${right.name}`)),
    edges: [...parsed.edges].sort((left, right) => `${left.from}:${left.to}`.localeCompare(`${right.from}:${right.to}`)),
  });
}

export function defaultNodes(): CanvasNode[] {
  return [
    {
      id: 'begin',
      type: 'agentNode',
      position: { x: 120, y: 170 },
      data: { label: 'Begin', nodeType: 'begin', config: defaultConfig('begin') },
    },
  ];
}

export function fromDSL(dsl: FlowDSL): { nodes: CanvasNode[]; edges: Edge[] } {
  const nodes: CanvasNode[] = dsl.nodes.map((node, index) => {
    const config = (node.config ?? {}) as CanvasNodeData['config'];
    const ui = config._ui as AgentUIState | undefined;
    const pos = typeof ui?.x === 'number' && typeof ui?.y === 'number' ? { x: ui.x, y: ui.y } : { x: 120 + index * 240, y: 170 };
    const isLegacyAgent = node.type === 'agent_loop';
    return {
      id: node.id,
      type: 'agentNode',
      position: pos,
      data: {
        label: isLegacyAgent ? 'Agent' : node.name || nodeMeta[node.type]?.label || node.type,
        nodeType: node.type,
        config,
      },
    };
  });
  return {
    nodes: nodes.length > 0 ? nodes : defaultNodes(),
    edges: dsl.edges.map((edge, index) => ({ id: `edge-${edge.from}-${edge.to}-${index}`, source: edge.from, target: edge.to })),
  };
}

export function toDSL(workflowId: number, nodes: CanvasNode[], edges: Edge[]): FlowDSL {
  return {
    schema_version: 'v1',
    flow_id: `workflow-${workflowId}`,
    nodes: nodes.map<DSLNode>((node) => ({
      id: node.id,
      type: node.data.nodeType,
      name: node.data.label,
      config: {
        ...node.data.config,
        _ui: {
          ...((node.data.config._ui ?? {}) as AgentUIState),
          x: Math.round(node.position.x),
          y: Math.round(node.position.y),
        },
      },
    })),
    edges: edges.map<DSLEdge>((edge) => ({ from: edge.source, to: edge.target })),
  };
}
