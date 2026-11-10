// 界面字体与字号档（设置 → 外观）：持久化与 DOM 应用的唯一入口，结构对齐
// theme.ts（resolve 纯函数可选不需要——字体是直接值，不做三态解析）。
//
// 机制：base.css :root 的 --font-sans/--font-mono 改为可覆盖模式——
//   --font-sans: var(--app-font-sans, "Inter Variable"), …;
// 本模块把用户选择写到 html 根属性 --app-font-sans / --app-font-mono /
// --app-font-scale 上；不设置（默认）时回落 token 里的默认栈，与历史渲染一致。
//
// 纪律（同 theme.ts）：
// - apply 只碰 DOM（documentElement.style.setProperty），持久化归 persist，
//   职责分开；localStorage 读写失败静默回落默认（隐私模式等）；
// - index.html 的防闪内联脚本读同一个键（UI_FONT_KEY / UI_FONT_SCALE_KEY，
//   两边必须一致），首帧前先应用，避免字体跳变。

export type UiFontId = "default" | "system" | "msyh" | "cascadia" | "jetbrains";

/** localStorage 持久化键（index.html 防闪脚本读同一组键，两边必须一致）。 */
export const UI_FONT_KEY = "lxcode.ui.font";
export const UI_FONT_SCALE_KEY = "lxcode.ui.fontScale";

/** 字号档（--app-font-scale）：作用在 base.css 的 html { zoom: var(...) }。
 *  zoom 是 Chromium 标准属性，缩放整个渲染面——包括各组件硬编码 px 的字号
 *  与布局尺寸，全局生效。注意 zoom 同时缩放布局：窄窗下大档位（1.1/1.2）
 *  会比默认档更早触发小屏断点。 */
export const FONT_SCALES: { id: string; label: string; scale: number }[] = [
  { id: "default", label: "默认", scale: 1 },
  { id: "compact", label: "紧凑", scale: 0.92 },
  { id: "large", label: "大", scale: 1.1 },
  { id: "xlarge", label: "特大", scale: 1.2 },
];

/** 预置字体栈（id → CSS font-family 栈）。default = 不写覆盖属性，走 token 默认。 */
export const UI_FONTS: { id: UiFontId; label: string; stack?: string }[] = [
  { id: "default", label: "默认（Inter）" },
  { id: "system", label: "系统默认" , stack: 'system-ui, -apple-system, "Segoe UI", "Microsoft YaHei", sans-serif' },
  { id: "msyh", label: "微软雅黑", stack: '"Microsoft YaHei", "PingFang SC", "Noto Sans SC", sans-serif' },
  { id: "cascadia", label: "Cascadia Mono", stack: '"Cascadia Mono", "Cascadia Code", Consolas, monospace' },
  { id: "jetbrains", label: "JetBrains Mono", stack: '"JetBrains Mono", "JetBrains Mono Variable", monospace' },
];

function isKnownFontId(v: unknown): v is UiFontId {
  return typeof v === "string" && UI_FONTS.some((f) => f.id === v);
}

/** 字体 id → CSS 栈；空串/null = 默认（清掉覆盖属性，回落 token）。 */
function stackFor(id: UiFontId): string | null {
  return UI_FONTS.find((f) => f.id === id)?.stack ?? null;
}

/** 把当前偏好落到 DOM（html 根属性 --app-font-*）。scale 为 null = 默认 1（清属性）。 */
export function applyUiFont(fontId: UiFontId | null, scaleId?: string | null): void {
  if (typeof document === "undefined") return; // SSR/测试环境——无 DOM 可应用
  const root = document.documentElement;
  const stack = fontId ? stackFor(fontId) : null;
  if (stack) root.style.setProperty("--app-font-sans", stack);
  else root.style.removeProperty("--app-font-sans");
  // mono 跟随界面字体选择（预置里两个 mono 栈同时覆盖 sans 与 mono；
  // 其余清掉 mono 覆盖，回落 token 默认 JetBrains Mono）。
  if (stack && /mono|Consolas/i.test(stack)) root.style.setProperty("--app-font-mono", stack);
  else root.style.removeProperty("--app-font-mono");
  const scale = scaleId ? FONT_SCALES.find((s) => s.id === scaleId)?.scale : undefined;
  if (scale != null && scale !== 1) root.style.setProperty("--app-font-scale", String(scale));
  else root.style.removeProperty("--app-font-scale");
}

/** 持久化偏好（下次启动 index.html 的内联脚本首帧前读它）。 */
export function persistUiFont(fontId: UiFontId, scaleId?: string): void {
  try { localStorage.setItem(UI_FONT_KEY, fontId); } catch { /* 不持久化而已 */ }
  if (scaleId) {
    try { localStorage.setItem(UI_FONT_SCALE_KEY, scaleId); } catch { /* 同上 */ }
  }
}

/** 读持久化字体 id；无记录/值不认识返回 null（调用方回落 default）。 */
export function loadUiFont(): UiFontId | null {
  try {
    const v = localStorage.getItem(UI_FONT_KEY);
    return isKnownFontId(v) ? v : null;
  } catch { return null; }
}

/** 读持久化字号档 id；无记录/值不认识返回 null（调用方回落 default）。 */
export function loadUiFontScale(): string | null {
  try {
    const v = localStorage.getItem(UI_FONT_SCALE_KEY);
    return v && FONT_SCALES.some((s) => s.id === v) ? v : null;
  } catch { return null; }
}

/**
 * 应用完整偏好：DOM + 持久化。设置面板的 onChange 与启动初始化都走这一个口。
 * scaleId 省略时沿用当前持久化档（只改字体不动字号）。
 */
export function setUiFont(fontId: UiFontId, scaleId?: string): void {
  persistUiFont(fontId, scaleId ?? loadUiFontScale() ?? undefined);
  applyUiFont(fontId, scaleId ?? loadUiFontScale());
}

/** 只改字号档（DOM + 持久化），字体不动。 */
export function setUiFontScale(scaleId: string): void {
  persistUiFont(loadUiFont() ?? "default", scaleId);
  applyUiFont(loadUiFont(), scaleId);
}

/** 启动初始化：读持久化偏好（缺省 = 不覆盖，token 默认栈）应用到 DOM。 */
export function initUiFont(): void {
  applyUiFont(loadUiFont(), loadUiFontScale());
}
