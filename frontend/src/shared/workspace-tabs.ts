// 工作区标签状态模型：固定页签（Agent/拓展/Git）+ 可关闭的**子会话标签**。
//
// 子会话标签（子 Agent = 独立会话，AGENTS.md §2.3）与固定页签共用同一套「打开顺序 /
// 焦点 / 关闭回退」语义，所以不另起一个状态机——只把标签键从 WorkspacePage 泛化成
// **字符串键** `child:<sessionId>`。
//
// 为什么坚持字符串键而不是对象：本文件与调用方（App / TabBar）大量按值比较
// （includes / filter / ===），换成对象会让每一处比较都退化成按引用比，波及面远超这一步
// 的范围；字符串键是天然的判等凭据。
import type { ThreadBlock } from "./blocks";

export type WorkspacePage = "agents" | "catalog" | "git";
/** 子会话标签键（模板字面量类型：手写拼接的键在编译期就能被发现）。 */
export type ChildTabKey = `child:${string}`;
/** 可关闭的工作区标签：固定页签 + 子会话标签。 */
export type WorkspaceTab = WorkspacePage | ChildTabKey;
/** 工作区视图：聊天（恒在）+ 可关闭标签。 */
export type WorkspaceView = "chat" | WorkspaceTab;

export interface WorkspaceTabsState {
  /** 按打开顺序排列；聚焦不会改变顺序。 */
  tabs: WorkspaceTab[];
  active: WorkspaceView;
  /** 最近焦点优先，用于关闭当前标签后的回退。 */
  history: WorkspaceView[];
}

export function createWorkspaceTabs(): WorkspaceTabsState {
  return { tabs: [], active: "chat", history: [] };
}

/** 子会话标签的键。**唯一**的拼键处——调用方手写 `"child:" + id` 早晚会拼错。 */
export function childTabKey(sessionId: string): ChildTabKey {
  return `child:${sessionId}`;
}

/** 键 → 子会话 id（非子会话键返回 null）。
 *
 *  固定页签（agents/catalog/git）与 chat 都不带这个前缀，天然不会被误认成子会话键。
 *  `child:` 后面为空 = 坏键（会渲染成一个没有信息的空标签），返回 null 而不是空串。 */
export function childTabSession(key: string): string | null {
  if (!key.startsWith("child:")) return null;
  const sessionId = key.slice("child:".length);
  return sessionId === "" ? null : sessionId;
}

/** 是不是固定页签（App 按它分流：固定页签走既有打开逻辑，其余按子会话标签处理）。 */
export function isWorkspacePage(tab: WorkspaceTab): tab is WorkspacePage {
  return tab === "agents" || tab === "catalog" || tab === "git";
}

/** 打开/聚焦一个工作区标签（固定页签与子会话标签共用这一条路径）。
 *
 *  **去重就在这里**：已在 tabs 里的键不重复 push——重复打开同一个子会话必须回到同一个
 *  标签，而不是并排长出两个一模一样的标签（关掉一个还剩一个，用户会以为关不掉）。
 *  tests/child-tab.test.mjs ② 钉住这条：把这里的 includes 判定去掉，那条测试立刻变红。 */
export function focusWorkspaceTab(state: WorkspaceTabsState, tab: WorkspaceTab): WorkspaceTabsState {
  const tabs = state.tabs.includes(tab) ? state.tabs : [...state.tabs, tab];
  return focusView(state, tab, tabs);
}

export function focusWorkspacePage(state: WorkspaceTabsState, page: WorkspacePage): WorkspaceTabsState {
  return focusWorkspaceTab(state, page);
}

/** 打开/聚焦子会话标签（DispatchCard 的「打开子会话」→ 独立工作区标签）。 */
export function focusChildTab(state: WorkspaceTabsState, sessionId: string): WorkspaceTabsState {
  // 空 id 不是合法子会话：开一个空键标签只会得到一个标题回落到「子会话 未知」的空白标签，
  // 不如不开（no-op）
  if (sessionId === "") return state;
  return focusWorkspaceTab(state, childTabKey(sessionId));
}

export function focusChatTab(state: WorkspaceTabsState): WorkspaceTabsState {
  return focusView(state, "chat", state.tabs);
}

