// 顶部标签栏：左侧工作区标签（聊天固定 + Agent/拓展/Git 可关闭页签），右侧会话标签
// （主会话标签 + **子会话标签** + 新建按钮）。
// 子会话标签是**带参数的标签**：键形如 child:<sessionId>（见 shared/workspace-tabs.ts），
// 与其他标签共用同一个渲染件、同一套「关掉当前页回退到最近打开的页」语义——只有标题来源
// 不同（App 用纯函数 childTabTitle / childTabHint 从主时间线那张 dispatch 块算出来，
// 找不到就回落）。它跟主会话标签同一条 strip（子会话是**会话**不是工作区页面，
// 2026-10-09 用户拍板从左 strip 迁出）：子会话标签 active 时主会话标签退为普通态，
// 点主会话标签走 focusSession → backToChat() 回到 chat 视图。
// 会话标签顺序语义对齐浏览器：**打开顺序固定**——点标签只切焦点，绝不重排；
// 新会话/从侧栏点进来的会话追加到右侧，容量满丢最老的。
// 每个会话标签是独立并发 Session；切焦点不取消后台轮次。关闭会话标签只隐藏标签，
// 会话本体仍留在侧栏并在刷新时恢复。
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import { overflowAttr, overflowEdges, shouldInterceptWheel } from "../../shared/scroll-metrics";
import type { OverflowAttr } from "../../shared/scroll-metrics";
import {
  appendSessionTab,
  pruneSessionTabs,
  seedSessionTabs,
  sessionTabFallback,
  visibleSessionTabs,
} from "../../shared/session-tabs";
import type { AgentSource } from "../../shared/types";
import {
  childTabSession,
  splitWorkspaceTabs,
  visibleChildTabs,
  type WorkspacePage,
  type WorkspaceTab,
  type WorkspaceView,
} from "../../shared/workspace-tabs";

const WORKSPACE_LABELS: Record<WorkspacePage, string> = {
  agents: "Agent",
  catalog: "拓展",
  git: "Git",
  remote: "远程访问",
};

/** 活动标签滚进可视区（两条 strip 共用一份实现）。
 *
 *  这是用户报的「标签页超出窗口被遮住」的**主因**修复：新开的会话标签（尤其子会话标签，
 *  它追加在最右）落在可视区外，而滚动条是隐藏的，用户看到的就是"被遮住一部分"。
 *
 *  inline: "nearest"（不是 center）：用户只是切标签时，center 会把整条 strip 拽来拽去，
 *  连本来看得见的标签也跟着跑。block: "nearest"：标签纵向本来就完整可见，
 *  不许把外层容器也拽一下（scrollIntoView 会连带滚动所有可滚祖先）。 */
function useActiveTabVisible(ref: RefObject<HTMLElement | null>, activeKey: string, tabCount: number) {
  useLayoutEffect(() => {
    // 活动标签就是带 .on 的那个（两条 strip 的渲染件都用这个类，不再另起一套标记）
    ref.current?.querySelector<HTMLElement>(".tab.on")?.scrollIntoView({ block: "nearest", inline: "nearest" });
    // tabCount 也要进依赖：标签是**渲染后**才挂上 DOM 的（首屏铺开标签时 key 并没有变），
    // 只盯 key 的话，首屏那一次滚进可视区会被整个跳过。
  }, [ref, activeKey, tabCount]);
}

/** 标签条横向溢出的两个附属行为（两条 strip 共用一份实现）。
 *
 *  ① 渐隐提示：方向由运行时 scrollLeft 决定，CSS 读不到（纯 CSS 只能"永远两侧都渐隐"，
 *     那在没溢出时是假提示）——所以在这里算成 data-overflow，样式只负责按方向画。
 *     不用常显滚动条：36px 的栏里塞滚动条更难看，但"还有更多"必须有提示。
 *  ② 滚轮横向滚：overflow-x: auto 在多数浏览器里不吃纵向滚轮，把 deltaY 转到 scrollLeft。
 *     只在真的溢出、且该方向还有余量时拦截——否则会吞掉页面自身的滚动。 */
