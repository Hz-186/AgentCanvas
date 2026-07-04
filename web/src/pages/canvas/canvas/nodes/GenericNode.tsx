import type { NodeProps } from '@xyflow/react';
import { nodeMeta, nodeSummaryItems } from '../../config';
import type { CanvasNode } from '../../types';
import { NodeWrapper } from './NodeWrapper';

export function GenericNode({ data, selected }: NodeProps<CanvasNode>) {
  const meta = nodeMeta[data.nodeType];
  const Icon = meta.icon;
  const summary = nodeSummaryItems(data).slice(0, 3);
  return (
    <NodeWrapper data={data} selected={selected}>
      <div className="workflow-node-head">
        <div className="node-icon">
          <Icon size={16} />
        </div>
        <div className="min-w-0">
          <strong className="truncate">{data.label}</strong>
          <span className="truncate">{meta.label}</span>
        </div>
      </div>
      <p className="workflow-node-desc">{meta.description}</p>
      <div className="workflow-node-tags">
        {summary.map((item) => <span className="truncate" key={item}>{item}</span>)}
      </div>
    </NodeWrapper>
  );
}
