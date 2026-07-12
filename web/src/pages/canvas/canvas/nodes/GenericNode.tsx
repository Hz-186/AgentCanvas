import { Handle, Position, type NodeProps } from '@xyflow/react';
import { nodeMeta, nodeSummaryItems } from '../../config';
import type { CanvasNode } from '../../types';
import { NodeWrapper } from './NodeWrapper';

function SwitchNode({ data, selected }: NodeProps<CanvasNode>) {
  const meta = nodeMeta[data.nodeType];
  const Icon = meta.icon;
  const config = data.config as Record<string, unknown>;
  const conditions = Array.isArray(config.conditions)
    ? config.conditions.slice(0, 5) as Array<{ expr?: unknown; target?: unknown }>
    : [];

  return (
    <NodeWrapper data={data} selected={selected} className="node-kind-switch switch-track-node">
      <div className="workflow-node-head">
        <div className="node-icon"><Icon size={16} /></div>
        <div className="min-w-0">
          <strong className="truncate">{data.label}</strong>
          <span className="truncate">条件轨道 · {conditions.length} routes</span>
        </div>
      </div>
      <div className="switch-rule-track">
        {conditions.map((condition, index) => (
          <div className="switch-rule" key={`${String(condition.expr)}-${index}`}>
            <span>{String(index + 1).padStart(2, '0')}</span>
            <strong title={String(condition.expr ?? 'default')}>{String(condition.expr ?? 'default')}</strong>
            <small>→ {String(condition.target ?? 'next')}</small>
          </div>
        ))}
      </div>
      <div className="switch-branch-handles" aria-hidden="true">
        {conditions.map((condition, index) => (
          <Handle
            className="switch-branch-handle"
            id={`branch-${index}`}
            key={`${String(condition.target)}-${index}`}
            position={Position.Bottom}
            style={{ left: `${((index + 0.5) / Math.max(conditions.length, 1)) * 100}%` }}
            type="source"
          />
        ))}
      </div>
    </NodeWrapper>
  );
}

export function GenericNode(props: NodeProps<CanvasNode>) {
  const { data, selected } = props;
  if (data.nodeType === 'switch') return <SwitchNode {...props} />;
  const meta = nodeMeta[data.nodeType];
  const Icon = meta.icon;
  const summary = nodeSummaryItems(data).slice(0, 3);
  return (
    <NodeWrapper data={data} selected={selected} className={`node-kind-${data.nodeType}`}>
      <div className="workflow-node-head">
        <div className="node-icon">
          <Icon size={16} />
        </div>
        <div className="min-w-0">
          <strong className="truncate">{data.label}</strong>
          <span className="truncate">{meta.label}</span>
        </div>
      </div>
      <div className="workflow-node-signature" aria-hidden="true">
        <Icon size={15} />
        <span>{meta.description}</span>
      </div>
      <div className="workflow-node-tags">
        {summary.map((item) => <span className="truncate" key={item}>{item}</span>)}
      </div>
    </NodeWrapper>
  );
}
