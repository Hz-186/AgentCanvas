import { StatusBadge } from '../../../components/ui';
import type { ToolInvocation } from '../../../types/api';
import { prettyJson } from '../../../utils/format';

export function ToolInvocationList({ items }: { items: ToolInvocation[] }) {
  return (
    <div className="trace-list">
      {items.map((item) => (
        <article className="trace-item" key={item.id}>
          <div className="trace-item-head">
            <strong>{item.tool_name}</strong>
            <StatusBadge tone={item.status === 'succeeded' ? 'good' : item.error_message ? 'bad' : 'info'}>{item.status}</StatusBadge>
          </div>
          <p className="muted">{item.tool_type} · {item.latency_ms}ms · Node {item.node_id}</p>
          {item.input_json ? <pre className="code-box">{prettyJson(item.input_json)}</pre> : null}
          {item.output_json ? <pre className="code-box">{prettyJson(item.output_json)}</pre> : null}
        </article>
      ))}
    </div>
  );
}
