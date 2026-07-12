import type { Node } from '@xyflow/react';
import type { NodeConfig, NodeType } from '../../types/flow';

export type AgentMode = 'react' | 'plan_execute';

export type NodeRunStatus = 'idle' | 'running' | 'waiting_human' | 'succeeded' | 'failed';

export interface AgentUIState {
  x?: number;
  y?: number;
  agent_mode?: AgentMode;
  modules?: Array<{
    id: string;
    kind: string;
    collapsed?: boolean;
    order: number;
    refNodeId?: string;
  }>;
  selectedModuleId?: string;
}

export interface CanvasNodeData extends Record<string, unknown> {
  label: string;
  nodeType: NodeType;
  config: NodeConfig & { _ui?: AgentUIState };
  runStatus?: NodeRunStatus;
}

export type CanvasNode = Node<CanvasNodeData>;
