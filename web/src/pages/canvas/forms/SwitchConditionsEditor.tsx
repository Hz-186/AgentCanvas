import { Plus, Trash2 } from 'lucide-react';
import { Button, IconButton, Select, TextInput } from '../../../components/ui';
import type { CanvasNode } from '../types';

type Condition = { expr: string; target: string };

export function SwitchConditionsEditor({
  node,
  nodes,
  onChange,
}: {
  node: CanvasNode;
  nodes: CanvasNode[];
  onChange: (conditions: Condition[]) => void;
}) {
  const config = node.data.config as Record<string, unknown>;
  const conditions = Array.isArray(config.conditions) ? config.conditions as Condition[] : [];
  const targets = nodes.filter((item) => item.id !== node.id);
  const update = (index: number, patch: Partial<Condition>) => onChange(conditions.map((condition, current) => current === index ? { ...condition, ...patch } : condition));

  return (
    <div className="condition-editor">
      {conditions.map((condition, index) => (
        <div className="condition-row" key={`${condition.expr}-${index}`}>
          <span className="condition-index">{String(index + 1).padStart(2, '0')}</span>
          <TextInput value={condition.expr} onChange={(event) => update(index, { expr: event.target.value })} placeholder={'{{sys.query}} != "" 或 default'} />
          <Select value={condition.target} onChange={(event) => update(index, { target: event.target.value })}>
            <option value="">选择目标节点</option>
            {targets.map((target) => <option key={target.id} value={target.id}>{target.data.label} · {target.id}</option>)}
          </Select>
          <IconButton label={`删除条件 ${index + 1}`} onClick={() => onChange(conditions.filter((_, current) => current !== index))}>
            <Trash2 size={15} />
          </IconButton>
        </div>
      ))}
      <Button type="button" onClick={() => onChange([...conditions, { expr: conditions.length ? 'default' : '{{sys.query}} != ""', target: targets[0]?.id ?? '' }])}>
        <Plus size={15} />
        添加条件
      </Button>
    </div>
  );
}
