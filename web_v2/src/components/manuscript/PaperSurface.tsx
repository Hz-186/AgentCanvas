import type { CSSProperties, HTMLAttributes, ReactNode } from 'react';
import { cn } from '@/lib/cn';

export type PaperVariant = 'folio' | 'note' | 'ribbon' | 'ledger' | 'index';
export type PaperHardware = 'paperclip' | 'pin' | 'waxSeal' | 'stitch' | 'none';

export function PaperSurface({ children, className, variant = 'folio', hardware = 'none', rotation = 0, ...props }: HTMLAttributes<HTMLDivElement> & { children: ReactNode; variant?: PaperVariant; hardware?: PaperHardware; rotation?: number }) {
  return <div {...props} className={cn('paper-shell', `paper-${variant}`, className)} style={{ ...props.style, '--paper-rotation': `${rotation}deg` } as CSSProperties}>
    {hardware === 'paperclip' ? <span className="paperclip" aria-hidden="true" /> : null}
    {hardware === 'pin' ? <span className="paper-pin" aria-hidden="true" /> : null}
    {hardware === 'waxSeal' ? <span className="paper-wax" aria-hidden="true" /> : null}
    {hardware === 'stitch' ? <span className="paper-stitch" aria-hidden="true" /> : null}
    <div className="paper-surface">{children}</div>
  </div>;
}

