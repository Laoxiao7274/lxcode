// 助手模式顶层容器（助手模式 P2）：助手域的独立工作区——会话列表 + 时间线。
//
// 与项目模式的分工（AppMode = "project" | "assistant"，shared/app-mode.ts）：
// 项目模式的 Sidebar/TabBar/主时间线由 App 渲染；切到助手模式时整块让位给
// 本组件——它自己持 workspaceState（与主域同构：会话列表视图 + 子会话临时
// 视图）、自己拉 assistant.list、自己的子会话域判定（App.tsx 的连带迁移三组
// 在这里安家）。两个模式各持状态，互不覆盖。
//
// 数据源：source.assistant（AssistantSource 域能力口）。缺省时显示说明——
// 不炸（与 AssistantPage 的旧约定一致）。
//
// 子会话视图（run_task 派发卡点进）：键 child:<sessionId> 的临时视图，判定、
// 装载（assistant.childHistory）、取消、裁决（assistant.confirm 按 dispatch_id
// 反查父——原 App.tsx 的 resolveAssistantChildOwner 组迁移至此）全部在助手域内。
//
// 拓展页内嵌视图（2026-12 用户拍板）：侧栏「拓展」在助手模式内开二级视图
// （secondaryView = "catalog"）挂 CatalogPage，返回按钮回对话视图。
// 助手配置独立成页（2026-12 用户拍板）：侧栏「助手配置」不再借道 CatalogPage
// 的 assistant 页签——secondaryView = "assistant-config" 直接渲染独立页面壳
// （返回头 + AssistantConfigTab 自包含组件）。两个二级视图互斥语义由单 state
// 的枚举取值天然保证（点会话行/新对话/派发卡清一切二级视图）。
//
// **保活常驻（2026-12 卸载竞态防御）**：本组件不再随模式切换卸载——App.tsx
// 的两个模式分支都常驻渲染，显隐由 CSS display:none 承担（workspace-view-panel
// 的冻结保活先例）。切走/切回是纯显示切换，内部订阅（refreshList、
// session.changed、use-done-notify 的 hasFocus 判定）与全部组件状态（含时间线
// 流式渲染的中间态）跨模式存活——「切模式后生成停掉」一类卸载竞态整类消灭。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AgentSource, SessionMeta } from "../../shared/types";
import type { UIState } from "../../shared/store";
import type { AppMode } from "../../shared/app-mode";
import { childTabSession, type WorkspaceView } from "../../shared/workspace-tabs";
import { readLastAssistantSession, writeLastAssistantSession } from "../../shared/app-mode";
import { takeAssistantChildPref } from "./assistant-child-pref";
import { AssistantChildPage } from "./AssistantChildPage";
import { AssistantPage } from "./AssistantPage";
import { CatalogPage } from "../catalog/CatalogPage";
import { AssistantConfigTab } from "../catalog/AssistantConfigTab";
import { setCatalogTabPref } from "../catalog/catalog-tab-pref";
import { AssistantSessionList } from "./AssistantSessionList";
import { Button } from "../form";
import { childTabTitle } from "../../shared/workspace-tabs";
import { timelineOf } from "../../shared/blocks";
/** 助手模式的工作区视图：会话列表（恒在）+ 当前会话时间线 + 子会话临时视图。 */
export type AssistantWorkspaceView = WorkspaceView;

