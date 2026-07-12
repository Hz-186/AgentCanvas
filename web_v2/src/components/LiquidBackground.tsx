import { useEffect } from 'react';

export function LiquidBackground() {
  useEffect(() => {
    const onMove = (event: PointerEvent) => {
      const x = `${(event.clientX / window.innerWidth) * 100}%`;
      const y = `${(event.clientY / window.innerHeight) * 100}%`;
      document.documentElement.style.setProperty('--mx', x);
      document.documentElement.style.setProperty('--my', y);
    };
    window.addEventListener('pointermove', onMove, { passive: true });
    return () => window.removeEventListener('pointermove', onMove);
  }, []);

  return <div className="liquid-bg" aria-hidden="true" />;
}
