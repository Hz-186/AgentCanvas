import { create } from 'zustand';

/**
 * ThinkingStore — 驱动羊皮纸背景的"活手稿"状态。
 *
 * mode 概念性地回应产品状态；activity 是一个 0..1 的平滑目标强度，
 * ParchmentBackground 会用 requestAnimationFrame 缓慢地 lerp 逼近它，
 * 并据此调制不同区域手稿的渗出程度。
 */
export type ThinkingMode = 'idle' | 'thinking' | 'tool' | 'retrieval' | 'running' | 'settling' | 'error';

interface ThinkingState {
  mode: ThinkingMode;
  /** 目标活跃强度 0..1，由 mode 推导，也可被 pulse 临时抬升 */
  activity: number;
  /** 单调递增的令牌，用于让消费者感知"又一次推演脉冲" */
  pulseToken: number;
  setMode: (mode: ThinkingMode) => void;
  pulse: () => void;
  reset: () => void;
}

const MODE_ACTIVITY: Record<ThinkingMode, number> = {
  idle: 0.06,
  thinking: 0.62,
  tool: 0.7,
  retrieval: 0.66,
  running: 0.58,
  settling: 0.24,
  error: 0.16,
};

export const useThinkingStore = create<ThinkingState>((set, get) => ({
  mode: 'idle',
  activity: MODE_ACTIVITY.idle,
  pulseToken: 0,
  setMode: (mode) => set({ mode, activity: MODE_ACTIVITY[mode] }),
  pulse: () => set({ pulseToken: get().pulseToken + 1, activity: Math.min(1, get().activity + 0.12) }),
  reset: () => set({ mode: 'idle', activity: MODE_ACTIVITY.idle }),
}));

/** 手稿片段的语义类别，用于按 mode 加权控制不同区域的渗出程度。 */
export type SketchCategory = 'geometry' | 'mechanism' | 'text' | 'flow' | 'wave';

/**
 * 每个 mode 对不同手稿类别的权重（0..1）。
 * 让"思考"偏几何与流程，"工具"偏机械，"检索"偏文本页边批注。
 */
export const MODE_CATEGORY_WEIGHT: Record<ThinkingMode, Record<SketchCategory, number>> = {
  idle:      { geometry: 0.32, mechanism: 0.28, text: 0.30, flow: 0.32, wave: 0.28 },
  thinking:  { geometry: 1.00, mechanism: 0.55, text: 0.62, flow: 0.95, wave: 0.50 },
  tool:      { geometry: 0.62, mechanism: 1.00, text: 0.42, flow: 0.82, wave: 0.46 },
  retrieval: { geometry: 0.50, mechanism: 0.36, text: 1.00, flow: 0.56, wave: 0.72 },
  running:   { geometry: 0.86, mechanism: 0.86, text: 0.56, flow: 1.00, wave: 0.60 },
  settling:  { geometry: 0.54, mechanism: 0.46, text: 0.46, flow: 0.56, wave: 0.46 },
  error:     { geometry: 0.42, mechanism: 0.52, text: 0.32, flow: 0.36, wave: 0.26 },
};
