// 助手模式子会话临时视图的跨模式注入（2026-12「来自助手」跳转）。
//
// 为什么走模块级传递而不是 App props：跳转入口在**项目模式**的 Sidebar 里
//（props 链 App → Sidebar 需加一跳），消费方在**助手模式**的 AssistantWorkspace
//（另一棵子树）——同一个跨组件一次性偏好的形状，与 catalog-tab-pref 同款：
// 「下一次 AssistantWorkspace 挂载时直接打开某个子会话临时视图」。take 语义
//（读走即清）保证它不影响之后用户在助手模式里的常规导航；且不占 props 面。
//
// 时序保证：App 的 handleModeChange("assistant") 之后 AssistantWorkspace 才
// 挂载（appMode === "assistant" 分支渲染），useState 初始化器读 take——
// 一次性消费，晚于它的挂载（用户切走再切回）读到 null = 不注入。

let pref: string | null = null;

/** 预设下一次 AssistantWorkspace 挂载直接打开的子会话 id（「来自助手」行
 *  点击调用——App 的 onOpenAssistantChild 链）。 */
export function setAssistantChildPref(childId: string): void {
  pref = childId;
}

/** 取走偏好（读走即清——返回 null = 无预设，正常进入助手模式）。 */
export function takeAssistantChildPref(): string | null {
  const p = pref;
  pref = null;
  return p;
}

/** 测试口：清偏好（生产不调用）。 */
export function resetAssistantChildPref(): void {
  pref = null;
}
