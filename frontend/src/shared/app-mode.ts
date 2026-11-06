// 顶层应用模式（助手模式 P2 拍板）：AppMode = "project" | "assistant"。
//
// 「助手」不再是主域工作区的一个页签（workspace-tabs 的 WorkspacePage 已摘除
// "assistant"）——它是与项目模式**并列的顶层模式**：切到助手模式时整个
// Sidebar/TabBar/主时间线让位给 AssistantWorkspace（助手域会话列表 + 时间线），
// 两个模式各持自己的工作区状态，互不覆盖。
//
// 放在 workspace-tabs.ts 之外的理由：该文件描述的是**项目模式内部**的页签模型
// （固定页签 + 子会话临时视图），顶层模式是它的上一级概念——塞进去会让「项目
// 模式的页签」与「模式本身」两个层级混在一个文件里。

export type AppMode = "project" | "assistant";

/** 记住离开项目模式时用户正在看的视图（切回项目模式时恢复——不掉回 chat）。
 *  视图键与项目模式的 WorkspaceView 同域（"chat" / 固定页签 / child:<id>）。 */
let lastProjectView: string = "chat";

/** lastAssistantSession 的 localStorage 持久化键（记忆跨重启有效——进入助手
 *  模式恢复上次会话；键固定，读写失败静默当无记忆，与 theme.ts 同款纪律）。 */
const LS_LAST_ASSISTANT = "lx.app.lastAssistantSession";

/** 切到助手模式：先记住项目模式当前的视图，再换模式。 */
export function enterAssistantMode(currentProjectView: string): { mode: AppMode; savedView: string } {
  lastProjectView = currentProjectView;
  return { mode: "assistant", savedView: currentProjectView };
}

/** 切回项目模式：恢复离开时的视图（lastProjectView 记忆）。 */
export function enterProjectMode(): { mode: AppMode; restoredView: string } {
  return { mode: "project", restoredView: lastProjectView };
}

/** 测试口：重置记忆（生产不调用）。 */
export function resetLastProjectView(view: string = "chat"): void {
  lastProjectView = view;
}

// ---- lastAssistantSession：上次助手会话记忆（2026-12）----
// 进入助手模式不再自动新建（AssistantPage 的 boot 已是纯装载语义），改为恢复
// 上次会话：有记忆直接装载历史，无记忆显示中性态。记忆在用户点会话行 / 新建
// 会话时更新（AssistantWorkspace 的 handleSelect / handleNewSession）。

/** 读上次助手会话 id（无记忆 / 读不到 = 空串——调用方按中性态处理）。 */
export function readLastAssistantSession(): string {
  try {
    return localStorage.getItem(LS_LAST_ASSISTANT) ?? "";
  } catch {
    return "";
  }
}

/** 写记忆（id 为空 = 清除记忆——语义与「未选择」一致）。 */
export function writeLastAssistantSession(id: string): void {
  try {
    if (id) localStorage.setItem(LS_LAST_ASSISTANT, id);
    else localStorage.removeItem(LS_LAST_ASSISTANT);
  } catch {
    /* 隐私模式等——不持久化而已 */
  }
}

/** 测试口：清掉持久化的助手会话记忆（生产不调用）。 */
export function resetLastAssistantSession(): void {
  writeLastAssistantSession("");
}
