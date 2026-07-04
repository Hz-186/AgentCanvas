import type { ReactNode } from 'react';
import { Panel } from '../../../components/ui';
import { nodeMeta } from '../config';
import type { CanvasNode } from '../types';
import { TitleInput } from './TitleInput';

export function FormSheet({
  selected,
  title,
  onTitleChange,
  children,
  agent = false,
}: {
  selected: CanvasNode;
  title: string;
  onTitleChange: (value: string) => void;
  children: ReactNode;
  agent?: boolean;
}) {
  const meta = nodeMeta[selected.data.nodeType];
  const Icon = meta.icon;
  return (
    <Panel title={title} eyebrow={selected.id} className={`form-sheet ${agent ? 'agent-config-panel' : ''}`}>
      <div className="form-sheet-headline">
        <span className="node-icon"><Icon size={16} /></span>
        <div className="min-w-0">
          <strong>{meta.label}</strong>
          <p className="muted">{meta.description}</p>
        </div>
      </div>
      <TitleInput value={title} onChange={onTitleChange} />
      {children}
    </Panel>
  );
}
