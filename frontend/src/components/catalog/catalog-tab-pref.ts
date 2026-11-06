// 拓展页初始页签的跨组件传递（「助手配置」入口 → CatalogPage）。
//
// 为什么走模块级传递而不是 App state：跳转入口（项目模式 TabBar / 助手模式
// 内嵌视图）的打开动作都只带固定页 tab 语义——为传一个「打开 catalog 时选中
// 助手页签」给四层深的 props 链（App → TabBar → page）加一个 state 字段，
// 波及面远超一个跨组件一次性偏好的收益。这里是一次性的「下一次 CatalogPage
// 挂载选中哪个页签」，take 语义（读走即清）保证它不影响用户手动切页签。
export type CatalogTabPref = "tools" | "skills" | "templates" | "mcp" | "assistant";

let pref: CatalogTabPref | null = null;

/** 预设下一次 CatalogPage 挂载的初始页签（「助手配置」入口调用——项目模式
 *  TabBar 或助手模式内嵌视图的 nav）。 */
export function setCatalogTabPref(tab: CatalogTabPref): void {
  pref = tab;
}

/** 取走偏好（读走即清——返回 null = 无预设，用默认 tools）。 */
export function takeCatalogTabPref(): CatalogTabPref | null {
  const p = pref;
  pref = null;
  return p;
}

/** 测试口：清偏好（生产不调用）。 */
export function resetCatalogTabPref(): void {
  pref = null;
}