export function AssistantWorkspace({
  source,
  assistantStates,
  resolveAssistant,
  childParents,
  reportError,
  onOpenSettings,
  mode,
}: {
  source: AgentSource;
  /** 助手域 states（store 双投——assistant.* 事件的归约目标）。 */
  assistantStates: Record<string, UIState>;
  /** 助手域的两处定格（store.resolveAssistant——确认卡裁决的本地定格）。 */
  resolveAssistant: (sessionId: string, id: string, outcome: "allow" | "deny", answer?: string) => void;
  /** 子会话 id → 父会话 id（dispatch 块反查——临时视图标题与父判定）。 */
  childParents: Record<string, string>;
  reportError: (message: string) => void;
  /** 侧栏底部设置行（主域 Sidebar 同款入口——SettingsPanel 由 App 持有）。 */
  onOpenSettings: () => void;
  /** 顶层模式（App 的 appMode 直传）：本组件保活常驻、不随模式卸载，侧键探针
   *  的注册/注销不能以挂载为准——以「助手模式可见」为准（mode === "assistant"）。 */
  mode: AppMode;
}) {
  const assistant = source.assistant;
  /** 当前助手会话 id（"" 起始 = 中性态「未选择」；进入助手模式恢复上次会话
   *  记忆——lastAssistantSession，app-mode.ts 的 localStorage 记忆层）。 */
  const [sessionId, setSessionId] = useState(() => readLastAssistantSession());
  /** 记账：本工作区见过的助手会话 id（子会话域判定的依据——与 App 旧
   *  assistantIdsRef 同语义，只是收进助手模式内部）。 */
  const knownIdsRef = useRef<Set<string>>(new Set());
  /** 会话列表（assistant.list；enter 时与 session.changed 刷新）。 */
  const [sessionList, setSessionList] = useState<SessionMeta[]>([]);
  /** 列表加载失败（可见、可重试——不静默降级成空列表）。 */
  const [listError, setListError] = useState("");
  /** 侧栏搜索框（按标题过滤助手会话——主域 Sidebar 的 search-box 同款交互）。 */
  const [query, setQuery] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);

  // 子会话临时视图（与主域 focusChildView 同语义：只切 active，不进列表）。
  // **外部注入（2026-12「来自助手」跳转）**：项目模式侧栏点了任务行 → App 切
  // 助手模式 → 本组件挂载，useState 初始化器 take 一次性偏好——直接落在该任务
  // 的子会话视图（历史由 AssistantChildPage → assistant.childHistory 装载）。
  // 消费即清：之后用户在助手模式里的常规导航不再受影响。
  const [childViewId, setChildViewId] = useState<string | null>(() => takeAssistantChildPref());
  // 保活渲染（2026-12 卸载竞态防御）下的运行期消费：本组件常驻不随模式卸载，
  // 「挂载时初始化消费」只发生在应用启动——运行期「来自助手」跳转（App 的
  // openAssistantChild 先 setAssistantChildPref 再切模式）引发 App 重渲染，
  // 本组件随之重渲染，渲染后检查并 take 待消费的偏好。无偏好时 take 返回
  // null、不 setState（无自激循环）。
  useEffect(() => {
    const pending = takeAssistantChildPref();
    if (pending !== null) {
      setChildViewId(pending);
      setSecondaryView(null);
    }
  });
  /** 助手模式内的二级视图（形态照抄 childViewId）："catalog" = 拓展页
   *  （CatalogPage），"assistant-config" = 助手配置独立页（AssistantConfigTab
   *  直接挂载，不经 CatalogPage）。null = 无二级视图。单 state 枚举：三个值
   *  天然互斥，互跳时直接覆盖，不存在两个布尔交叉的中间态。 */
  const [secondaryView, setSecondaryView] = useState<"catalog" | "assistant-config" | null>(null);

  const knownTick = useCallback((id: string) => {
    if (id) knownIdsRef.current.add(id);
  }, []);

  const refreshList = useCallback(async () => {
    if (!assistant) return;
    try {
      const list = await assistant.list();
      setSessionList(list);
      setListError("");
    } catch (e) {
      setListError(e instanceof Error ? e.message : String(e));
    }
  }, [assistant]);

  // 初始装载（挂载时拉一次列表）+ session.changed 订阅：助手会话的新建/首条
  // 消息落库/归档/压缩都会广播 session.changed（后端 emit.go 的两域共用广播，
  // 助手域换 assistant.* 前缀但全局事件原样）——列表是共享状态，别的端建的
  // 助手会话也要立刻看见。订阅经 source.subscribe（AgentSource 能力口，现成）。
  useEffect(() => {
    void refreshList();
    return source.subscribe((ev) => {
      if (ev.type === "sessionChanged") void refreshList();
    });
  }, [source, refreshList]);

  // 卸载取证（一次性，2026-12「切回项目模式生成停掉」排查）：组件挂载起一个
  // 一次性标志，cleanup 触发时打日志。保活改造后正常使用永不卸载——这条日志
  // 出现即意味着真的发生了卸载（React 严格模式的双挂载例外：dev 下挂载→卸载
  // →重挂载各打一条，属预期），可据此区分「组件被卸载」与「仅隐藏」。
  const everMountedRef = useRef(false);
  useEffect(() => {
    if (!everMountedRef.current) {
      everMountedRef.current = true;
      console.log("[asst-ws] mounted");
    }
    return () => {
      if (everMountedRef.current) {
        everMountedRef.current = false;
        console.warn("[asst-ws] unmounted @", new Date().toISOString());
      }
    };
  }, []);

  const handleSessionId = useCallback((id: string) => {
    knownTick(id);
    setSessionId(id);
    void refreshList();
  }, [knownTick, refreshList]);

  // 列表选中一个会话：切 sessionId（AssistantPage 按 key 重挂 → boot 装载历史），
  // 收掉子会话临时视图与拓展页内嵌视图；记忆更新（进入助手模式恢复的就是它）。
  const handleSelect = useCallback((id: string) => {
    knownTick(id);
    writeLastAssistantSession(id);
    setSessionId(id);
    setChildViewId(null);
    setSecondaryView(null);
  }, [knownTick]);

  // ---- 鼠标侧键（2026-12 修复）：助手域导航探针 ----
  // 助手域是纯 useState 导航、从不写浏览器历史——壳层的侧键注入默认走
  // history.back()，消费的是项目模式遗留条目、改到隐藏分支的 state。壳层已改为
  // 探针优先（main.ts：window.__lxNavBack?.() ?? history.back()），本组件在
  // **助手模式可见时**注册探针、切走时注销（保活常驻：不能挂载/卸载判——组件
  // 不知道自身显隐，由 App 传 mode 进来——见 props 注释）。
  //
  // back 语义（从最深到最浅）：子会话临时视图 → 关它；二级视图（拓展/助手配置）
  // → 关它；否则回上一会话（prevAssistantSessionRef 记忆，若有）→ 否则回会话
  // 列表（sessionId 清空 = 中性态）。forward 最小实现：只在 back 刚离开的会话
  // 记忆里对称恢复，无栈则 no-op（与项目域 forward 只能沿浏览器栈对称是同一取舍）。
  // 设计要点：只在**可见**时注册，两个模式不会同时接管——项目域的 popstate 路径
  // 在助手模式里永远不会被侧键命中（壳层先查探针）。
  const prevAssistantSessionRef = useRef("");
  const lastLeftAssistantSessionRef = useRef("");
  // 上一落点记忆：sessionId 变化时把旧值记进 prevAssistantSessionRef（侧键 back
  // 的「回上一落点」依据；空串 = 上一落点是会话列表——back 回中性态）。effect
  // 每渲染比对——与 App 的 prevFocusedRef 同款纪律。探针 back 分支里先手工对齐
  // prevTrackedRef 再切落点（防本 effect 把刚离开的会话又记成 prev——那样反复
  // 按侧键会在两会话之间弹跳而不是逐级回退）。
  const prevTrackedRef = useRef(sessionId);
  useEffect(() => {
    if (prevTrackedRef.current !== sessionId) {
      prevAssistantSessionRef.current = prevTrackedRef.current;
      prevTrackedRef.current = sessionId;
    }
  });

  // ---- 鼠标侧键（2026-12 修复）：助手域导航探针 ----
  // 助手域是纯 useState 导航、从不写浏览器历史——壳层的侧键注入默认走
  // history.back()，消费的是项目模式遗留条目、改到隐藏分支的 state。壳层已改为
  // 探针优先（main.ts：window.__lxNavBack?.() ?? history.back()），本组件在
  // **助手模式可见时**注册探针、切走时注销（保活常驻：不能挂载/卸载判——组件
  // 不知道自身显隐，由 App 传 mode 进来——见 props 注释）。
  //
  // back 语义（从最深到最浅）：子会话临时视图 → 关它；二级视图（拓展/助手配置）
  // → 关它；否则回上一落点（prevAssistantSessionRef——空串 = 回会话列表中性态）。
  // forward 最小实现：只在 back 刚离开的会话记忆里对称恢复，无栈则 no-op（与
  // 项目域 forward 只能沿浏览器栈对称是同一取舍）。
  // 设计要点：只在**可见**时注册，两个模式不会同时接管——项目域的 popstate 路径
  // 在助手模式里永远不会被侧键命中（壳层先查探针）。
  useEffect(() => {
    if (mode !== "assistant") return;
    window.__lxNavBack = () => {
      if (childViewId !== null) { setChildViewId(null); return; }
      if (secondaryView !== null) { setSecondaryView(null); return; }
      if (sessionId) {
        const prev = prevAssistantSessionRef.current;
        // forward 依据 + 手工对齐 prevTrackedRef（见上方 effect 注释），再切落点。
        lastLeftAssistantSessionRef.current = sessionId;
        prevTrackedRef.current = prev;
        prevAssistantSessionRef.current = "";
        if (prev) {
          handleSelect(prev);
        } else {
          // 回会话列表（中性态）：不写 lastAssistantSession 记忆——那是「上次
          // 在哪个会话」的记忆，重进助手模式恢复的还是它；这里只是视图退回列表。
          setSessionId("");
        }
      }
      // 已在中性态：无可后退（no-op——不碰项目域历史）。
    };
    window.__lxNavForward = () => {
      const left = lastLeftAssistantSessionRef.current;
      if (!left || left === sessionId) return;
      lastLeftAssistantSessionRef.current = "";
      handleSelect(left);
    };
    return () => {
      delete window.__lxNavBack;
      delete window.__lxNavForward;
    };
  }, [mode, childViewId, secondaryView, sessionId, handleSelect]);

  const handleNewSession = useCallback(async () => {
    if (!assistant) return;
    try {
      const id = await assistant.newSession();
      knownTick(id);
      writeLastAssistantSession(id);
      setSessionId(id);
      setChildViewId(null);
      setSecondaryView(null);
      void refreshList();
    } catch (e) {
      reportError(`新建助手会话失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [assistant, knownTick, refreshList, reportError]);

  // ---- 子会话临时视图（原 App.tsx 的 isAssistantChild 域判定迁进助手模式）----
  // 助手模式里派发卡的子会话必是助手 run_task 的产物（父会话是助手会话）——
  // state 恒从 assistantStates 取，历史装载/取消恒走 assistant 域。
  const childId = childViewId !== null ? childTabSession(`child:${childViewId}`) : null;

  /** 子会话标题：父会话（childParents 反查）时间线里的 dispatch 块（与主域
   *  childTabTitleOf 同一份纯函数；映射缺失时回落 sessionId 前缀）。 */
  const childTitleOf = useCallback((id: string) => {
    const parent = childParents[id] ?? sessionId;
    const parentState = assistantStates[parent];
    return childTabTitle(timelineOf(parentState), id);
  }, [childParents, sessionId, assistantStates]);

  // ---- 助手域子会话的确认裁决（原 App.tsx 的 resolveAssistantChildOwner 组）----
  const assistantStatesRef = useRef(assistantStates);
  assistantStatesRef.current = assistantStates;
  const resolveAssistantChildOwner = useCallback((id: string): string => {
    const did = assistantStatesRef.current[id]?.pending?.dispatch_id ?? "";
    if (!did) return id;
    const owner = Object.entries(assistantStatesRef.current).find(([, st]) =>
      st.blocks.some((b) => b.kind === "dispatch" && b.id === did))?.[0];
    return owner ?? id;
  }, []);
  const handleChildConfirm = useCallback((id: string, confirmId: string, allow: boolean) => {
    void assistant?.confirm(resolveAssistantChildOwner(id), confirmId, allow)
      .then(() => resolveAssistant(id, confirmId, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [assistant, resolveAssistantChildOwner, resolveAssistant, reportError]);
  const handleChildAnswer = useCallback((id: string, confirmId: string, text: string) => {
    void assistant?.confirm(resolveAssistantChildOwner(id), confirmId, true, text)
      .then(() => resolveAssistant(id, confirmId, "allow", text))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [assistant, resolveAssistantChildOwner, resolveAssistant, reportError]);
  // 子会话视图的 *partial* 包装（AssistantChildPage 要的是 (id, allow) 签名）。
  // **必须 useCallback**（2026-12 闪烁修复）：内联箭头每帧换身份 → ChildSessionPage
  // 的 handleConfirm/handleAnswer（useCallback 依赖 onConfirm/onAnswer）跟着换 →
  // 时间线上的确认卡 Block 逐帧失忆重挂。childId 变化时重生成（依赖它是语义需要）。
  const childConfirmOf = useCallback(
    (childId: string) => (id: string, allow: boolean) => handleChildConfirm(childId, id, allow),
    [handleChildConfirm],
  );
  const childAnswerOf = useCallback(
    (childId: string) => (id: string, text: string) => handleChildAnswer(childId, id, text),
    [handleChildAnswer],
  );

  const activeMeta = useMemo(
    () => sessionList.find((s) => s.id === sessionId),
    [sessionList, sessionId],
  );

  /** 搜索过滤后的列表（按标题包含——与主域 Sidebar 的 query 语义一致）。 */
  const filteredSessions = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return sessionList;
    return sessionList.filter((s) => s.title.toLowerCase().includes(q));
  }, [sessionList, query]);

  if (!assistant) {
    return (
      <div className="assistant-ws">
        <div className="assistant-ws-empty">
          <div className="assistant-ws-empty-title">助手</div>
          <div>当前数据源不支持助手模式（演示模式请升级数据源，或连接后端后重试）。</div>
        </div>
      </div>
    );
  }

  return (
    <div className="assistant-ws">
      {/* 左侧 = 主域 .sidebar 同款骨架（助手域无项目分组）：nav「新对话」→
          搜索框 → 「对话」分组标题 → 会话滚动区 → 底部设置行。全部类名走
          sidebar.css/composer.css 的既有规则，视觉逐像素对齐主域侧栏。 */}
      <aside className="sidebar assistant-ws-side">
        <nav className="nav-list">
          <button type="button" className="nav-item" onClick={() => { void handleNewSession(); }}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
            </svg>
            新对话
          </button>
          {/* 助手模式内嵌视图组（2026-12 用户拍板：助手的配置和拓展在助手
              模式内独立呈现，不跳回项目模式）：点开主区二级视图
              （secondaryView），返回按钮回对话视图。助手配置是独立页
              （AssistantConfigTab 直接挂载，不经 CatalogPage）。激活高亮不接
              secondaryView 状态（打开期间侧栏仍被主区盖住一半语义，保持原有
              恒不高亮的形态）。git/remote nav 已摘除（2026-12 用户拍板：模式
              切换入口唯一化——只有 Topbar 的模式切换控件，助手域不再有任何
              跳去项目模式的按钮）。 */}
          <>
            <button
              type="button"
              className="nav-item"
              data-nav="assistant-config"
              onClick={() => {
                setChildViewId(null);
                setSecondaryView("assistant-config");
              }}
              title="助手的性格与拓展绑定"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
              </svg>
              助手配置
            </button>
            <button
              type="button"
              className="nav-item"
              data-nav="catalog"
              onClick={() => {
                // 拓展：不带页签偏好——保持原语义（开默认 tools 页签）。
                setChildViewId(null);
                setSecondaryView("catalog");
              }}
              title="工具、技能、模板与 MCP 的拓展"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <rect x="3" y="3" width="7" height="7" rx="1" />
                <rect x="14" y="3" width="7" height="7" rx="1" />
                <rect x="3" y="14" width="7" height="7" rx="1" />
                <rect x="14" y="14" width="7" height="7" rx="1" />
              </svg>
              拓展
            </button>
            {/* 占位（与主域同款）：自动化尚未实现——无 onClick，不假装能点 */}
            <button type="button" className="nav-item">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <circle cx="12" cy="12" r="9" />
                <path d="M12 7v5l3 2" />
              </svg>
              自动化
            </button>
          </>
        </nav>
        <div className="sidebar-pad">
          <div className="search-box">
            <svg className="search-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="11" cy="11" r="7" />
              <path d="m20 20-3.5-3.5" />
            </svg>
            <input
              ref={searchRef}
              value={query}
              placeholder="搜索对话"
              aria-label="搜索助手对话"
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setQuery("");
                  e.currentTarget.blur();
                }
              }}
            />
            {query ? (
              <button type="button" className="search-clear" aria-label="清除" onClick={() => setQuery("")}>
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                  <path d="M18 6 6 18M6 6l12 12" />
                </svg>
              </button>
            ) : null}
          </div>
          {listError && (
            <div className="assistant-ws-list-error" role="alert">
              <span>{listError}</span>
              <Button onClick={() => { void refreshList(); }}>重试</Button>
            </div>
          )}
        </div>
        <div className="sidebar-label group-head">
          对话
          <span className="group-actions" aria-hidden>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
              <path d="M3 6h18M6 12h12M10 18h4" />
            </svg>
          </span>
        </div>
        <AssistantSessionList
          sessions={filteredSessions}
          activeId={sessionId}
          onSelect={handleSelect}
        />
        <div className="sidebar-footer">
          <div className="settings-row" role="button" tabIndex={0} onClick={onOpenSettings} onKeyDown={(e) => { if (e.key === "Enter") onOpenSettings(); }}>
            <span className="settings-label">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
              </svg>
              设置
            </span>
            <span className="ver">助手</span>
          </div>
        </div>
      </aside>
      {secondaryView === "assistant-config" ? (
        <main className="assistant-ws-main">
          {/* 助手配置独立页（2026-12 用户拍板）：不经 CatalogPage——返回头 +
              AssistantConfigTab（自包含组件：assistant.config.get/set 数据源、
              useAgents chips、configChanged 订阅都在组件内）。滚动容器与
              .cg-page 同款内边距（.assistant-config-page 承担）。 */}
          <div className="assistant-ws-child-head">
            <Button
              className="assistant-ws-back"
              onClick={() => setSecondaryView(null)}
              title="返回助手会话"
            >
              ← 返回
            </Button>
            <span className="assistant-ws-child-title">助手配置</span>
          </div>
          <div className="assistant-config-page">
            <AssistantConfigTab
              onOpenCatalog={(tab) => {
                // 「管理技能 / 管理 MCP 服务器」→ 拓展页（现成机制）：
                // 预设目标页签 + 切 secondaryView("catalog")。
                setCatalogTabPref(tab);
                setSecondaryView("catalog");
              }}
            />
          </div>
        </main>
      ) : secondaryView === "catalog" ? (
        <main className="assistant-ws-main">
          {/* 拓展页内嵌视图：返回头复用子会话视图同款类与交互，主区挂
              CatalogPage（纯组件、零项目域依赖——useAgents context 在
              助手模式天然可用）。 */}
          <div className="assistant-ws-child-head">
            <Button
              className="assistant-ws-back"
              onClick={() => setSecondaryView(null)}
              title="返回助手会话"
            >
              ← 返回
            </Button>
            <span className="assistant-ws-child-title">拓展</span>
          </div>
          <CatalogPage />
        </main>
      ) : childId !== null ? (
        <main className="assistant-ws-main">
          <div className="assistant-ws-child-head">
            <Button
              className="assistant-ws-back"
              onClick={() => setChildViewId(null)}
              title="返回助手会话"
            >
              ← 返回
            </Button>
            <span className="assistant-ws-child-title">{childTitleOf(childId)}</span>
          </div>
          <AssistantChildPage
            key={childId}
            source={source}
            sessionId={childId}
            state={assistantStates[childId]}
            onConfirm={childConfirmOf(childId)}
            onAnswer={childAnswerOf(childId)}
          />
        </main>
      ) : sessionId ? (
        <main className="assistant-ws-main">
          {/* key = sessionId：按会话强制重挂——bootRef 随实例新建，isBooted 的
              「每实例一次」闸门不再挡住切会话后的历史装载（2026-12 修复：
              切回会话 assistant.history 永不再调的根因）。 */}
          <AssistantPage
            key={sessionId}
            source={source}
            state={assistantStates[sessionId]}
            sessionId={sessionId}
            sessionMeta={activeMeta}
            onSessionId={handleSessionId}
            onOpenChild={(id) => { setChildViewId(id); setSecondaryView(null); }}
            onConfirm={(id, allow) => {
              if (!sessionId) return;
              resolveAssistant(sessionId, id, allow ? "allow" : "deny");
              assistant.confirm(sessionId, id, allow).catch((e) =>
                reportError(`裁决失败: ${e instanceof Error ? e.message : String(e)}`));
            }}
            onAnswer={(id, text) => {
              if (!sessionId) return;
              resolveAssistant(sessionId, id, "allow", text);
              assistant.confirm(sessionId, id, true, text).catch((e) =>
                reportError(`回答失败: ${e instanceof Error ? e.message : String(e)}`));
            }}
          />
        </main>
      ) : (
        /* 中性态：未选择会话——不自动 assistant.new（进入助手模式建空会话
           是旧语义，已废除）。给两条出路：点左侧会话行，或「新对话」。 */
        <main className="assistant-ws-main">
          <div className="assistant-ws-empty">
            <div className="assistant-ws-empty-title">助手</div>
            <div>选择左侧会话继续，或点「新对话」开始。</div>
            <Button onClick={() => { void handleNewSession(); }}>新对话</Button>
          </div>
        </main>
      )}
    </div>
  );
}

// AssistantChildPage / AssistantSessionList 在各自的文件里（避免单文件过长）；
// 这里只 import。
