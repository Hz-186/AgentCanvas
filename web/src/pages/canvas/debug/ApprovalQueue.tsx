import { Button, EmptyState, StatusBadge } from '../../../components/ui';
import type { ApprovalRequest } from '../../../types/api';

export function ApprovalQueue({ items, onDecide }: { items: ApprovalRequest[]; onDecide: (item: ApprovalRequest, approve: boolean) => void }) {
  if (items.length === 0) return <EmptyState title="暂无待审批请求" description="高风险工具触发审批后会出现在这里。" />;
  return (
    <div className="trace-list">
      {items.map((item) => (
        <article className="trace-item" key={item.id}>
          <div className="trace-item-head">
            <strong>{item.tool_name}</strong>
            <StatusBadge tone={item.risk_level === 'high' ? 'warn' : 'neutral'}>{item.risk_level}</StatusBadge>
          </div>
          <p>{item.reason}</p>
          <p className="muted">Run #{item.run_id} · Node {item.node_id || '-'}</p>
          <div className="toolbar-actions">
            <Button onClick={() => onDecide(item, true)}>批准并恢复</Button>
            <Button onClick={() => onDecide(item, false)}>拒绝并恢复</Button>
          </div>
        </article>
      ))}
    </div>
  );
}
