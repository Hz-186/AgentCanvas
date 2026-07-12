import parchmentBackground from '@/assets/manuscript/parchment-background.webp';
import parchmentFolio from '@/assets/manuscript/parchment-folio.webp';
import parchmentNote from '@/assets/manuscript/parchment-note.webp';
import authAgentFlow from '@/assets/manuscript/auth-agent-flow.png';
import canvasCelestialEngine from '@/assets/manuscript/canvas-celestial-engine.png';
import runObservatory from '@/assets/manuscript/run-observatory.png';
import workflowsArchive from '@/assets/manuscript/workflows-archive.png';

export const manuscriptAssets = {
  texture: { background: parchmentBackground, folio: parchmentFolio, note: parchmentNote },
  plate: { auth: authAgentFlow, canvas: canvasCelestialEngine, run: runObservatory, workflows: workflowsArchive },
} as const;

