import { Button, StatusBadge } from '../../../components/ui';
import type { RunStep } from '../../../types/api';
import { prettyJson } from '../../../utils/format';

const groupLabels: Record<string, string> = {
  plan: 'Plan',
  plan_revision: 'Plan Revision',
  tool_call: 'Tool Call',
  tool_result: 'Tool Result',
  reflection_recall: 'Reflection Recall',
  reflection: 'Reflection',
  final: 'Final',
  approval_required: 'Approval',
};

function stepGroup(step: RunStep) {
  const type = step.step_type || step.role || 'final';
  if (type.includes('plan_revision')) return 'plan_revision';
  if (type.includes('reflection_recall')) return 'reflection_recall';
  if (type.includes('reflection') || type.includes('reflect')) return 'reflection';
  if (type.includes('plan')) return 'plan';
  if (type.includes('tool_call')) return 'tool_call';
  if (type.includes('tool_result')) return 'tool_result';
  if (type.includes('approval')) return 'approval_required';
  return 'final';
}

function toneFor(group: string) {
  if (group === 'reflection' || group === 'reflection_recall' || group === 'approval_required') return 'warn' as const;
  if (group === 'tool_call' || group === 'tool_result') return 'info' as const;
  if (group === 'final') return 'good' as const;
  return 'neutral' as const;
}

export function reflectionIds(value: unknown): number[] {
  let parsed = value;
  if (typeof value === 'string') {
    try {
      parsed = JSON.parse(value) as unknown;
    } catch {
      return [];
    }
  }
  if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
    const record = parsed as Record<string, unknown>;
    parsed = record.reflection_ids ?? record.ids ?? [];
  }
  return Array.isArray(parsed)
    ? parsed.map(Number).filter((id) => Number.isFinite(id) && id > 0)
    : [];
}

interface AgentTraceTimelineProps {
  steps: RunStep[];
  onReflectionFeedback?: (reflectionId: number, verdict: 'helpful' | 'harmful') => void;
  reflectionFeedback?: Record<number, 'helpful' | 'harmful'>;
}

export function AgentTraceTimeline({ steps, onReflectionFeedback, reflectionFeedback = {} }: AgentTraceTimelineProps) {
  const groups = steps.reduce<Record<string, RunStep[]>>((acc, step) => {
    const group = stepGroup(step);
    acc[group] = [...(acc[group] ?? []), step];
    return acc;
  }, {});
  return (
    <div className="agent-trace-timeline">
      {Object.entries(groups).map(([group, items]) => (
        <section className="trace-group" key={group}>
          <div className="trace-group-head">
            <strong>{groupLabels[group] ?? group}</strong>
            <StatusBadge tone={toneFor(group)}>{items.length}</StatusBadge>
          </div>
          <div className="trace-list">
            {items.map((step) => {
              const recalledReflectionIds = group === 'reflection_recall' ? reflectionIds(step.output_json) : [];
              return <article className="trace-item" key={step.id}>
                <div className="trace-item-head">
                  <strong>#{step.step_index} {step.tool_name || step.role || step.step_type}</strong>
                  <StatusBadge tone={step.error_message ? 'bad' : step.compressed ? 'warn' : toneFor(group)}>{step.compressed ? 'compressed' : step.step_type}</StatusBadge>
                </div>
                {step.content ? <p>{step.content}</p> : null}
                {step.arguments_json ? <pre className="code-box">{prettyJson(step.arguments_json)}</pre> : null}
                {step.output_json ? <pre className="code-box">{prettyJson(step.output_json)}</pre> : null}
                {recalledReflectionIds.length > 0 ? (
                  <div className="stack">
                    {recalledReflectionIds.map((reflectionId) => (
                      <div className="toolbar-actions" key={reflectionId}>
                        <StatusBadge tone="info">Reflection #{reflectionId}</StatusBadge>
                        <Button disabled={!onReflectionFeedback || reflectionFeedback[reflectionId] === 'helpful'} onClick={() => onReflectionFeedback?.(reflectionId, 'helpful')}>
                          Helpful
                        </Button>
                        <Button disabled={!onReflectionFeedback || reflectionFeedback[reflectionId] === 'harmful'} onClick={() => onReflectionFeedback?.(reflectionId, 'harmful')}>
                          Harmful
                        </Button>
                      </div>
                    ))}
                  </div>
                ) : null}
                {step.error_message ? <p className="error-text">{step.error_message}</p> : null}
              </article>
            })}
          </div>
        </section>
      ))}
    </div>
  );
}
