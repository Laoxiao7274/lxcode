// 主题三态（跟随系统/深色/浅色）：解析与应用的唯一入口。
//
// 背景：主 UI 的设计语言是「白底近黑字」（base.css :root），而 aicss 的 AI 内容卡
// 自带 OS 暗色分支——暗色系统下卡片文字翻白、容器却还是亮色，白字落白底看不清
// （用户反馈「文字输出都是纯白」的根因）。修复方式不是锁死亮色，而是把主题做成
// 完整三态：html 根属性 data-theme ∈ "auto" | "dark" | "light"，auto 时由
// prefers-color-scheme 媒体查询决定亮暗（base.css 与 aicss 各自的暗色覆盖块）。
//
// 纪律：
// - resolveTheme 是纯函数（偏好 + 系统亮暗 → 实际亮暗），测试直接测它；
// - applyTheme 只碰 DOM（html 属性），持久化归 persistTheme（localStorage），
//   职责分开——设置面板切换时先 persist 再 apply，测试可各测各的；
// - 浏览器/Electron 桥（__LX__.setThemePreference）是可选能力：没有壳就不同步
//   nativeTheme（滚动条/原生控件的系统侧跟随只在 Electron 里做）。

export type ThemePreference = "system" | "dark" | "light";
export type ResolvedTheme = "dark" | "light";

/** localStorage 持久化键（index.html 的防闪内联脚本读同一个键，两边必须一致）。 */
export const THEME_KEY = "lxcode.theme";

/** html 根属性上的值：偏好 system 映射为 auto（CSS 媒体查询接管）。 */
export type ThemeAttr = "auto" | "dark" | "light";

const PREFS: readonly ThemePreference[] = ["system", "dark", "light"];

export function isThemePreference(v: unknown): v is ThemePreference {
  return typeof v === "string" && (PREFS as readonly string[]).includes(v);
}

/** 偏好 → 实际亮暗。system 跟随系统；dark/light 直接生效。 */
export function resolveTheme(pref: ThemePreference, systemScheme: ResolvedTheme): ResolvedTheme {
  return pref === "system" ? systemScheme : pref;
}

/** 读系统亮暗（无 matchMedia 的环境回落 light——与 :root 默认 token 一致）。 */
export function systemScheme(): ResolvedTheme {
  return typeof window !== "undefined" && window.matchMedia?.("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

/** 偏好 → html data-theme 属性值（system 记 auto，CSS 的媒体查询块认它）。 */
export function themeAttr(pref: ThemePreference): ThemeAttr {
  return pref === "system" ? "auto" : pref;
}

/** 把偏好落到 DOM：html data-theme（CSS token 切换）。返回实际亮暗。 */
export function applyTheme(pref: ThemePreference): ResolvedTheme {
  const resolved = resolveTheme(pref, systemScheme());
  document.documentElement.setAttribute("data-theme", themeAttr(pref));
  return resolved;
}

/** 持久化偏好（下次启动 index.html 的内联脚本首帧前读它）。 */
export function persistTheme(pref: ThemePreference): void {
  try { localStorage.setItem(THEME_KEY, pref); } catch { /* 隐私模式等——不持久化而已 */ }
}

/** 读持久化的偏好；无记录/值不认识返回 null（调用方回落默认 system）。 */
export function loadTheme(): ThemePreference | null {
  try {
    const v = localStorage.getItem(THEME_KEY);
    return isThemePreference(v) ? v : null;
  } catch { return null; }
}

/** 通知 Electron 壳同步 nativeTheme（浏览器模式无桥，静默跳过）。 */
function syncShell(pref: ThemePreference): void {
  const bridge = (window as unknown as { __LX__?: { setThemePreference?: (p: string) => void } }).__LX__;
  bridge?.setThemePreference?.(pref);
}

/**
 * 应用完整偏好：DOM + 持久化 + 壳同步。返回实际亮暗。
 * 设置面板的 onChange 与启动初始化都走这一个口，保证三个落点不漂移。
 */
export function setTheme(pref: ThemePreference): ResolvedTheme {
  persistTheme(pref);
  const resolved = applyTheme(pref);
  syncShell(pref);
  return resolved;
}

/**
 * 启动初始化：读持久化偏好（缺省 system）应用到 DOM，并监听系统亮暗变化
 * （只有跟随系统时才需要重应用）。返回注销函数。
 */
export function initTheme(): () => void {
  const pref = loadTheme() ?? "system";
  applyTheme(pref);
  syncShell(pref);
  const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
  const onChange = (): void => {
    if ((loadTheme() ?? "system") === "system") applyTheme("system");
  };
  mq?.addEventListener?.("change", onChange);
  return () => mq?.removeEventListener?.("change", onChange);
}