function useStripOverflow<T extends HTMLElement>() {
  const ref = useRef<T | null>(null);
  const [attr, setAttr] = useState<OverflowAttr>("none");
  const syncRef = useRef<() => void>(() => {});
  // 每次渲染后重算：标签增删改的是 scrollWidth 而不是容器的盒子宽，
  // ResizeObserver 只盯容器本身，看不见"标签多了"。
  useLayoutEffect(() => { syncRef.current(); });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const sync = () => {
      const next = overflowAttr(overflowEdges(el));
      setAttr((prev) => (prev === next ? prev : next)); // 同值不 setState：否则每次渲染都多跑一轮
    };
    syncRef.current = sync;
    sync();
    const onScroll = () => sync();
    const onWheel = (event: WheelEvent) => {
      if (!shouldInterceptWheel(el, event.deltaY)) return; // 不溢出/该方向到头 → 让事件冒泡
      event.preventDefault();
      el.scrollLeft += event.deltaY;
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    // 要 preventDefault，监听器就不能是 passive
    el.addEventListener("wheel", onWheel, { passive: false });
    const ro = new ResizeObserver(sync);
    ro.observe(el);
    return () => {
      el.removeEventListener("scroll", onScroll);
      el.removeEventListener("wheel", onWheel);
      ro.disconnect();
      syncRef.current = () => {};
    };
  }, []);
  return [ref, attr] as const;
}

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
  /** 子会话标签的 hover 提示（`<项目名> · 主会话「<主会话标题>」 · <子会话标签名>`）。
   *  主会话与项目名只有 App 知道（dispatch 块在主时间线里、项目表在 source 里），
   *  TabBar 不再自己反查一份——两处各查一份早晚分叉。 */
  childTabHint,
  childParents,
  activeParent,
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
  /** 子会话标签的 hover 提示（App 用纯函数 childTabHint 算，见上）。 */
  childTabHint: (sessionId: string) => string;
  /** 子会话 id → 父会话 id（store 维护的持久映射；数据源 = dispatchStart 的
   *  owner_session_id + 历史回放扫描，见 workspace-tabs.ts）。 */
  childParents: Record<string, string>;
  /** 当前活跃的**主会话** id：chat 视图 = currentId；子会话标签活跃 = 那个子
   *  会话的父（App 计算，见 App.tsx 的 activeParent）。 */
  activeParent: string;
}) {
  const sessions = source.sessions();
  // 项目表：主会话标签的 hover 提示要带项目名（workspace 存的是项目 id）。
  const projects = source.projects();
  const projectNameOf = (workspace?: string) =>
    workspace ? projects.find((p) => p.id === workspace)?.name : undefined;
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
  // 标签条分流（2026-10-09 用户拍板）：固定页签留在左 strip，子会话标签挪到右边的
  // 会话标签条（主会话标签之后）——子会话是**会话**不是工作区页面，混在「聊天」旁边
  // 既突兀又让选中态看起来同时高亮两处。
  const { pages, children: allChildTabs } = splitWorkspaceTabs(workspaceTabs);
  // 子会话标签**按需显示**（体验修复批次 5）：只显示「父会话是当前活跃主会话」的
  // 那些——父在跑/刚跑完的子会话才跟当前视图相关，别的会话的子标签混进来只会
  // 把标签条淹掉。active 的子标签本身恒可见（历史导航到它时不能悬空）。
  const childTabs = visibleChildTabs(allChildTabs, activeWorkspaceView, childParents, activeParent);
  // 高亮：目的地优先——关标签后立刻指出"接下来是哪个"，不必等历史读回来。
  // 子会话标签 active 时主会话标签**退为普通态**：同屏两个高亮 = 用户报的
  // 「应该同时只能选中一个」。点主会话标签走 focusSession → backToChat()，链路不变。
  const childActive = childTabSession(activeWorkspaceView) !== null;
  const activeId = childActive
    ? ""
    : pendingFocus && tabIds.includes(pendingFocus)
      ? pendingFocus
      : currentId;

  // 两条 strip 各自的溢出渐隐方向与滚轮横向滚（共用一份 hook）
  const [workspaceStripRef, workspaceOverflow] = useStripOverflow<HTMLElement>();
  const [sessionStripRef, sessionOverflow] = useStripOverflow<HTMLDivElement>();
  // 活动标签滚进可视区：焦点变化（工作区视图 / 会话）时各来一次。
  // 工作区那条的计数含固定「聊天」页签（+1），否则首屏铺开时那次滚动会被跳过；
  // 会话那条的计数**含子会话标签**（它们现在就在这条 strip 里）。
  useActiveTabVisible(workspaceStripRef, activeWorkspaceView, pages.length + 1);
  useActiveTabVisible(sessionStripRef, childActive ? activeWorkspaceView : activeId, tabIds.length + childTabs.length);

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
    <div className="tabbar" data-tabs={String(tabs.length)} data-workspace-tabs={String(pages.length + 1)}>
      <nav className="workspace-tab-strip" aria-label="工作区标签" ref={workspaceStripRef} data-overflow={workspaceOverflow}>
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
        {pages.map((tab) => (
          <WorkspaceTabView
            key={tab}
            tab={tab}
            title={WORKSPACE_LABELS[tab]}
            active={activeWorkspaceView === tab}
            onFocus={onFocusWorkspaceTab}
            onClose={onCloseWorkspaceTab}
          />
        ))}
      </nav>
      <span className="tabbar-divider" aria-hidden="true" />
      <div className="tab-strip session-tab-strip" role="group" aria-label="会话标签" ref={sessionStripRef} data-overflow={sessionOverflow}>
        {tabs.map(({ id, meta }) => {
          const on = id === activeId;
          const busy = Boolean(busyBySession[id]);
          const title = meta?.title || "新对话";
          // hover 提示带项目名（workspace 存项目 id）：`<项目名> · <会话标题>`；
          // 未分组会话没有项目名，就只有标题（不许伪装成有归属）。
          const projectName = projectNameOf(meta?.workspace);
          const hint = projectName ? `${projectName} · ${title}` : title;
          return (
            <div
              key={id}
              className={"tab" + (on ? " on" : "")}
              data-cg="tab"
              data-busy={busy ? "true" : undefined}
              title={hint}
            >
              <button
                type="button"
                className="tab-main"
                aria-current={on ? "page" : undefined}
                aria-label={`${hint}${busy ? "（生成中）" : ""}`}
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
        {/* 子会话标签：跟主会话标签同一条 strip（它们本来就是会话）。渲染件仍是
            WorkspaceTabView——↳ 前缀、「子会话」胶囊、关闭动画与「关掉后焦点去哪」
            都只有一份实现；标题/hover 提示来源与 App 的一套纯函数（childTabTitle /
            childTabHint），TabBar 不各算一份。 */}
        {childTabs.map((tab) => {
          const sessionId = childTabSession(tab) ?? "";
          return (
            <WorkspaceTabView
              key={tab}
              tab={tab}
              title={childTabTitle(sessionId)}
              hint={childTabHint(sessionId)}
              active={activeWorkspaceView === tab}
              onFocus={onFocusWorkspaceTab}
              onClose={onCloseWorkspaceTab}
            />
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
  hint,
  active,
  onFocus,
  onClose,
}: {
  tab: WorkspaceTab;
  /** 显示标题（固定页签来自 WORKSPACE_LABELS，子会话标签来自 childTabTitle）。 */
  title: string;
  /** hover 提示（原生 title）。固定页签不给（页签名即全部信息）；子会话标签来自
   *  App 的 childTabHint（`<项目名> · 主会话「<主会话标题>」 · <子会话标签名>`）。 */
  hint?: string;
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

  // 子会话标签要在视觉上与固定页签（Agent/拓展/Git）分开——用户报「子会话和主会话表明的
  // 不明显」。三个通道一起用：① ↳ 前缀（形状，灰度/色盲下也分得开）② 左侧强调色条
  // （颜色）③ data-child（样式挂点）。只靠颜色不够（UI/UX 规范：不能只用颜色传达信息）。
  const childId = childTabSession(tab);
  return (
    <div ref={tabRef} className={"tab workspace-tab" + (active ? " on" : "")} data-workspace-tab={tab} data-child={childId !== null ? "true" : undefined} title={hint}>
      <button
        type="button"
        className="tab-main workspace-tab-main"
        disabled={closing}
        aria-current={active ? "page" : undefined}
        onClick={() => onFocus(tab)}
      >
        {/* 子会话标记：↳（形状）+ 「子会话」（文字）+ data-child（颜色）三通道一起用。
         *  只靠颜色不够——UI/UX 规范：不能只用颜色传达信息（灰度/色盲下要分得开）。
         *  固定页签（Agent/拓展/Git）是**页面**，子会话标签是**会话**：这两类混在一条
         *  strip 里长得一模一样，就是用户报的「表明的不明显」。 */}
        {childId !== null && <span className="tab-child-mark" aria-hidden>↳</span>}
        <span className="tab-title">{title}</span>
        {childId !== null && <span className="tab-child-kind">子会话</span>}
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
