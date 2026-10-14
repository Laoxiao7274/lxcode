// 工作区导航域（审查 M5 前端重构：从 App.tsx 纯移动，语义零改动）。
//
// 页签状态 + **真实浏览器历史**（History API；2026-10-07 二轮重写）+ 页签/视图
// 回调 + 两块伴随的 UI 态（settingsOpen / filter）。
//
// 一轮的教训：硬件侧键（和 Alt+←/→）在 Chromium 里由**浏览器进程**处理——不派发
// 给页面（页面收不到 button 3/4 的 mousedown），直接导航 WebContents 自己的历史栈
//（electron#22202；CDP 合成事件走另一条管线，所以页面监听方案在真机必然失效）。
// pushState 推的条目就在那个栈里（electron#22269 佐证）。所以「跟网页一样」的
// 唯一实现：每次切页 pushState 一个条目，popstate 时把视图读回来——浏览器历史
// 就是我们的栈，侧键/Alt+方向/浏览器后退全部原生生效。
import { useCallback, useEffect, useRef, useState } from "react";
import {
  childTabSession,
  closeWorkspacePage,
  createWorkspaceTabs,
  focusChatTab,
  focusChildView,
  focusHistoryView,
  focusWorkspacePage,
  focusWorkspaceTab,
  isWorkspacePage,
  type WorkspacePage,
  type WorkspaceTab,
  type WorkspaceTabsState,
  type WorkspaceView,
} from "../shared/workspace-tabs";
import { LOOSE } from "../components/sidebar";
import type { AgentSource } from "../shared/types";

export function useWorkspaceNav({ source }: { source: AgentSource }) {
  /** 工作区页签与会话标签分开；未关闭的工作区页面保持挂载以保留表单/视图状态。 */
  const [workspaceState, setWorkspaceState] = useState(createWorkspaceTabs);
  const workspaceStateRef = useRef(workspaceState);
  workspaceStateRef.current = workspaceState;
  const view = workspaceState.active;

  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsOpenRef = useRef(settingsOpen);
  settingsOpenRef.current = settingsOpen;
  /** 对话范围（项目 id / ""=未分组）——App 持有：侧栏过滤、「新对话」归属、
   *  空态项目标签三处共用。用户拍板：**恒有范围**（启动即「未分组」，点项目行
   *  切换且不可取消——没有「全部」视图）。 */
  const [filter, setFilter] = useState<string>(LOOSE);

  /** 最后写进浏览器历史的那个视图：popstate 是异步事实，用它区分「UI 导航要 push」
   *  与「历史条目已是它」（同页重复聚焦不该堆出重复条目）。 */
  const pushedViewRef = useRef<WorkspaceView>("chat");

  /** 工作区导航的唯一收口：换状态 + 把落点写进真实浏览器历史（侧键可退回的依据）。 */
  const pushWorkspace = useCallback((next: WorkspaceTabsState) => {
    workspaceStateRef.current = next;
    setWorkspaceState(next);
    if (next.active !== pushedViewRef.current) {
      pushedViewRef.current = next.active;
      try {
        window.history.pushState({ lxView: next.active }, "");
      } catch {
        // app:// 自定义协议或极端环境下 pushState 可能被拒——退化为无历史（侧键无效），
        // 应用功能不受影响。不能让导航历史炸掉一次 UI 导航。
      }
    }
  }, []);

  // 初始条目打上标记；popstate = 用户按了侧键/Alt+方向/浏览器后退——把条目落回工作区
  //（state 为 null = 退到了首条 → 回聊天）。后退到子会话临时视图的条目是**有意**的：
  // 浏览器后退到一页就是把它恢复出来（focusHistoryView → focusChildView）。
  useEffect(() => {
    try {
      window.history.replaceState({ lxView: pushedViewRef.current }, "");
    } catch { /* 同上：没有历史就没有侧键，应用照常 */ }
    const onPop = (e: PopStateEvent) => {
      const target = (e.state?.lxView as WorkspaceView | undefined) ?? "chat";
      const cur = workspaceStateRef.current;
      if (cur.active === target) return;
      const next = focusHistoryView(cur, target);
      workspaceStateRef.current = next;
      setWorkspaceState(next);
      pushedViewRef.current = target;
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const openWorkspace = useCallback((page: WorkspacePage) => {
    pushWorkspace(focusWorkspacePage(workspaceStateRef.current, page));
  }, [pushWorkspace]);
  // 关闭的工作区标签：固定页签（agents/catalog/git）同一套语义（子会话临时视图不进
  // 标签栏，不经过这里——返回走 backToChat）
  const closeWorkspace = useCallback((tab: WorkspaceTab): WorkspaceView => {
    const next = closeWorkspacePage(workspaceStateRef.current, tab);
    pushWorkspace(next);
    return next.active;
  }, [pushWorkspace]);
  /** 标签栏点击一个工作区标签（固定页签；子会话不进标签栏——临时视图由
   *  openChildView 打开，不经过这里）。 */
  const openWorkspaceTab = useCallback((tab: WorkspaceTab) => {
    if (isWorkspacePage(tab)) {
      openWorkspace(tab);
      return;
    }
    pushWorkspace(focusWorkspaceTab(workspaceStateRef.current, tab));
  }, [openWorkspace, pushWorkspace]);
  /** 卡上的「打开子会话」→ 打开**临时视图**（focusChildView 只切 active、不进 tabs：
   *  标签栏不再出现子会话，重复打开同一个子会话回到同一个视图）。 */
  const openChildView = useCallback((sessionId: string) => {
    pushWorkspace(focusChildView(workspaceStateRef.current, sessionId));
  }, [pushWorkspace]);
  const openAgents = useCallback(() => openWorkspace("agents"), [openWorkspace]);
  const openCatalog = useCallback(() => openWorkspace("catalog"), [openWorkspace]);
  const openRemote = useCallback(() => openWorkspace("remote"), [openWorkspace]);
  const backToChat = useCallback(() => {
    pushWorkspace(focusChatTab(workspaceStateRef.current));
  }, [pushWorkspace]);
  const focusSession = useCallback((id: string) => {
    setFilter(source.sessions().find((session) => session.id === id)?.workspace ?? LOOSE);
    void source.resumeSession(id);
    backToChat();
  }, [source, backToChat]);
  // 侧栏「设置」入口（内联箭头会击穿 Sidebar memo——useCallback 固定身份）。
  const openSettings = useCallback(() => setSettingsOpen(true), []);

  return {
    view, workspaceState, settingsOpen, setSettingsOpen, filter, setFilter,
    openWorkspace, closeWorkspace, openWorkspaceTab, openChildView,
    openAgents, openCatalog, openRemote, backToChat, focusSession, openSettings,
  };
}

// childTabSession 从 workspace-tabs 再导出（App 判子会话临时视图用）——
// 仅为保持 App.tsx 原有的单一 import 面；不新增语义。
export { childTabSession };
