// 顶部标签栏：左侧工作区标签（聊天固定，Agent/拓展/Git 与**子会话**可关闭），右侧会话标签。
// 子会话标签是**带参数的标签**：键形如 child:<sessionId>（见 shared/workspace-tabs.ts），
// 与固定页签共用同一个渲染件、同一套「关掉当前页回退到最近打开的页」语义——只有标题来源
// 不同（App 用纯函数 childTabTitle 从主时间线那张 dispatch 块算出来，找不到就回落）。
// 会话标签顺序语义对齐浏览器：**打开顺序固定**——点标签只切焦点，绝不重排；
// 新会话/从侧栏点进来的会话追加到右侧，容量满丢最老的。
// 每个会话标签是独立并发 Session；切焦点不取消后台轮次。关闭会话标签只隐藏标签，
// 会话本体仍留在侧栏并在刷新时恢复。
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import {
  appendSessionTab,
  pruneSessionTabs,
  seedSessionTabs,
  sessionTabFallback,
  visibleSessionTabs,
} from "../../shared/session-tabs";
import type { AgentSource } from "../../shared/types";
import { childTabSession, type WorkspacePage, type WorkspaceTab, type WorkspaceView } from "../../shared/workspace-tabs";

const WORKSPACE_LABELS: Record<WorkspacePage, string> = {
  agents: "Agent",
  catalog: "拓展",
  git: "Git",
};