/** 关闭一个工作区标签（固定页签与子会话标签同一套语义，**逐字段保持原行为**）：
 *  非当前标签 → 只从 tabs/history 摘掉；当前标签 → 回退到最近仍打开的标签（没有就回 chat）。
 *  未知键是 no-op（返回原对象）。 */
export function closeWorkspacePage(state: WorkspaceTabsState, tab: WorkspaceTab): WorkspaceTabsState {
  if (!state.tabs.includes(tab)) return state;

  const tabs = state.tabs.filter((item) => item !== tab);
  const history = state.history.filter((item) => item === "chat" || (item !== tab && tabs.includes(item)));
  if (state.active !== tab) return { ...state, tabs, history };

  const active = history.find((item) => item === "chat" || tabs.includes(item)) ?? "chat";
  return {
    tabs,
    active,
    history: history.filter((item) => item !== active),
  };
}

/** 子会话标签的标题：Agent 名 + 任务摘要（如 `researcher · 通读 internal/agent`）。
 *
 *  数据取自**主时间线里那张 dispatch 块**（按 sessionId 命中 kind === "dispatch"）——
 *  卡上是什么身份，标签上就是什么身份，两处不会各说各话。回放路径的 agentName 是空串
 *  （父会话历史里只有 tool_call 的 arguments，展示名是 Agent 注册表的知识），所以留了
 *  nameOf 回落口：App 传注册表查名，与 DispatchCard 的回落同款。
 *
 *  **不许空白**：找不到对应 dispatch 块（会话被撤回、历史被压缩掉、坏数据）就回落成
 *  `子会话 <id 前 8 位>`——空白标签在标签栏上就是一个认不出的色块。 */
export function childTabTitle(
  blocks: ThreadBlock[],
  sessionId: string,
  nameOf?: (agentId: string) => string,
): string {
  const fallback = `子会话 ${sessionId.slice(0, 8) || "未知"}`;
  const card = blocks.find(
    (block): block is Extract<ThreadBlock, { kind: "dispatch" }> =>
      block.kind === "dispatch" && block.sessionId === sessionId,
  );
  if (!card) return fallback;
  const name = card.agentName || nameOf?.(card.agentId) || card.agentId || "";
  const task = clipOneLine(card.task, 24);
  if (name && task) return `${name} · ${task}`;
  return name || task || fallback;
}

/** 单行摘要：折叠空白 + 按**码点**截断（代理对从中间切开就是半个字符）。
 *
 *  为什么不复用 panels/outline.ts 的 outlineLabel：shared 层反向 import 组件层会把依赖
 *  方向倒过来；两处的宽度预算也不同（侧栏 40 / 标签栏 24）。 */
function clipOneLine(text: string, max: number): string {
  const one = text.replace(/\s+/g, " ").trim();
  const chars = [...one];
  return chars.length <= max ? one : chars.slice(0, max).join("") + "…";
}

function focusView(state: WorkspaceTabsState, active: WorkspaceView, tabs: WorkspaceTab[]): WorkspaceTabsState {
  if (state.active === active) return { ...state, tabs };
  return {
    tabs,
    active,
    history: [state.active, ...state.history.filter((item) => item !== state.active && item !== active)],
  };
}

/** 浏览器历史（popstate）驱动的工作区聚焦：把一个历史条目恢复成当前页。
 *
 *  与鼠标侧键监听的历史方案的区别（2026-10-07 二轮）：硬件侧键在 Chromium 里是
 *  **浏览器进程**处理的——不派发给页面，直接导航 WebContents 自己的历史栈。所以
 *  「跟网页一样」的唯一实现 = 每次切页 pushState、popstate 时用本函数把条目落回
 *  工作区。**已关闭的子会话标签在这里要重开**（浏览器后退到一页就是把它恢复出来，
 *  不是跳过——页面内容由 ChildSessionPage 重新装载）。 */
export function focusHistoryView(state: WorkspaceTabsState, target: WorkspaceView): WorkspaceTabsState {
  if (target === "chat") return focusChatTab(state);
  if (isWorkspacePage(target)) return focusWorkspacePage(state, target);
  return focusChildTab(state, childTabSession(target) ?? "");
}
