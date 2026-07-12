import type { Node, Edge } from '@xyflow/react';
import type { NodeType, NodeConfig } from '@/types/flow';

export interface CanvasNodeData extends Record<string, unknown> {
  nodeType: NodeType;
  label: string;
  description: string;
  config: NodeConfig & { _ui?: { x: number; y: number } };
  runtimeStatus?: 'idle' | 'running' | 'success' | 'failed';
}

export type CanvasNode = Node<CanvasNodeData, 'agentNode'>;
export type CanvasEdge = Edge;
