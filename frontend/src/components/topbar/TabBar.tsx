// 顶部标签栏：左侧工作区页面（聊天固定、Agent/拓展/Git 可关闭），右侧会话标签。
// 会话标签顺序语义对齐浏览器：**打开顺序固定**——点标签只切焦点，绝不重排；
// 新会话/从侧栏点进来的会话追加到右侧，容量满丢最老的。
// 每个会话标签是独立并发 Session；切焦点不取消后台轮次。关闭会话标签只隐藏标签，
// 会话本体仍留在侧栏并在刷新时恢复。
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import type { AgentSource } from "../../shared/types";
import type { WorkspacePage, WorkspaceView } from "../../shared/workspace-tabs";

/** 标签条容量：最多同时显示的标签数（超出丢最老的——浏览器同款）。 */
const TAB_LIMIT = 8;
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
  onFocusWorkspacePage,
  onCloseWorkspacePage,
}: {
  source: AgentSource;
  currentId: string;
  busyBySession: Record<string, boolean>;
  onNewChat: () => void;
  onFocusSession: (id: string) => void;
  workspaceTabs: WorkspacePage[];
  activeWorkspaceView: WorkspaceView;
  onFocusChat: () => void;
  onFocusWorkspacePage: (page: WorkspacePage) => void;
  onCloseWorkspacePage: (page: WorkspacePage) => WorkspaceView;
}) {
  const sessions = source.sessions();
  // 打开顺序（标签 id 列表）；closed = 用户关掉的（纯 UI 态，刷新恢复）
  const [order, setOrder] = useState<string[]>([]);
  const [closed, setClosed] = useState<Set<string>>(new Set());
  const seededRef = useRef(false);

  // 首次拿到会话列表铺开标签：后端序是最近在前 → 反转成「新的在右」
  //（浏览器里后开的标签在右边）。只铺一次——之后顺序由用户操作决定。
  useEffect(() => {
    if (seededRef.current || sessions.length === 0) return;
    seededRef.current = true;
    setOrder(sessions.filter((s) => !s.archived).slice(0, TAB_LIMIT).map((s) => s.id).reverse());
  }, [sessions]);

  // 切到不在标签条里的会话（侧栏点进来 / 新建）→ 追加到右侧。
  // 已在列表里则不动（点标签不重排——浏览器语义）。
  useEffect(() => {
    if (!currentId) return;
    setOrder((prev) => (prev.includes(currentId) ? prev : [...prev.slice(-(TAB_LIMIT - 1)), currentId]));
  }, [currentId]);

  // 渲染集合：按打开顺序，剔除已归档/已关闭的（当前会话恒显示）
  const tabs = order
    .map((id) => ({ id, meta: sessions.find((s) => s.id === id) }))
    .filter(({ id, meta }) => id === currentId || (meta && !meta.archived && !closed.has(id)));

  // 关闭 = 从标签条隐藏（会话保留；关掉当前会话 → 切到新对话）
  const close = (id: string) => {
    if (id === currentId) onNewChat();
    setClosed((prev) => new Set(prev).add(id));
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
        {workspaceTabs.map((page) => (
          <WorkspacePageTab
            key={page}
            page={page}
            active={activeWorkspaceView === page}
            onFocus={onFocusWorkspacePage}
            onClose={onCloseWorkspacePage}
          />
        ))}
      </nav>
      <span className="tabbar-divider" aria-hidden="true" />
      <div className="tab-strip session-tab-strip" role="group" aria-label="会话标签">
        {tabs.map(({ id, meta }) => {
          const on = id === currentId;
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

function WorkspacePageTab({
  page,
  active,
  onFocus,
  onClose,
}: {
  page: WorkspacePage;
  active: boolean;
  onFocus: (page: WorkspacePage) => void;
  onClose: (page: WorkspacePage) => WorkspaceView;
}) {
  const tabRef = useRef<HTMLDivElement>(null);
  const closeTweenRef = useRef<gsap.core.Tween | null>(null);
  const closingRef = useRef(false);
  const [closing, setClosing] = useState(false);
  const title = WORKSPACE_LABELS[page];

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
    const fallback = onClose(page);
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
    <div ref={tabRef} className={"tab workspace-tab" + (active ? " on" : "")} data-workspace-tab={page}>
      <button
        type="button"
        className="tab-main workspace-tab-main"
        disabled={closing}
        aria-current={active ? "page" : undefined}
        onClick={() => onFocus(page)}
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
