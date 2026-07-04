import { Handle, Position, type NodeProps } from '@xyflow/react';
import { Loader2 } from 'lucide-react';
import type { ReactNode } from 'react';
import type { CanvasNode } from '../../types';

export function NodeWrapper({
  selected,
  data,
  children,
  className = '',
  toolsHandle = false,
  subAgentHandle = false,
}: Pick<NodeProps<CanvasNode>, 'selected' | 'data'> & {
  children: ReactNode;
  className?: string;
  toolsHandle?: boolean;
  subAgentHandle?: boolean;
}) {
  const status = data.runStatus ?? 'idle';
  return (
    <div className={`workflow-node node-wrapper ${selected ? 'selected' : ''} node-status-${status} ${className}`}>
      <Handle type="target" position={Position.Left} />
      {status === 'running' ? <Loader2 className="node-running-icon" size={14} /> : null}
      {children}
      <Handle type="source" position={Position.Right} id="default" />
      {toolsHandle ? <Handle className="node-bottom-handle node-tools-handle" type="source" position={Position.Bottom} id="tools" /> : null}
      {subAgentHandle ? <Handle className="node-bottom-handle node-sub-agent-handle" type="source" position={Position.Bottom} id="sub-agents" /> : null}
    </div>
  );
}
