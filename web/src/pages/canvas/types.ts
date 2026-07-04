import type { Node } from '@xyflow/react';
import type { NodeConfig, NodeType } from '../../types/flow';

export type AgentMode = 'action' | 'react' | 'plan_execute' | 'reasoning_action' | 'reflect' | 'supervisor';

export type NodeRunStatus = 'idle' | 'running' | 'waiting_human' | 'succeeded' | 'failed';

export interface AgentUIState {
  x?: number;
  y?: number;
  agent_mode?: AgentMode;
  reasoning_profile?: 'reasoning_action';
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
