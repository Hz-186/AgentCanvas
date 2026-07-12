import { Handle, Position, type NodeProps } from '@xyflow/react';
import { Loader2 } from 'lucide-react';
import type { ReactNode } from 'react';
import type { CanvasNode } from '../../types';

export function NodeWrapper({
  selected,
  data,
  children,
  className = '',
}: Pick<NodeProps<CanvasNode>, 'selected' | 'data'> & {
  children: ReactNode;
  className?: string;
}) {
  const status = data.runStatus ?? 'idle';
  const isResource = ['knowledge_retrieval', 'memory_read', 'memory_write', 'http_tool', 'mcp_tool'].includes(data.nodeType);
  const targetPosition = isResource ? Position.Top : Position.Left;
  const sourcePosition = data.nodeType === 'begin' ? Position.Bottom : Position.Right;
  return (
    <div className={`workflow-node node-wrapper ${selected ? 'selected' : ''} node-status-${status} ${className}`}>
      {data.nodeType !== 'begin' ? <Handle type="target" position={targetPosition} id={isResource ? 'dependency' : 'default'} /> : null}
      {status === 'running' ? <Loader2 className="node-running-icon" size={14} /> : null}
      {children}
      {data.nodeType !== 'message' && data.nodeType !== 'json_output' ? <Handle type="source" position={sourcePosition} id="default" /> : null}
      {data.nodeType === 'agent_loop' ? <Handle className="dependency-handle" type="source" position={Position.Bottom} id="dependency" /> : null}
    </div>
  );
}
