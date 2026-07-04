import { StatusBadge } from '../../../components/ui';
import type { RunStep } from '../../../types/api';
import { prettyJson } from '../../../utils/format';

const groupLabels: Record<string, string> = {
  plan: 'Plan',
  tool_call: 'Tool Call',
  tool_result: 'Tool Result',
  reflection: 'Reflection',
  final: 'Final',
  approval_required: 'Approval',
};

function stepGroup(step: RunStep) {
  const type = step.step_type || step.role || 'final';
  if (type.includes('plan')) return 'plan';
  if (type.includes('tool_call')) return 'tool_call';
  if (type.includes('tool_result')) return 'tool_result';
  if (type.includes('reflection') || type.includes('reflect')) return 'reflection';
  if (type.includes('approval')) return 'approval_required';
  return 'final';
}

function toneFor(group: string) {
  if (group === 'reflection' || group === 'approval_required') return 'warn' as const;
  if (group === 'tool_call' || group === 'tool_result') return 'info' as const;
  if (group === 'final') return 'good' as const;
  return 'neutral' as const;
}

export function AgentTraceTimeline({ steps }: { steps: RunStep[] }) {
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
            {items.map((step) => (
              <article className="trace-item" key={step.id}>
                <div className="trace-item-head">
                  <strong>#{step.step_index} {step.tool_name || step.role || step.step_type}</strong>
                  <StatusBadge tone={step.error_message ? 'bad' : step.compressed ? 'warn' : toneFor(group)}>{step.compressed ? 'compressed' : step.step_type}</StatusBadge>
                </div>
                {step.content ? <p>{step.content}</p> : null}
                {step.arguments_json ? <pre className="code-box">{prettyJson(step.arguments_json)}</pre> : null}
                {step.output_json ? <pre className="code-box">{prettyJson(step.output_json)}</pre> : null}
                {step.error_message ? <p className="error-text">{step.error_message}</p> : null}
              </article>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}