export function TabBar({
  source,
  currentId,
  busyBySession,
  onNewChat,
  onFocusSession,
  workspaceTabs,
  activeWorkspaceView,
  onFocusChat,
  onFocusWorkspaceTab,
  onCloseWorkspaceTab,
  childTabTitle,
}: {
  source: AgentSource;
  currentId: string;
  busyBySession: Record<string, boolean>;
  onNewChat: () => void;
  onFocusSession: (id: string) => void;
  /** 可关闭的工作区标签：固定页签 + 子会话标签（键形如 child:<sessionId>）。 */
  workspaceTabs: WorkspaceTab[];
  activeWorkspaceView: WorkspaceView;
  onFocusChat: () => void;
  onFocusWorkspaceTab: (tab: WorkspaceTab) => void;
  onCloseWorkspaceTab: (tab: WorkspaceTab) => WorkspaceView;
  /** 子会话标签的标题（App 用纯函数 childTabTitle 算：Agent 名 + 任务摘要）。
   *  **必填**——固定页签走下面的 WORKSPACE_LABELS，子会话标签不在这里再写一份回落，
   *  两份回落早晚分叉（标签栏显示「子会话 x」而顶栏显示别的名字）。 */
  childTabTitle: (sessionId: string) => string;
}) {
  const sessions = source.sessions();
  // 打开顺序（标签 id 列表）；closed = 用户关掉的（纯 UI 态，刷新恢复）
  const [order, setOrder] = useState<string[]>([]);
  const [closed, setClosed] = useState<Set<string>>(new Set());
  // 关掉当前标签后"焦点要去的那个"：resumeSession 是一个 WS 往返（项目会话还要先校验
  // 工作树），这期间 store 里的 currentId 还没动。标签条是用户刚操作的地方，不能在这段
  // 时间里一个高亮都没有（实测：标签 12ms 就消失了，但高亮要等 229ms 才落下来，
  // 中间整条标签栏没有"你在哪"的指示）。所以这里先记下目的地，currentId 追上后清掉。
  const [pendingFocus, setPendingFocus] = useState("");
  const seededRef = useRef(false);

  // 首次拿到会话列表铺开标签：后端序是最近在前 → 反转成「新的在右」
  //（浏览器里后开的标签在右边）。只铺一次——之后顺序由用户操作决定。
  useEffect(() => {
    if (seededRef.current || sessions.length === 0) return;
    seededRef.current = true;
    setOrder(seedSessionTabs(sessions));
  }, [sessions]);

  // 切到不在标签条里的会话（侧栏点进来 / 新建）→ 追加到右侧。
  // 已在列表里则不动（点标签不重排——浏览器语义）。
  useEffect(() => {
    if (!currentId) return;
    setOrder((prev) => appendSessionTab(prev, currentId));
  }, [currentId]);

  // 焦点回到一个被关过的会话（从侧栏点进来）→ 该标签重新出现。
  // 关闭只是标签条上的 UI 状态，不是"这个会话不许再开"。
  useEffect(() => {
    if (!currentId) return;
    setClosed((prev) => {
      if (!prev.has(currentId)) return prev;
      const next = new Set(prev);
      next.delete(currentId);
      return next;
    });
  }, [currentId]);

  // 死标签不占容量：归档/关闭后从 order 剔掉，否则它们会顶掉下次追加时的活标签。
  // prune 在无变化时返回同一个数组引用，所以不会引起重渲染循环。
  useEffect(() => {
    setOrder((prev) => pruneSessionTabs(prev, sessions, closed, currentId));
  }, [sessions, closed, currentId]);

  // currentId 追上目的地 → 交还高亮（目的地在焦点移过去之前可能已被用户关掉，也要清）
  useEffect(() => {
    if (!pendingFocus) return;
    if (pendingFocus === currentId || closed.has(pendingFocus)) setPendingFocus("");
  }, [pendingFocus, currentId, closed]);

  // 渲染集合：按打开顺序，剔除已归档/已关闭的（当前会话恒显示）
  const tabIds = visibleSessionTabs(order, sessions, closed, currentId);
  const tabs = tabIds.map((id) => ({ id, meta: sessions.find((s) => s.id === id) }));
  // 高亮：目的地优先——关标签后立刻指出"接下来是哪个"，不必等历史读回来
  const activeId = pendingFocus && tabIds.includes(pendingFocus) ? pendingFocus : currentId;

  // 关闭 = 从标签条隐藏（会话本体留在侧栏，刷新仍在）。
  // 关掉**当前**标签时把焦点让给最近打开的另一个标签——**绝不新建会话**：
  // 原来这里调 onNewChat()，那会真的在后端建一个会话，于是"关一个冒一个"，
  // 而新会话又被追加进 order 顶掉最老的标签，看起来就是"关掉新会话，
  // 别的标签也一起没了"。只剩一个标签时没有可让的对象，就保持现状（关不掉）。
  const close = (id: string) => {
    setClosed((prev) => new Set(prev).add(id));
    if (id !== currentId) return;
    const fallback = sessionTabFallback(tabIds, id);
    if (!fallback) return;
    setPendingFocus(fallback); // 先把高亮挪过去，内容随后到（历史读回来才切焦点）
    onFocusSession(fallback);
  };

  // 工作区的「聊天」固定保留；会话区即使为空也保留「+」按钮，保证标签栏高度稳定。

  return (
    <div className="tabbar" data-tabs={String(tabs.length)} data-workspace-tabs={String(workspaceTabs.length + 1)}>
      <nav className="workspace-tab-strip" aria-label="工作区标签">
        <div className={"tab workspace-tab" + (activeWorkspaceView === "chat" ? " on" : "")} data-workspace-tab="chat">
          <button
            type="button"
            className="tab-main workspace-tab-main"
            aria-current={activeWorkspaceView === "chat" ? "page" : undefined}
            onClick={onFocusChat}
          >
            <span className="tab-title">聊天</span>
          </button>
        </div>
        {workspaceTabs.map((tab) => {
          // 标题在这里分流：固定页签查标签表，子会话标签问 App（childTabTitle 纯函数）。
          // 渲染件是同一个——关闭动画与「关掉后焦点去哪」只有一份实现。
          const sessionId = childTabSession(tab);
          // sessionId 为 null 只可能是固定页签（tabs 里只有这两类键，见 workspace-tabs.ts）
          const title = sessionId !== null ? childTabTitle(sessionId) : WORKSPACE_LABELS[tab as WorkspacePage];
          return (
            <WorkspaceTabView
              key={tab}
              tab={tab}
              title={title}
              active={activeWorkspaceView === tab}
              onFocus={onFocusWorkspaceTab}
              onClose={onCloseWorkspaceTab}
            />
          );
        })}
      </nav>
      <span className="tabbar-divider" aria-hidden="true" />
      <div className="tab-strip session-tab-strip" role="group" aria-label="会话标签">
        {tabs.map(({ id, meta }) => {
          const on = id === activeId;
          const busy = Boolean(busyBySession[id]);
          const title = meta?.title || "新对话";
          return (
            <div
              key={id}
              className={"tab" + (on ? " on" : "")}
              data-cg="tab"
              data-busy={busy ? "true" : undefined}
              title={title}
            >
              <button
                type="button"
                className="tab-main"
                aria-current={on ? "page" : undefined}
                aria-label={`${title}${busy ? "（生成中）" : ""}`}
                onClick={() => {
                  if (!on) {
                    onFocusSession(id);
                  }
                }}
              >
                {busy && <span className="mset-spinner tab-spinner" aria-hidden />}
                <span className="tab-title">{title}</span>
              </button>
              {/* 最后一个标签不显示关闭按钮：关掉它没有可回退的标签，而这个应用
                  的对话区恒需要一条当前会话（不能靠新建来"补位"——那正是原来的缺陷）。
                  会话本体仍在侧栏，要清理会话请用侧栏的归档。 */}
              {tabs.length > 1 && (
                <button
                  type="button"
                  className="tab-close"
                  onClick={() => close(id)}
                  aria-label={`关闭标签「${title}」`}
                  title="关闭标签（会话保留在侧栏）"
                >
                  <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                    <path d="M18 6 6 18M6 6l12 12" />
                  </svg>
                </button>
              )}
            </div>
          );
        })}
        <button type="button" className="tab-new" onClick={onNewChat} aria-label="新对话" title="新对话">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      </div>
    </div>
  );
}

