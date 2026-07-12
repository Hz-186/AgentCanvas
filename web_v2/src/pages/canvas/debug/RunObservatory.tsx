import { AnimatePresence, motion } from 'framer-motion';
import { BookOpen, Check, CircleStop, Clock3, Search, ShieldAlert, Wrench, X } from 'lucide-react';
import { Button, IconButton } from '@/components/ui';
import { ParchmentBackground, type ManuscriptState } from '@/components/ParchmentBackground';
import type { RuntimeEvent, RunDoneEvent } from '@/types/events';
import { prettyJson } from '@/utils/format';

type ObservatoryEvent = RuntimeEvent | RunDoneEvent | { error: string };

function runtimeType(event: ObservatoryEvent): string {
  if ('error' in event) return 'error';
  if ('type' in event) return event.type;
  return 'workflow_finished';
}

const humanize = (value: string) => value.split('_').join(' ');

export function phaseFor(events: ObservatoryEvent[], running: boolean): ManuscriptState {
  if (events.length === 0) return running ? 'thinking' : 'idle';
  const type = runtimeType(events[events.length - 1] ?? { error: '' });
  if (type.includes('failed') || type === 'error') return 'error';
  if (type.includes('approval')) return 'approval';
  if (type.includes('retrieval')) return 'retrieval';
  if (type.includes('tool')) return 'tool';
  if (type.includes('finished') || (!running && events.length)) return 'complete';
  return running ? 'thinking' : 'idle';
}

function eventIcon(type: string) {
  if (type.includes('retrieval')) return BookOpen;
  if (type.includes('tool')) return Wrench;
  if (type.includes('approval')) return ShieldAlert;
  if (type.includes('finished')) return Check;
  return Clock3;
}

export function RunObservatory({ open, running, events, onClose, onStop }: { open: boolean; running: boolean; events: ObservatoryEvent[]; onClose: () => void; onStop: () => void }) {
  const phase = phaseFor(events, running);
  const visibleEvents = events.length ? events : [{ type: 'workflow_started', run_id: 0, created_at: new Date().toISOString(), payload: { note: '等待第一条运行记录…' } } as RuntimeEvent];
  const toolEvents = events.filter((event) => runtimeType(event).includes('tool'));
  const retrievalEvents = events.filter((event) => runtimeType(event).includes('retrieval'));
  const approvalEvents = events.filter((event) => runtimeType(event).includes('approval'));

  return <AnimatePresence>{open ? <motion.section className={`run-observatory phase-${phase}`} initial={{ opacity: 0, scale: .985 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0, scale: .99 }} transition={{ type: 'spring', stiffness: 240, damping: 28 }}>
    <ParchmentBackground scene="run" state={phase} />
    <header className="observatory-head"><div><span className="folio-index">Experimentum · live folio</span><h2>Run Observatory <em>{phase}</em></h2></div><div className="observatory-actions"><span className={`run-state state-${phase}`}><i />{running ? '运行中' : phase === 'complete' ? '已完成' : phase}</span>{running ? <Button tone="danger" size="small" onClick={onStop}><CircleStop size={15} /> 停止</Button> : null}<IconButton aria-label="关闭运行观察台" onClick={onClose}><X size={17} /></IconButton></div></header>
    <aside className="experiment-trace deckle-paper">
      <div className="paper-ribbon">Experiment trace</div>
      <div className="trace-manuscript">{visibleEvents.map((event, index) => { const type = runtimeType(event); const Icon = eventIcon(type); return <div className={`trace-step${index === visibleEvents.length - 1 && running ? ' active' : ''}`} key={index}><span className="trace-number">{index + 1}</span><Icon size={16} /><span><strong>{humanize(type)}</strong><small>{'created_at' in event ? new Date(event.created_at).toLocaleTimeString() : 'now'}</small></span></div>; })}</div>
    </aside>
    <main className="run-transcript deckle-paper">
      <div className="transcript-heading"><span>Conversation / Run transcript</span><span>{visibleEvents.length} records</span></div>
      <div className="transcript-list scroll-surface">{visibleEvents.map((event, index) => { const type = runtimeType(event); const Icon = eventIcon(type); return <article className={`transcript-entry entry-${type.includes('failed') || type === 'error' ? 'error' : 'normal'}`} key={index}><div className="entry-icon"><Icon size={17} /></div><div><div className="entry-title"><strong>{humanize(type)}</strong><span>#{String(index + 1).padStart(2, '0')}</span></div><pre>{prettyJson(event)}</pre></div></article>; })}</div>
      <div className="observatory-composer"><span className="script-quill">Scrivi una nota per questa esecuzione…</span><Search size={17} /></div>
    </main>
    <aside className="run-notes">
      <div className="paper-ribbon thinking-ribbon">{running ? 'Cogitatio in progress…' : phase === 'complete' ? 'Opus completum' : 'Folio quietum'}</div>
      <section className="run-note note-tool deckle-paper pinned-paper"><h3><Wrench size={16} /> Invocazione strumento</h3><p>{toolEvents.length ? `${toolEvents.length} 条工具记录已写入实验册。` : '等待工具机构响应。'}</p></section>
      <section className="run-note note-approval deckle-paper pinned-paper"><h3><ShieldAlert size={16} /> 审批批注</h3><p>{approvalEvents.length ? '发现需要人工确认的检查点。' : '当前没有待审批的检查点。'}</p>{approvalEvents.length ? <Button size="small">审阅记录</Button> : null}</section>
      <section className="run-note note-source deckle-paper pinned-paper"><h3><BookOpen size={16} /> 来源与检索</h3><p>{retrievalEvents.length ? `${retrievalEvents.length} 条检索事件，引用已附于正文。` : '尚未查阅知识卷册。'}</p></section>
    </aside>
  </motion.section> : null}</AnimatePresence>;
}
