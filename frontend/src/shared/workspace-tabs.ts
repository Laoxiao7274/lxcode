// 工作区标签状态模型：固定页签（Agent/拓展/Git）+ **子会话临时视图**。
//
// 子会话（子 Agent = 独立会话，AGENTS.md §2.3）**不再是标签页**（2026-12 用户拍板）：
// 点主时间线的派发卡打开的是**临时视图**——只切 active、**不进 tabs**，标签栏因此永远
// 不出现子会话；返回（focusChatTab / popstate）后视图消失，重新点同一张卡回到同一个
// 视图键（child:<sessionId>），子会话在 store 里的 state 与实时流缓存不受影响。
//
// 视图键仍是**字符串键** `child:<sessionId>`：本文件与调用方（App / TabBar）大量按值
// 比较（includes / filter / ===），换成对象会让每一处比较都退化成按引用比，波及面远超
// 这一步的范围；字符串键是天然的判等凭据。
import type { ThreadBlock } from "./blocks";

export type WorkspacePage = "agents" | "catalog" | "git" | "remote";
/** 子会话视图键（模板字面量类型：手写拼接的键在编译期就能被发现）。 */
export type ChildTabKey = `child:${string}`;
/** 可关闭的工作区标签：固定页签（子会话临时视图不进 tabs，见 focusChildView）。 */
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

/** 子会话视图的键。**唯一**的拼键处——调用方手写 `"child:" + id` 早晚会拼错。 */
export function childTabKey(sessionId: string): ChildTabKey {
  return `child:${sessionId}`;
}

/** 键 → 子会话 id（非子会话键返回 null）。
 *
 *  固定页签（agents/catalog/git）与 chat 都不带这个前缀，天然不会被误认成子会话键。
 *  `child:` 后面为空 = 坏键（会渲染成一个没有信息的空视图），返回 null 而不是空串。 */
export function childTabSession(key: string): string | null {
  if (!key.startsWith("child:")) return null;
  const sessionId = key.slice("child:".length);
  return sessionId === "" ? null : sessionId;
}

/** 是不是固定页签（App 按它分流：固定页签走工作区标签逻辑，子会话走 focusChildView）。 */
export function isWorkspacePage(tab: WorkspaceTab): tab is WorkspacePage {
  return tab === "agents" || tab === "catalog" || tab === "git" || tab === "remote";
}

/** 打开/聚焦一个工作区标签（固定页签走这一条路径）。
 *
 *  **去重就在这里**：已在 tabs 里的键不重复 push——重复打开同一个页面必须回到同一个
 *  标签，而不是并排长出两个一模一样的标签（关掉一个还剩一个，用户会以为关不掉）。
 *  tests/workspace-tabs.test.mjs 钉住这条：把这里的 includes 判定去掉，那条测试立刻变红。 */
export function focusWorkspaceTab(state: WorkspaceTabsState, tab: WorkspaceTab): WorkspaceTabsState {
  const tabs = state.tabs.includes(tab) ? state.tabs : [...state.tabs, tab];
  return focusView(state, tab, tabs);
}

export function focusWorkspacePage(state: WorkspaceTabsState, page: WorkspacePage): WorkspaceTabsState {
  return focusWorkspaceTab(state, page);
}

/** 打开/聚焦子会话**临时视图**（DispatchCard 整行点击的唯一入口）。
 *
 *  临时视图语义（2026-12 用户拍板：标签栏不再出现子会话）：**只切 active，不进
 *  tabs**——标签栏长度不变；返回（focusChatTab / popstate）后视图消失。重复打开
 *  同一个子会话回到同一个视图（active 已是该键 = 焦点保持），连点/多张卡来回点都
 *  不会长出重复视图。子会话在 store 里的 state 与实时流缓存不受影响（重新打开时
 *  ChildSessionPage 重新装载历史，store 双投归约照旧续上）。
 *
 *  空 id 不是合法子会话：no-op（开一个空键视图只会得到「子会话 未知」的空页）。 */
export function focusChildView(state: WorkspaceTabsState, sessionId: string): WorkspaceTabsState {
  if (sessionId === "") return state;
  return focusView(state, childTabKey(sessionId), state.tabs);
}

export function focusChatTab(state: WorkspaceTabsState): WorkspaceTabsState {
  return focusView(state, "chat", state.tabs);
}

/** 关闭一个工作区标签（固定页签语义，**逐字段保持原行为**）：
 *  非当前标签 → 只从 tabs/history 摘掉；当前标签 → 回退到最近仍打开的标签（没有就回 chat）。
 *  未知键是 no-op（返回原对象）。子会话临时视图不进 tabs，不经过这里（返回走 focusChatTab）。 */
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

