import { Handle, Position, type NodeProps } from '@xyflow/react';
import { isResourceNode, nodeMeta, summary } from '../config';
import type { CanvasNode } from '../types';

const accentByType: Record<string, string> = {
  begin: 'var(--node-color-begin)', agent_loop: 'var(--node-color-begin)', llm: 'var(--node-color-llm)', prompt: 'var(--node-color-llm)',
  http_tool: 'var(--node-color-tool)', mcp_tool: 'var(--node-color-tool)', switch: 'var(--node-color-switch)',
  memory_read: 'var(--node-color-memory)', memory_write: 'var(--node-color-memory)', knowledge_retrieval: 'var(--node-color-memory)',
  code_sandbox: 'var(--node-color-code)', guardrail: 'var(--node-color-guard)',
};

export function CanvasNodeView({ data, selected }: NodeProps<CanvasNode>) {
  const meta = nodeMeta[data.nodeType];
  const Icon = meta.icon;
  const terminal = data.nodeType === 'message' || data.nodeType === 'json_output';
  const resource = isResourceNode(data.nodeType);
  const conditions = data.nodeType === 'switch' && Array.isArray((data.config as Record<string, unknown>).conditions)
    ? (data.config as Record<string, unknown>).conditions as Array<{ expr?: string; target?: string }>
    : [];
  return (
    <div className={`flow-node node-${data.nodeType}${selected ? ' selected' : ''}`} style={{ '--node-accent': accentByType[data.nodeType] ?? 'var(--accent)' } as React.CSSProperties}>
      <span className="type-bar" />
      {data.nodeType !== 'begin' ? <Handle id={resource ? 'dependency' : undefined} type="target" position={resource ? Position.Top : Position.Left} /> : null}
      {!terminal && data.nodeType !== 'switch' ? <Handle type="source" position={Position.Right} /> : null}
      {data.nodeType === 'agent_loop' ? <Handle id="dependency" type="source" position={Position.Bottom} className="dependency-handle" /> : null}
      {data.nodeType === 'switch' ? conditions.map((condition, index) => <Handle key={`${condition.expr}-${index}`} id={`branch-${index}`} type="source" position={Position.Bottom} style={{ left: `${((index + 1) / (conditions.length + 1)) * 100}%` }} />) : null}
      <div className="flow-node-head">
        <div className="flow-node-icon"><Icon size={18} /></div>
        <div className="flow-node-title"><strong>{data.label}</strong><span>{data.description}</span></div>
        <span className={`node-status-dot ${data.runtimeStatus ?? 'idle'}`} aria-label={data.runtimeStatus ? `运行状态：${data.runtimeStatus}` : undefined} />
        <span className="node-folio">{data.nodeType.slice(0, 3).toUpperCase()}</span>
      </div>
      <div className="flow-node-foot">
        {summary(data.config).map((item) => <span className="node-chip" key={item}>{item}</span>)}
        {data.nodeType === 'switch' ? <span className="node-chip">{conditions.length} branches</span> : null}
      </div>
    </div>
  );
}

export const nodeTypes = { agentNode: CanvasNodeView };
