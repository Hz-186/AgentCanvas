import { useEffect, useRef, type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react';

export function EditorialHeader({
  word,
  script,
  kicker,
  description,
  action,
  compact = false,
}: {
  word: string;
  script: string;
  kicker: string;
  description: string;
  action?: ReactNode;
  compact?: boolean;
}) {
  return (
    <header className={`editorial-head ${compact ? 'editorial-head-compact' : ''}`}>
      <div className="editorial-copy">
        <p className="editorial-kicker">{kicker}</p>
        <h1 className="editorial-title">
          <span>{word}</span>
          <em>{script}</em>
        </h1>
        <p className="editorial-description">{description}</p>
      </div>
      {action ? <div className="editorial-action">{action}</div> : null}
    </header>
  );
}

export function AmbientLiquidField() {
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const node = ref.current;
    if (!node || !window.matchMedia('(pointer: fine)').matches) return undefined;
    let frame = 0;
    const onMove = (event: PointerEvent) => {
      if (frame) return;
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        const rect = node.getBoundingClientRect();
        node.style.setProperty('--ambient-x', `${event.clientX - rect.left}px`);
        node.style.setProperty('--ambient-y', `${event.clientY - rect.top}px`);
      });
    };
    window.addEventListener('pointermove', onMove, { passive: true });
    return () => {
      window.removeEventListener('pointermove', onMove);
      if (frame) window.cancelAnimationFrame(frame);
    };
  }, []);

  return <div ref={ref} className="ambient-liquid-field" aria-hidden="true"><i /><i /><i /></div>;
}

type ResizeSide = 'left' | 'right';

export function ResizableRail({
  containerRef,
  variable,
  storageKey,
  side,
  min,
  max,
  collapsed,
  defaultWidth,
  onCommit,
  label,
}: {
  containerRef: React.RefObject<HTMLElement | null>;
  variable: `--${string}`;
  storageKey: string;
  side: ResizeSide;
  min: number;
  max: number;
  collapsed: number;
  defaultWidth: number;
  onCommit?: (width: number) => void;
  label: string;
}) {
  const lastExpanded = useRef(defaultWidth);

  const start = (event: ReactPointerEvent<HTMLButtonElement>) => {
    event.preventDefault();
    const container = containerRef.current;
    if (!container) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    const startX = event.clientX;
    const initial = Number.parseFloat(getComputedStyle(container).getPropertyValue(variable)) || defaultWidth;
    let width = initial;
    let frame = 0;
    let clientX = startX;
    container.classList.add('is-resizing-pane');

    const apply = () => {
      frame = 0;
      const raw = side === 'left' ? initial + (clientX - startX) : initial - (clientX - startX);
      width = raw <= collapsed ? 0 : Math.min(max, Math.max(min, raw));
      if (width > 0) lastExpanded.current = width;
      container.style.setProperty(variable, `${width}px`);
    };
    const move = (moveEvent: PointerEvent) => {
      clientX = moveEvent.clientX;
      if (!frame) frame = window.requestAnimationFrame(apply);
    };
    const finish = () => {
      if (frame) {
        window.cancelAnimationFrame(frame);
        apply();
      }
      container.classList.remove('is-resizing-pane');
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', finish);
      localStorage.setItem(storageKey, String(width));
      if (width > 0) localStorage.setItem(`${storageKey}-expanded`, String(width));
      onCommit?.(width);
    };
    window.addEventListener('pointermove', move, { passive: true });
    window.addEventListener('pointerup', finish, { once: true });
  };

  const reopen = () => {
    const container = containerRef.current;
    if (!container) return;
    const current = Number.parseFloat(getComputedStyle(container).getPropertyValue(variable)) || 0;
    if (current > 0) return;
    const stored = Number(localStorage.getItem(`${storageKey}-expanded`));
    const width = Number.isFinite(stored) && stored >= min ? Math.min(max, stored) : lastExpanded.current;
    container.style.setProperty(variable, `${width}px`);
    localStorage.setItem(storageKey, String(width));
    onCommit?.(width);
  };

  return (
    <button
      type="button"
      className="resize-rail"
      aria-label={label}
      title={label}
      onDoubleClick={reopen}
      onPointerDown={start}
    ><span /></button>
  );
}

export function storedWidth(key: string, fallback: number) {
  const value = Number(localStorage.getItem(key));
  return Number.isFinite(value) ? value : fallback;
}

export function paneStyle(values: Record<`--${string}`, string>): CSSProperties {
  return values as CSSProperties;
}
