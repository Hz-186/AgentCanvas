import type { ReactNode } from 'react';
import type { SketchCategory } from '@/stores/thinkingStore';

/**
 * 手稿片段定义。
 * 每个片段散布在 viewBox(0..1600 × 0..1000) 的不同区域，
 * 拥有各自的语义类别、基础/峰值透明度与错峰节律，
 * 由 ParchmentBackground 的动画引擎异步调制其渗出程度。
 */
export interface SketchFragment {
  id: string;
  category: SketchCategory;
  /** 基础可见度（空闲时的历史痕迹），0..1 */
  base: number;
  /** 峰值可见度（完全渗出时），0..1，保持克制 */
  peak: number;
  /** 呼吸周期（毫秒），越大越缓慢 */
  period: number;
  /** 相位偏移（0..1），令各片段错峰 */
  phase: number;
  /** 该片段的 SVG 内容（相对自身分组的局部坐标） */
  render: ReactNode;
}

const S = 'currentColor';

/** 平行排线：在给定矩形内生成一组细密斜线，模拟手绘阴影排线。 */
function hatch(x: number, y: number, w: number, h: number, gap: number, width = 0.6, skew = 0): ReactNode {
  const lines: ReactNode[] = [];
  for (let i = 0; x + i <= x + w; i += gap) {
    const lx = x + i;
    lines.push(<line key={`h${i}`} x1={lx} y1={y} x2={lx + skew} y2={y + h} stroke={S} strokeWidth={width} />);
    if (i > w) break;
  }
  return <g>{lines}</g>;
}

export { hatch };
