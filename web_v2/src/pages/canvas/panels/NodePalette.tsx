import type { NodeType } from '@/types/flow';
import { nodeMeta, paletteNodes } from '../config';

export function NodePalette({ onAdd, onCollapse }: { onAdd: (type: NodeType) => void; onCollapse?: () => void }) {
  return (
    <aside className="canvas-panel canvas-palette deckle-paper">
      <div className="panel-header"><div><span className="folio-index">Index · I</span><h3>Node Folios</h3></div><div className="panel-actions"><span className="badge">{paletteNodes.length}</span>{onCollapse ? <button className="ink-collapse" onClick={onCollapse} aria-label="折叠节点库">‹</button> : null}</div></div>
      <div className="panel-body scroll-surface">
        <div className="palette-list">
          {paletteNodes.map((type) => {
            const meta = nodeMeta[type];
            const Icon = meta.icon;
            return <button className="palette-item" key={type} onClick={() => onAdd(type)}><span className="palette-icon"><Icon size={17} /></span><span><strong>{meta.label}</strong><span>{meta.description}</span></span></button>;
          })}
        </div>
      </div>
    </aside>
  );
}
