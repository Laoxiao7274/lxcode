// 灯箱（图片点击放大）的**纯函数**模块：状态机、钳制、多图索引、平移算术。
// 组件只消费这里的判定；全部可测（tests/lightbox.test.mjs 的钉子）。
//
// 设计取自主流图片查看器（千问/微信/Chrome）：打开即「适配视口」的基准态，
// 滚轮以光标为中心缩放（25%~800%），双击在「100%」与「适配」间切换，
// 放大后可拖动平移，多图左右切换。

export const LIGHTBOX_MIN_SCALE = 0.25;
export const LIGHTBOX_MAX_SCALE = 8;

/** 缩放钳制：25%~800%。越界一律钉回边界；NaN 回 1（不产生坏 transform）。 */
export function clampScale(scale: number): number {
  if (!Number.isFinite(scale)) return Number.isNaN(scale) ? 1 : scale > 0 ? LIGHTBOX_MAX_SCALE : LIGHTBOX_MIN_SCALE;
  return Math.min(LIGHTBOX_MAX_SCALE, Math.max(LIGHTBOX_MIN_SCALE, scale));
}

/** 滚轮一步的缩放因子：>1 放大、<1 缩小。指数步进（乘法）比加法手感均匀。 */
export const LIGHTBOX_WHEEL_STEP = 1.1;

/** 滚轮缩放：钳制后返回。scale 与 delta 由组件传，方向约定 delta>0 = 放大。 */
export function wheelScale(scale: number, delta: number): number {
  const factor = delta > 0 ? LIGHTBOX_WHEEL_STEP : 1 / LIGHTBOX_WHEEL_STEP;
  return clampScale(scale * factor);
}

/**
 * 缩放后的平移钳制：保证图片至少一角留在视口内（拖丢找不回是最常见的
 * 灯箱翻车点）。数学：图心允许偏离视口中心的最大距离 = (图显示尺寸 − 视口)/2
 * 且下限 0——图小于视口时没有自由度 → 图吸附回中心；只有图超出视口的方向
 * 允许拖动，且最多拖到图缘贴视口缘。
 */
export function clampOffset(offset: { x: number; y: number }, scale: number, fit: { width: number; height: number }, viewport: { width: number; height: number }): { x: number; y: number } {
  const limitX = Math.max(0, (fit.width * scale - viewport.width) / 2);
  const limitY = Math.max(0, (fit.height * scale - viewport.height) / 2);
  const clamp = (v: number, lim: number) => {
    const r = Math.min(lim, Math.max(-lim, v));
    return r === 0 ? 0 : r; // 归一化 -0（transform 里 -0 与 0 等价，但保持纯函数输出的整洁）
  };
  return { x: clamp(offset.x, limitX), y: clamp(offset.y, limitY) };
}

/** 光标为中心的缩放：锚点视口坐标 a 的内容坐标 c = a - o 不随缩放变，
 *  解 o' = a - c·(s'/s)。返回新偏移（未钳制，钳制由 clampOffset 做）。 */
export function anchorZoomOffset(offset: { x: number; y: number }, from: number, to: number, anchor: { x: number; y: number }): { x: number; y: number } {
  if (from <= 0) return { ...offset };
  const k = to / from;
  return { x: anchor.x - (anchor.x - offset.x) * k, y: anchor.y - (anchor.y - offset.y) * k };
}

/** 双击语义：当前在适配（±0.01 容差）→ 100%；否则 → 适配（偏移归零）。 */
export function doubleClickScale(scale: number, fitScale: number): { scale: number; reset: boolean } {
  return Math.abs(scale - fitScale) <= 0.01 ? { scale: 1, reset: false } : { scale: fitScale, reset: true };
}

/** 多图索引步进：+1/-1 环形（n<=1 或非法步长原地不动）。 */
export function stepIndex(index: number, n: number, step: number): number {
  if (n <= 1) return 0;
  if (step === 0) return index;
  const d = ((index + step) % n + n) % n;
  return d;
}

/** 显示百分比：0.25 → "25%"、1 → "100%"、8 → "800%"。 */
export function scaleLabel(scale: number): string {
  return `${Math.round(scale * 100)}%`;
}

/**
 * 适配视口的显示尺寸与比例：contain 缩放（与 CSS object-fit:contain 同义），
 * 92vw/92vh 边距由调用方作为 viewport 传入（纯函数不读 window）。
 * 退化输入（0 尺寸）返回 null——调用方等 img 的 load 事件再算。
 */
export function fitSize(natural: { width: number; height: number }, viewport: { width: number; height: number }, margin = 0.92): { width: number; height: number } | null {
  if (!natural.width || !natural.height || !viewport.width || !viewport.height) return null;
  const s = Math.min((viewport.width * margin) / natural.width, (viewport.height * margin) / natural.height, 1);
  return { width: natural.width * s, height: natural.height * s };
}
