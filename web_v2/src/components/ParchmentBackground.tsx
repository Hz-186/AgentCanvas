import { useEffect, useRef } from 'react';
import { manuscriptAssets } from '@/components/manuscript/assets';

export type ManuscriptScene = 'auth' | 'canvas' | 'run' | 'workflows' | 'general';
export type ManuscriptState = 'idle' | 'thinking' | 'retrieval' | 'tool' | 'approval' | 'complete' | 'error';

const scriptByScene: Record<ManuscriptScene, string[]> = {
  auth: ['ordine delle operazioni', 'memoria e ragionamento', 'lo strumento risponde al pensiero', 'ogni ramo ritorna alla causa', 'la mente artificiale osserva e decide'],
  canvas: ['misura degli astri e dei moti', 'ogni nodo muove il seguente', 'cerchi concentrici della ragione', 'proporzione tra causa ed effetto', 'la macchina conserva memoria'],
  run: ['esperimento in corso', 'il tempo rivela ogni passaggio', 'strumenti collegati alla prova', 'osservare prima di concludere', 'la risposta nasce dal movimento'],
  workflows: ['indice delle opere', 'fogli legati secondo il loro ordine', 'archivio delle macchine pensanti', 'ogni disegno conserva una storia', 'versione approvata e sigillata'],
  general: ['codice delle memorie', 'strumenti e osservazioni', 'ordine delle conoscenze', 'note per una macchina futura', 'la carta conserva ogni traccia'],
};

export function ParchmentBackground({ scene = 'general', state = 'idle' }: { scene?: ManuscriptScene; state?: ManuscriptState }) {
  const layerRef = useRef<HTMLDivElement | null>(null);
  const point = useRef({ x: 0, y: 0 });
  const target = useRef({ x: 0, y: 0 });
  const raf = useRef<number | null>(null);
  const plate = scene === 'general' ? null : manuscriptAssets.plate[scene];

  useEffect(() => {
    const move = (event: PointerEvent) => { target.current = { x: (event.clientX / innerWidth - .5) * 10, y: (event.clientY / innerHeight - .5) * 7 }; };
    const tick = () => {
      point.current.x += (target.current.x - point.current.x) * .04;
      point.current.y += (target.current.y - point.current.y) * .04;
      if (layerRef.current) layerRef.current.style.transform = `translate3d(${point.current.x}px,${point.current.y}px,0)`;
      raf.current = requestAnimationFrame(tick);
    };
    addEventListener('pointermove', move, { passive: true }); raf.current = requestAnimationFrame(tick);
    return () => { removeEventListener('pointermove', move); if (raf.current) cancelAnimationFrame(raf.current); };
  }, []);

  return <div className={`parchment-bg scene-${scene} state-${state}`} aria-hidden="true">
    <div className="parchment-base" style={{ backgroundImage: `url(${manuscriptAssets.texture.background})` }} />
    <div className="parchment-fiber" /><div className="parchment-grid" />
    <div className="manuscript-art-layer" ref={layerRef}>{plate ? <img src={plate} alt="" className="manuscript-plate" /> : null}</div>
    <svg className="drafting-guides" viewBox="0 0 1600 1000" preserveAspectRatio="xMidYMid slice">
      <g><circle cx="238" cy="225" r="142"/><circle cx="238" cy="225" r="94"/><path d="M70 225h336M238 57v336M138 125l200 200M338 125L138 325"/><circle cx="1330" cy="760" r="118"/><ellipse cx="1330" cy="760" rx="165" ry="48"/><path d="M1148 760h364M1330 575v370"/></g>
    </svg>
    <div className="script-field">{scriptByScene[scene].map((text, index) => <span className={`script-line script-${index + 1}${index % 2 ? ' mirrored' : ''}`} key={text}>{text}</span>)}</div>
    <div className="ink-transfer" /><div className="parchment-vignette" />
  </div>;
}
