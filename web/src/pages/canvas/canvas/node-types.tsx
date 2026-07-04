import type { NodeTypes } from '@xyflow/react';
import type { ComponentProps } from 'react';
import type { CanvasNode } from '../types';
import { AgentLoopNode } from './nodes/AgentLoopNode';
import { GenericNode } from './nodes/GenericNode';

function CanvasNodeRenderer(props: ComponentProps<typeof GenericNode>) {
  if ((props.data as CanvasNode['data']).nodeType === 'agent_loop') return <AgentLoopNode {...props} />;
  return <GenericNode {...props} />;
}

export const nodeTypes: NodeTypes = { agentNode: CanvasNodeRenderer };