// ---- 子→父会话映射（子会话视图的标题反查与双投路由，2026-10-09 体验修复批次 5）----

/** 子会话 id → 父会话 id 的持久映射。
 *
 *  为什么独立于 UIState：归约流是**逐事件**的，而映射是**跨会话**的索引——
 *  塞进 UIState 要么挂在子会话 state 上（子会话 state 可能根本不存在，双投
 *  「不建」规则）、要么每次事件全量扫（正是被消掉的那个 O(所有会话×所有块)）。
 *  独立一份键值表，App 级持久（切会话不丢），只增不删（子会话 id 不复用）。 */
export type ChildParents = Record<string, string>;

/** 同一批 dispatch 事实产出的**两张**映射（键不同，不能混用）：
 *  - childParents：子会话 id → 父会话 id（标签过滤、标题反查）；
 *  - dispatchChild：dispatch id → 子会话 id（子事件双投的归属路由——子事件
 *    带的是 dispatch_id，路由前要先把 dispatch_id 换成子会话 id）。 */
export interface ChildLinks {
  childParents: ChildParents;
  dispatchChild: Record<string, string>;
}

export function emptyChildLinks(): ChildLinks {
  return { childParents: {}, dispatchChild: {} };
}

/** 记一条 dispatch 事实（dispatchStart 或回放块）：两张映射一起更新。
 *  幂等：同样的内容重复记录返回同一对象（不触发无谓的重渲染）。 */
export function recordChildLink(
  links: ChildLinks,
  fact: { childId: string; parentId: string; dispatchId?: string },
): ChildLinks {
  if (!fact.childId || !fact.parentId) return links;
  const childParents = recordChildParent(links.childParents, fact.childId, fact.parentId);
  const dispatchId = fact.dispatchId ?? "";
  const dispatchChild = !dispatchId || links.dispatchChild[dispatchId] === fact.childId
    ? links.dispatchChild
    : { ...links.dispatchChild, [dispatchId]: fact.childId };
  if (childParents === links.childParents && dispatchChild === links.dispatchChild) return links;
  return { childParents, dispatchChild };
}

/** 记一条子→父映射（幂等：同一条重复记录返回同一内容）。 */
export function recordChildParent(parents: ChildParents, childId: string, parentId: string): ChildParents {
  if (!childId || !parentId) return parents;
  if (parents[childId] === parentId) return parents;
  return { ...parents, [childId]: parentId };
}

/** 从一个会话的 blocks 里扫出它派发的子会话（历史回放路径的映射重建）。
 *
 *  dispatch 块同时带两把键：`id`（工具调用 id = dispatch id）与 `sessionId`
 *  （子会话 id——实时路径来自 dispatchStart 的 childSessionId，回放路径来自
 *  工具结果提示行，两处都已核实），持有这批 blocks 的会话就是父。
 *  **只在 historyLoaded 时跑一次**，不是逐事件扫描。 */
export function childLinksFromBlocks(parentId: string, blocks: ThreadBlock[]): ChildLinks {
  let links: ChildLinks | null = null;
  for (const block of blocks) {
    if (block.kind !== "dispatch" || !block.sessionId) continue;
    links = recordChildLink(links ?? emptyChildLinks(), {
      childId: block.sessionId, parentId, dispatchId: block.id,
    });
  }
  return links ?? emptyChildLinks();
}

/** 子会话视图的标题：Agent 名 + 任务摘要（如 `researcher · 通读 internal/agent`）。
 *
 *  数据取自**主时间线里那张 dispatch 块**（按 sessionId 命中 kind === "dispatch"）——
 *  卡上是什么身份，临时视图页头/顶栏就是什么身份，两处不会各说各话。回放路径的
 *  agentName 是空串（父会话历史里只有 tool_call 的 arguments，展示名是 Agent 注册表的
 *  知识），所以留了 nameOf 回落口：App 传注册表查名，与 DispatchCard 的回落同款。
 *
 *  **不许空白**：找不到对应 dispatch 块（会话被撤回、历史被压缩掉、坏数据）就回落成
 *  `子会话 <id 前 8 位>`——空标题的视图页头就是一个认不出的空位。 */
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
 *  工作区。**后退到子会话视图条目就是把它重新打开**（浏览器后退到一页就是把它恢复
 *  出来，不是跳过——临时视图不进 tabs 也照样能恢复，页面内容由 ChildSessionPage
 *  重新装载）。 */
export function focusHistoryView(state: WorkspaceTabsState, target: WorkspaceView): WorkspaceTabsState {
  if (target === "chat") return focusChatTab(state);
  if (isWorkspacePage(target)) return focusWorkspacePage(state, target);
  return focusChildView(state, childTabSession(target) ?? "");
}
