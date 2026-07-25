import { useEffect, useState } from 'react';
import { Button, EmptyState, StatusBadge } from '../../../components/ui';
import type { ApprovalOption, ApprovalRequest } from '../../../types/api';

function approvalOptions(item: ApprovalRequest): ApprovalOption[] {
  if (Array.isArray(item.options)) return item.options.filter((option) => option && typeof option.id === 'string' && typeof option.label === 'string');
  const raw = item.request_json as { options?: ApprovalOption[] } | string | undefined;
  if (typeof raw === 'string') {
    try { return approvalOptions({ ...item, request_json: JSON.parse(raw) }); } catch { return []; }
  }
  return Array.isArray(raw?.options) ? raw.options.filter((option) => option && typeof option.id === 'string' && typeof option.label === 'string') : [];
}

export function ApprovalQueue({ items, onDecide }: { items: ApprovalRequest[]; onDecide: (item: ApprovalRequest, approve: boolean, optionID?: string) => void }) {
  const [decidingID, setDecidingID] = useState<number | null>(null);
  useEffect(() => {
    if (decidingID !== null && !items.some((item) => item.id === decidingID)) setDecidingID(null);
  }, [decidingID, items]);
  if (items.length === 0) return <EmptyState title="暂无待审批请求" description="高风险工具触发审批后会出现在这里。" />;
  const decide = (item: ApprovalRequest, approved: boolean, optionID?: string) => {
    if (decidingID !== null) return;
    setDecidingID(item.id);
    onDecide(item, approved, optionID);
  };
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
          {approvalOptions(item).length > 0 ? <div className="trace-list compact">{approvalOptions(item).map((option) => <div className="trace-item" key={option.id}><strong>{option.label}</strong>{option.description ? <p className="muted">{option.description}</p> : null}<Button disabled={decidingID !== null} onClick={() => decide(item, true, option.id)}>选择并恢复</Button></div>)}</div> : <div className="toolbar-actions"><Button disabled={decidingID !== null} onClick={() => decide(item, true)}>批准并恢复</Button><Button disabled={decidingID !== null} onClick={() => decide(item, false)}>拒绝并恢复</Button></div>}
        </article>
      ))}
    </div>
  );
}
