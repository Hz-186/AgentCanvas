import { useEffect, useState } from 'react';
import { Button, EmptyState, StatusBadge } from './ui';
import type { ApprovalOption, ApprovalRequest, UserInputQuestion } from '../types/api';

function approvalOptions(item: ApprovalRequest): ApprovalOption[] {
  if (Array.isArray(item.options)) return item.options.filter((option) => option && typeof option.id === 'string' && typeof option.label === 'string');
  const raw = item.request_json as { options?: ApprovalOption[] } | string | undefined;
  if (typeof raw === 'string') {
    try { return approvalOptions({ ...item, request_json: JSON.parse(raw) }); } catch { return []; }
  }
  return Array.isArray(raw?.options) ? raw.options.filter((option) => option && typeof option.id === 'string' && typeof option.label === 'string') : [];
}

export function ApprovalQueue({ items, onDecide }: { items: ApprovalRequest[]; onDecide: (item: ApprovalRequest, approve: boolean, optionID?: string, answers?: Record<string, string>) => void }) {
  const [decidingID, setDecidingID] = useState<number | null>(null);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  useEffect(() => {
    if (decidingID !== null && !items.some((item) => item.id === decidingID)) setDecidingID(null);
  }, [decidingID, items]);
  if (items.length === 0) return <EmptyState title="暂无待审批请求" description="高风险工具触发审批后会出现在这里。" />;
  const decide = (item: ApprovalRequest, approved: boolean, optionID?: string) => {
    if (decidingID !== null) return;
	if (approved && item.questions?.some((question) => !answers[question.id])) return;
    setDecidingID(item.id);
    onDecide(item, approved, optionID, answers);
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
          <p className="muted">Run #{item.run_id}</p>
          {item.questions?.map((question: UserInputQuestion) => <div className="stack" key={question.id}><label>{question.question}<select value={answers[question.id] ?? ''} onChange={(event) => setAnswers((current) => ({ ...current, [question.id]: event.target.value }))}><option value="">请选择</option>{question.options.map((option) => <option key={option.label} value={option.label}>{option.label}</option>)}{question.is_other ? <option value="__other__">其他</option> : null}</select></label>{question.is_other && answers[question.id] === '__other__' ? <input aria-label={`${question.header}自定义答案`} placeholder="请输入自定义答案" onChange={(event) => setAnswers((current) => ({ ...current, [question.id]: event.target.value }))} /> : null}</div>)}
          {approvalOptions(item).length > 0 ? <div className="trace-list compact">{approvalOptions(item).map((option) => <div className="trace-item" key={option.id}><strong>{option.label}</strong>{option.description ? <p className="muted">{option.description}</p> : null}<Button disabled={decidingID !== null || Boolean(item.questions?.some((question) => !answers[question.id]))} onClick={() => decide(item, true, option.id)}>选择并恢复</Button></div>)}</div> : <div className="toolbar-actions"><Button disabled={decidingID !== null || Boolean(item.questions?.some((question) => !answers[question.id]))} onClick={() => decide(item, true)}>批准并恢复</Button><Button disabled={decidingID !== null} onClick={() => decide(item, false)}>拒绝并恢复</Button></div>}
        </article>
      ))}
    </div>
  );
}