/** 一个可关闭的工作区标签（固定页签与子会话标签共用——关闭动画、焦点回落只有一份）。 */
function WorkspaceTabView({
  tab,
  title,
  active,
  onFocus,
  onClose,
}: {
  tab: WorkspaceTab;
  /** 显示标题（固定页签来自 WORKSPACE_LABELS，子会话标签来自 childTabTitle）。 */
  title: string;
  active: boolean;
  onFocus: (tab: WorkspaceTab) => void;
  onClose: (tab: WorkspaceTab) => WorkspaceView;
}) {
  const tabRef = useRef<HTMLDivElement>(null);
  const closeTweenRef = useRef<gsap.core.Tween | null>(null);
  const closingRef = useRef(false);
  const [closing, setClosing] = useState(false);

  useLayoutEffect(() => {
    const element = tabRef.current;
    if (!element || !motionAllowed()) return;
    const context = gsap.context(() => {
      gsap.fromTo(element, { opacity: 0, y: 5, scale: 0.97 }, {
        opacity: 1,
        y: 0,
        scale: 1,
        duration: 0.2,
        ease: "power2.out",
        clearProps: "transform,opacity",
      });
    }, element);
    return () => { context.revert(); };
  }, []);

  useEffect(() => {
    return () => { closeTweenRef.current?.kill(); };
  }, []);

  const finishClose = () => {
    const fallback = onClose(tab);
    window.setTimeout(() => {
      document.querySelector<HTMLButtonElement>(`[data-workspace-tab="${fallback}"] .workspace-tab-main`)?.focus();
    }, 0);
  };

  const close = () => {
    if (closingRef.current) return;
    closingRef.current = true;
    setClosing(true);
    const element = tabRef.current;
    if (!element || !motionAllowed()) {
      finishClose();
      return;
    }
    closeTweenRef.current = gsap.to(element, {
      opacity: 0,
      y: -4,
      scale: 0.96,
      duration: 0.14,
      ease: "power1.in",
      onComplete: finishClose,
    });
  };

  return (
    <div ref={tabRef} className={"tab workspace-tab" + (active ? " on" : "")} data-workspace-tab={tab}>
      <button
        type="button"
        className="tab-main workspace-tab-main"
        disabled={closing}
        aria-current={active ? "page" : undefined}
        onClick={() => onFocus(tab)}
      >
        <span className="tab-title">{title}</span>
      </button>
      <button
        type="button"
        className="tab-close"
        disabled={closing}
        onClick={close}
        aria-label={`关闭工作区标签「${title}」`}
        title={`关闭${title}工作区标签`}
      >
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
          <path d="M18 6 6 18M6 6l12 12" />
        </svg>
      </button>
    </div>
  );
}
