import { AnimatePresence, motion } from 'framer-motion';
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { cn } from '@/lib/cn';

type ButtonTone = 'primary' | 'secondary' | 'ghost' | 'danger';

export function Button({ className, tone = 'secondary', size, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: ButtonTone; size?: 'small' }) {
  return <button className={cn('button', `button-${tone}`, size === 'small' && 'button-small', className)} {...props} />;
}

export function IconButton(props: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <Button {...props} className={cn('button-icon', props.className)} />;
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="field">
      <span className="field-label">{label}{hint ? <span className="field-hint">{hint}</span> : null}</span>
      {children}
    </label>
  );
}

export function TextInput(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={cn('input', props.className)} />;
}

export function TextArea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={cn('textarea', props.className)} />;
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={cn('select', props.className)} />;
}

export function Badge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'info' }) {
  return <span className={cn('badge', tone !== 'neutral' && `badge-${tone}`)}>{children}</span>;
}

export function Card({ children, className }: { children: ReactNode; className?: string }) {
  return <section className={cn('card glass', className)}>{children}</section>;
}

export function Modal({ open, title, children, onClose }: { open: boolean; title: string; children: ReactNode; onClose: () => void }) {
  return createPortal(
    <AnimatePresence>
      {open ? (
        <motion.div className="modal-backdrop" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
          <motion.section className="modal-panel glass-strong" initial={{ y: 24, scale: .98 }} animate={{ y: 0, scale: 1 }} exit={{ y: 12, scale: .98 }} transition={{ type: 'spring', damping: 28, stiffness: 260 }}>
            <div className="panel-header" style={{ padding: 0, borderBottom: 0, marginBottom: 18 }}>
              <h3>{title}</h3>
              <IconButton aria-label="关闭" onClick={onClose}><X size={18} /></IconButton>
            </div>
            {children}
          </motion.section>
        </motion.div>
      ) : null}
    </AnimatePresence>,
    document.body,
  );
}

export function EmptyState({ title, description }: { title: string; description?: string }) {
  return <div className="empty-state"><strong>{title}</strong>{description ? <span>{description}</span> : null}</div>;
}
