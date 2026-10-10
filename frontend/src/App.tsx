import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "./shared/motion";
import {
  childTabHint,
  childTabSession,
  childTabTitle,
  closeWorkspacePage,
  createWorkspaceTabs,
  focusChatTab,
  focusChildTab,
  focusHistoryView,
  focusWorkspacePage,
  focusWorkspaceTab,
  isWorkspacePage,
  type WorkspacePage,
  type WorkspaceTab,
  type WorkspaceTabsState,
  type WorkspaceView,
} from "./shared/workspace-tabs";
import { getAgentSource } from "./agent";
import { useAgent, type ThreadBlock } from "./shared/store";
import { queueSendAction } from "./shared/send-queue";
import { draftOf, writeDraft, writeDraftAtts, writeDraftText, type SessionDraft, type SessionDrafts } from "./shared/session-drafts";
import { visionBlockNotice } from "./shared/attachments";
import { beginEdit, canRewind, planRewind, rewindThenRestore, type EditDraft } from "./shared/blocks";
import { AgentsProvider, useAgents } from "./shared/agents";
import { AgentsPage } from "./components/agents/AgentsPage";
import { CatalogPage } from "./components/catalog/CatalogPage";
import { GitWorkbenchPage } from "./components/git/GitWorkbenchPage";
import { RemoteAccessPage } from "./components/remote/RemoteAccessPage";
import { Topbar } from "./components/topbar";
import { Sidebar, LOOSE } from "./components/sidebar";
import type { MergeEntry } from "./components/jobs";
import { Thread } from "./components/thread";
import { Composer, type ComposerDraft } from "./components/composer";
import type { SlashCommand } from "./components/composer/SlashPalette";
import { TabBar } from "./components/topbar/TabBar";
import { SettingsPanel } from "./components/settings";
import { Button } from "./components/form";
// 右栏面板：轮次树（TurnPanel）取代了原先并排的 OutlinePanel + SubAgentPanel——
// 那两个组件与它们的测试仍在（回退用），只是不再挂载。
import { TurnPanel } from "./components/panels/TurnPanel";
import { ChildSessionPage } from "./components/panels/ChildSessionPage";
import { turnGroups } from "./components/panels/turns";
import { SettingsProvider, useSettings } from "./shared/settings";
import { ConnectionsProvider } from "./shared/connections";
import { UpdateProvider } from "./shared/update";
import { SearchAdminProvider } from "./shared/search-admin";
import { JobsProvider } from "./shared/jobs-admin";
import { getSakuraBridge } from "./agent/sakura";
import { getTailscaleBridge } from "./agent/tailscale";
import { getRemoteControlBridge } from "./agent/host";
import { UpdateToast } from "./components/update/UpdateToast";
import type { AgentSource, SendAttachments, SendOptions, SessionMeta } from "./shared/types";
import type { ComposerAttsDraft } from "./components/composer";

export default function App() {
  const source = useMemo(() => getAgentSource(), []);
  // 远程访问的桥（壳主进程领域）：浏览器模式 null → 各模式块演示/提示回落
  const sakuraBridge = useMemo(() => getSakuraBridge(), []);
  const tailscaleBridge = useMemo(() => getTailscaleBridge(), []);
  const remoteControlBridge = useMemo(() => getRemoteControlBridge(), []);
  return (
    <SettingsProvider source={source}>
      <AgentsProvider source={source}>
        <ConnectionsProvider source={source} sakuraBridge={sakuraBridge} remoteControlBridge={remoteControlBridge}>
          <UpdateProvider>
            <SearchAdminProvider source={source}>
              {/* 后台任务域：时间线卡片与顶栏面板共用同一份任务清单 */}
              <JobsProvider source={source}>
                <AppBody source={source} sakuraBridge={sakuraBridge} tailscaleBridge={tailscaleBridge} remoteControlBridge={remoteControlBridge} />
                <UpdateToast />
              </JobsProvider>
            </SearchAdminProvider>
          </UpdateProvider>
        </ConnectionsProvider>
      </AgentsProvider>
    </SettingsProvider>
  );
}

function AppBody({ source, sakuraBridge, tailscaleBridge, remoteControlBridge }: {
  source: AgentSource;
  sakuraBridge: ReturnType<typeof getSakuraBridge>;
  tailscaleBridge: ReturnType<typeof getTailscaleBridge>;
  remoteControlBridge: ReturnType<typeof getRemoteControlBridge>;
}) {
  // 自动发送的桥：useAgent 在组件顶部调用，而真正会发送的 sendWithOptions 要等
  // 设置/providers 就绪后才定义——经 ref 反转依赖（store 只喊「该发了」，App 决定
  // 怎么发：请求级参数 effort/approval/agent 都在 sendWithOptions 里补齐）。
  // atts = 队首条目携带的附件（图片批次 B——按引用透传）。
  const autoSendBridgeRef = useRef<(sessionId: string, text: string, atts?: SendAttachments) => void>(() => {});
  /** 发送失败的附件回滚（store 钩子 → Composer 的 attsDraft）：文本丢了附件不能丢。
   *  per-session 记账（drafts）之下它只服务「正看着的那个会话」——失败发生在别的
   *  会话时只落记账键（切回去由初始草稿还原），不装给挂载中的 Composer。 */
  const [attsDraft, setAttsDraft] = useState<ComposerAttsDraft | null>(null);
  const attsDraftIdRef = useRef(0);
  /** 当前会话 id 的 ref：草稿记账的写入点要落在**正确的会话**上——失败回填可能
   *  在用户已切到别的会话之后到达（ws 异步），按 currentId 闭包写会串会话。 */
  const currentIdRef = useRef("");
  const onSendFailedBridgeRef = useRef<(sessionId: string, atts: SendAttachments) => void>(() => {});
  const { state, sessionStates, childParents, sendQueues, send, resolve, clearError, reportError, enqueueSend, removeQueuedSend, moveQueuedToFront } = useAgent(source, {
    onAutoSend: (sessionId, text, atts) => autoSendBridgeRef.current(sessionId, text, atts),
    onSendFailed: (sessionId, atts) => onSendFailedBridgeRef.current(sessionId, atts),
  });
  const { settings, providers } = useSettings();
  // agents：子会话标签的标题要按 agentId 回落显示名（回放块的 agentName 是空串）
  const { agents, activeAgentId } = useAgents();
  /** 工作区页签与会话标签分开；未关闭的工作区页面保持挂载以保留表单/视图状态。 */
  const [workspaceState, setWorkspaceState] = useState(createWorkspaceTabs);
  const workspaceStateRef = useRef(workspaceState);
  workspaceStateRef.current = workspaceState;
  // 各会话 state 的 ref：子会话裁决要按 dispatch_id 反查持有挂起确认的那个会话，
  // 而回调身份必须稳定（Block 是 memo 的，每次新建回调会逐帧击穿它）——所以读 ref
  // 而不是把 sessionStates 放进 useCallback 的依赖里。
  const sessionStatesRef = useRef(sessionStates);
  sessionStatesRef.current = sessionStates;
  const view = workspaceState.active;
  // ---- 工作区导航 = **真实浏览器历史**（History API；2026-10-07 二轮重写）----
  //
  // 一轮的教训：硬件侧键（和 Alt+←/→）在 Chromium 里由**浏览器进程**处理——不派发
  // 给页面（页面收不到 button 3/4 的 mousedown），直接导航 WebContents 自己的历史栈
  //（electron#22202；CDP 合成事件走另一条管线，所以页面监听方案在真机必然失效）。
  // pushState 推的条目就在那个栈里（electron#22269 佐证）。所以「跟网页一样」的
  // 唯一实现：每次切页 pushState 一个条目，popstate 时把视图读回来——浏览器历史
  // 就是我们的栈，侧键/Alt+方向/浏览器后退全部原生生效。
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsOpenRef = useRef(settingsOpen);
  settingsOpenRef.current = settingsOpen;
  /** 对话范围（项目 id / ""=未分组）——App 持有：侧栏过滤、「新对话」归属、
   *  空态项目标签三处共用。用户拍板：**恒有范围**（启动即「未分组」，点项目行
   *  切换且不可取消——没有「全部」视图）。 */
  const [filter, setFilter] = useState<string>(LOOSE);
  const [gitProjectId, setGitProjectId] = useState("");
  /** 右侧「已发送消息」大纲的开合：**纯 UI 状态**（不进会话历史、不落库、切会话不
   *  清——它只是当前窗口的查看方式）。 */
  const [outlineOpen, setOutlineOpen] = useState(true);

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
  //（state 为 null = 退到了首条 → 回聊天）。重开已关的子会话标签是**有意**的：
  // 浏览器后退到一页就是把它恢复出来（focusHistoryView）。
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
  // 关闭的工作区标签：固定页签（agents/catalog/git）与子会话标签（child:<id>）同一套语义
  const closeWorkspace = useCallback((tab: WorkspaceTab): WorkspaceView => {
    const next = closeWorkspacePage(workspaceStateRef.current, tab);
    pushWorkspace(next);
    return next.active;
  }, [pushWorkspace]);
  /** 标签栏点击一个工作区标签：固定页签走既有打开逻辑，子会话标签只是**聚焦**
   *  （它的内容已经挂载并保活——见 WorkspaceViewPanels，重开不重新读历史）。 */
  const openWorkspaceTab = useCallback((tab: WorkspaceTab) => {
    if (isWorkspacePage(tab)) {
      openWorkspace(tab);
      return;
    }
    pushWorkspace(focusWorkspaceTab(workspaceStateRef.current, tab));
  }, [openWorkspace, pushWorkspace]);
  /** 卡上的「打开子会话」→ 把子会话作为**独立工作区标签**打开（focusChildTab 去重：
   *  重复打开同一个子会话回到同一个标签，不会并排长出两个）。 */
  const openChildTab = useCallback((sessionId: string) => {
    pushWorkspace(focusChildTab(workspaceStateRef.current, sessionId));
  }, [pushWorkspace]);
  const openAgents = useCallback(() => openWorkspace("agents"), [openWorkspace]);
  const openCatalog = useCallback(() => openWorkspace("catalog"), [openWorkspace]);
  const openGit = useCallback(() => {
    const projects = source.projects();
    setGitProjectId((current) => current || (filter && projects.some((project) => project.id === filter) ? filter : projects[0]?.id ?? ""));
    openWorkspace("git");
  }, [filter, openWorkspace, source]);
  const openRemote = useCallback(() => openWorkspace("remote"), [openWorkspace]);
  const backToChat = useCallback(() => {
    pushWorkspace(focusChatTab(workspaceStateRef.current));
  }, [pushWorkspace]);
  const focusSession = (id: string) => {
    setFilter(source.sessions().find((session) => session.id === id)?.workspace ?? LOOSE);
    void source.resumeSession(id);
    backToChat();
  };

  // live 写失败桥（AgentsProvider 的乐观更新 WS 调用失败 → 一次性提示）
  useEffect(() => {
    const on = (e: Event) => reportError(String((e as CustomEvent<string>).detail ?? ""));
    window.addEventListener("lx-operation-error", on);
    return () => window.removeEventListener("lx-operation-error", on);
  }, [reportError]);

  const currentId = state.currentId;
  currentIdRef.current = currentId;
  const busyBySession = useMemo(
    () => Object.fromEntries(Object.entries(sessionStates).map(([id, session]) => [id, session.busy])),
    [sessionStates],
  );

  // 子会话标签按需显示（体验修复批次 5）：活跃主会话 = chat 视图时的 currentId；
  // 正在看某个子会话标签时 = 那个子会话的父（映射缺失时回落 currentId——子标签
  // 活跃的导航路径里 currentId 本来就是父会话：focusChildTab 不改 currentId）。
  const activeChildId = childTabSession(view);
  const activeParent = activeChildId !== null ? childParents[activeChildId] ?? currentId : currentId;

  // 「合并请求」入口（后台任务面板）：只对项目会话可用——workspace 取当前会话
  // 的归属（列表还没刷新到新会话时回落对话范围 filter：刚建的会话本来就没有
  // 可合并的分支，落 "" 与落项目 id 的差别只是入口能不能点，后端还有同一道校验）。
  const currentWorkspace =
    source.sessions().find((s) => s.id === currentId)?.workspace ?? (filter === LOOSE ? "" : filter);
  const handleMergeStart = useCallback(async (sessionId: string, targetBranch: string) => {
    await source.mergeRequest(sessionId, targetBranch);
  }, [source]);
  const mergeEntry = useMemo<MergeEntry | undefined>(() => {
    if (!currentId) return undefined;
    return { sessionId: currentId, workspace: currentWorkspace, onStart: handleMergeStart };
  }, [currentId, currentWorkspace, handleMergeStart]);

  const projects = source.projects();
  useEffect(() => {
    if (view === "git" && !gitProjectId && projects.length > 0) setGitProjectId(projects[0].id);
  }, [view, gitProjectId, projects]);

  // 当前范围的项目名（「未分组」→ 空态不标——无归属不需要声明）
  const filterProjectName =
    filter === LOOSE ? undefined : projects.find((p) => p.id === filter)?.name;

  // 发送时携带请求级参数：effort 只在当前模型声明推理能力时上帧（后端
  // 能力门控会丢弃不匹配档位，不带上帧更诚实）；approval 恒带当前设置；
  // agent = 当前选用的 Agent（主 Agent 也显式携带——后端按名单语境跑）。
  // atts = 随消息的附件（图片/文件，图片批次 B）：按引用进 opts，不复制 base64。
  // sendToSession：目标会话显式传入——缓冲区的自动发送可能由「当前会话」的
  // busy 翻转触发，但触发瞬间 currentId 与目标一致，显式传参不依赖这个巧合。
  const sendToSession = useCallback((sessionId: string, text: string, atts?: SendAttachments) => {
    const current = providers.flatMap((p) => p.models).find((m) => m.id === settings.model);
    const opts: SendOptions = { approval: settings.approval, agent: activeAgentId };
    if (current && current.efforts.length > 0) opts.effort = settings.effort;
    if (atts?.images.length) opts.images = atts.images;
    if (atts?.files.length) opts.files = atts.files;
    if (sessionId) send(sessionId, text, opts);
  }, [providers, settings.model, settings.effort, settings.approval, send, activeAgentId]);
  const sendWithOptions = useCallback((text: string, atts?: SendAttachments) => sendToSession(currentId, text, atts), [sendToSession, currentId]);
  // 桥接（渲染期同步赋值——store 的订阅闭包经 ref 读到最新身份）
  autoSendBridgeRef.current = sendToSession;
  onSendFailedBridgeRef.current = useCallback((sessionId: string, atts: SendAttachments) => {
    // 失败回填落在**原会话**的记账键（shared/session-drafts.ts）：不管用户此刻
    // 看的是哪个会话，切回去文本不丢、附件也在。
    setDrafts((cur) => writeDraftAtts(cur, sessionId, atts));
    // 正看着这个会话才经 attsDraft 把附件装回**挂载中**的 Composer——装给别的
    // 会话的 Composer 就是串会话（这正是本次要修的 bug）。
    if (sessionId === currentIdRef.current) {
      attsDraftIdRef.current += 1;
      setAttsDraft({ id: attsDraftIdRef.current, atts });
    }
  }, []);

  // 稳定身份：Thread 的 Block 用 memo，onConfirm 每次新建会击穿它
  const handleConfirm = useCallback((id: string, allow: boolean) => {
    void source.confirm(currentId, id, allow)
      .then(() => resolve(currentId, id, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError, currentId]);

  /** 主会话时间线上 ask_user 提问的回答：文本答案发给当前会话（与 confirm 同一条
   *  tool.confirm 通道——answer 非空时后端走 Answer 路径）。 */
  const handleAnswer = useCallback((id: string, text: string) => {
    void source.answer(currentId, id, text)
      .then(() => resolve(currentId, id, "allow", text))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError, currentId]);

  /** 子会话标签页里的裁决。
   *
   *  **必须发到持有挂起确认的那个会话**：确认门由**父会话代理**（AGENTS.md §2.3——
   *  子会话自己持 pending 的话服务端的 tool.confirm 找不到它，会话会卡在 busy），
   *  而 `tool.confirm` 是按 session_id 找会话再找它那条挂起确认的（见 server 的
   *  MethodToolConfirm）。所以这里按确认的 `dispatch_id` 反查**卡在哪条会话里**——
   *  那张卡就是父会话，挂起确认就在它手上。
   *
   *  查不到（老数据没记下 dispatch_id）时退回子会话 id：发出去顶多报一句
   *  "没有挂起的确认"，而 store 侧的两处定格照旧生效——不会出现"点了没反应"。 */
  const handleChildConfirm = useCallback((sessionId: string, id: string, allow: boolean) => {
    const did = sessionStatesRef.current[sessionId]?.pending?.dispatch_id ?? "";
    const owner = did
      ? Object.entries(sessionStatesRef.current).find(([, st]) =>
          st.blocks.some((b) => b.kind === "dispatch" && b.id === did))?.[0]
      : undefined;
    void source.confirm(owner ?? sessionId, id, allow)
      .then(() => resolve(sessionId, id, allow ? "allow" : "deny"))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError]);

  /** 子会话标签页里 ask_user 提问的回答：与 handleChildConfirm 同一条 owner 反查
   *  规则（提问由父会话代理挂起，答案必须发给持有它的会话）——只换成 answer 方法
   *  与「已回答」定格。 */
  const handleChildAnswer = useCallback((sessionId: string, id: string, text: string) => {
    const did = sessionStatesRef.current[sessionId]?.pending?.dispatch_id ?? "";
    const owner = did
      ? Object.entries(sessionStatesRef.current).find(([, st]) =>
          st.blocks.some((b) => b.kind === "dispatch" && b.id === did))?.[0]
      : undefined;
    void source.answer(owner ?? sessionId, id, text)
      .then(() => resolve(sessionId, id, "allow", text))
      .catch((e) => reportError(e instanceof Error ? e.message : String(e)));
  }, [source, resolve, reportError]);

  // 手动压缩：请求类失败进一次性提示（不动 blocks）；没有可压收益时给一句人话
  // 原因（不是错误——历史还太短是正常态）。压缩成功由 chat.compacted 事件渲染标记块。
  const handleCompact = useCallback(async () => {
    try {
      const res = await source.compact(currentId);
      if (!res.compacted) reportError(res.reason ?? "没有可压缩的历史");
    } catch (e) {
      reportError(`压缩失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [source, reportError, currentId]);

  // ---- 用户气泡的三个动作（复制在 Block 内自足；这里管编辑与撤回）----

  /** 输入框草稿注入（撤回/编辑把原文放回输入框；atts = 附件一并装回附件区——
   *  队列条目「编辑」的装回路径）。id 单调递增——同一条消息连续注入两次也要
   *  重新写入（按文本比较的话第二次是 no-op，用户看到"点了没反应"）。
   *
   *  注入同时落**当前会话**的草稿记账键（drafts，shared/session-drafts.ts）：
   *  Composer 挂载中的那份由 draft prop 即时生效，记账键服务「切走再切回」的
   *  还原——两处写的是同一份语义。 */
  const [draft, setDraft] = useState<ComposerDraft | null>(null);
  const draftIdRef = useRef(0);
  /** per-session 草稿记账（照 sendQueues 的模式，独立于 reduce 流——纯 UI 状态）：
   *  键 = session id，值 = 文本 + 暂存附件。切走保留、切回还原、发送清空、失败
   *  回填落原会话键。 */
  const [drafts, setDrafts] = useState<SessionDrafts>({});
  const injectDraft = useCallback((text: string, atts?: SendAttachments) => {
    draftIdRef.current += 1;
    setDraft({ id: draftIdRef.current, text, ...(atts ? { atts } : {}) });
    const sid = currentIdRef.current;
    setDrafts((cur) => writeDraftAtts(writeDraftText(cur, sid, text), sid, atts));
  }, []);
  /** Composer 的草稿实时回写 → 当前会话的记账键（采集端见 Composer.onDraftChange：
   *  打字/注入/清空/回填全汇聚到那一个 effect）。身份恒定——写哪个会话由
   *  currentIdRef 在调用瞬间决定，不进依赖。 */
  const handleDraftChange = useCallback((d: SessionDraft) => {
    setDrafts((cur) => writeDraft(cur, currentIdRef.current, d));
  }, []);
  /** 编辑态：只记锚点，**历史一个字都不动**——真正的撤回推迟到下次发送前。 */
  const [editDraft, setEditDraft] = useState<EditDraft | null>(null);
  /** 最新时间线的引用（撤回要按锚点取原文）。**刻意用 ref 而不是把 state.blocks
   *  写进 handleRewind 的依赖**：依赖它 = 每个流式 delta 都换一次回调身份，而
   *  Block 是 memo 的（onRewind 变了 → 整屏用户气泡每帧重渲染一遍）。 */
  const blocksRef = useRef(state.blocks);
  blocksRef.current = state.blocks;

  // 撤回：历史立刻清空（由 source 的乐观 rewound 事件驱动归约器截断——两个实现
  // 走同一条路径）。**原文回到输入框的时机 = rewind 成功之后**（rewindThenRestore，
  // 与 handleSend 的编辑重发同款两步一致）：后端拒绝（配对校验）时文本根本不进
  // 输入框，时间线由 source 已用后端真相对齐（重放历史把被乐观删掉的块拿回来），
  // 这里只负责把失败说给用户听。
  const handleRewind = useCallback((block: ThreadBlock) => {
    if (!canRewind(block)) {
      reportError("这条消息来自旧版后端（没有 seq），无法撤回");
      return;
    }
    // 文本取自纯函数给出的撤回计划（与归约器同一份判定）——文本与截断不可能对不上
    const plan = planRewind(blocksRef.current, block.seq);
    if (!plan) {
      reportError("这条消息已经不在当前对话里了（可能已被撤回）");
      return;
    }
    setEditDraft(null); // 撤回之后没有"编辑中"这回事：历史已经清了，没什么可取消
    rewindThenRestore({
      rewind: () => source.rewind(currentId, block.seq),
      text: plan.text,
      inject: injectDraft,
      report: reportError,
    });
  }, [source, currentId, injectDraft, reportError]);

  // 编辑：只把原文放回输入框并进入编辑态——**不动历史**。编辑是"我可能改主意"，
  // 一点就把后面的对话毁掉是不可接受的；真正的撤回推迟到下次发送前。
  const handleEdit = useCallback((block: ThreadBlock) => {
    const d = beginEdit(block);
    if (!d) {
      reportError("这条消息来自旧版后端（没有 seq），无法编辑");
      return;
    }
    setEditDraft(d);
    injectDraft(d.text);
  }, [injectDraft, reportError]);

  // 取消编辑：退出编辑态。输入框里的文本**留着**（用户可能还想发）——取消只是
  // 撤回"这条是编辑"的语义，下次发送就是一条普通的新消息。
  const handleCancelEdit = useCallback(() => setEditDraft(null), []);

  // 切会话必须退出编辑态：编辑锚点（seq）只在它所属的那个会话里有意义，带着它
  // 切走再发送会去撤回**另一个会话**的那条消息（静默删错历史——比不生效坏得多）。
  // 注入态（draft / attsDraft）同理一并清：它们是「当前会话」的一次性动作，注入
  // 的内容已实时同步进那个会话的草稿记账键，切回来由初始草稿还原；不清的话重挂
  // 载的新 Composer 会把上一个会话的注入草稿再吃一遍（串会话）。
  useEffect(() => {
    setEditDraft(null);
    setDraft(null);
    setAttsDraft(null);
  }, [currentId]);

  // 发送：编辑态下**先撤回再发**（这是"编辑"真正生效的时刻）。撤回失败就不发——
  // 历史没清干净就发，新消息会接在被编辑那条的后面，等于改了个寂寞；文本还给
  // 用户重试（Composer 的 submit 已经先清了输入框）。atts 随消息携带（图片批次 B），
  // 撤回失败同样把附件装回输入框。
  const handleSend = useCallback((text: string, atts?: SendAttachments) => {
    const editing = editDraft;
    setEditDraft(null);
    if (!editing) {
      sendWithOptions(text, atts);
      return;
    }
    void source.rewind(currentId, editing.seq)
      .then(() => sendWithOptions(text, atts))
      .catch((e) => {
        reportError(`撤回失败，未发送: ${e instanceof Error ? e.message : String(e)}`);
        injectDraft(text, atts);
      });
  }, [editDraft, source, currentId, sendWithOptions, reportError, injectDraft]);

  // ---- 发送缓冲区（体验修复批次 5）：busy 时提交的消息在这里排队 ----
  // submit 入队走 Composer 的 onQueue（→ enqueueSend）；队列条目的三个动作：
  // 编辑 = 取出回填输入框（改完再发，或再入队）；删除 = 移除；直接发送 =
  // 空闲立即按正常路径发（乐观气泡照常），忙时把该条置顶等本轮结束。
  const [queueNotice, setQueueNotice] = useState("");
  const queueNoticeTimerRef = useRef<number | null>(null);
  const flashQueueNotice = useCallback((text: string) => {
    setQueueNotice(text);
    if (queueNoticeTimerRef.current !== null) window.clearTimeout(queueNoticeTimerRef.current);
    queueNoticeTimerRef.current = window.setTimeout(() => setQueueNotice(""), 4000);
  }, []);
  const handleQueueEdit = useCallback((id: string) => {
    const item = sendQueues[currentId]?.find((entry) => entry.id === id);
    if (!item) return;
    removeQueuedSend(currentId, id);
    // 附件随文本一并装回输入框（图片批次 B：编辑 = 文本 + 附件都回 Composer）
    const atts: SendAttachments | undefined =
      item.images?.length || item.files?.length
        ? { images: item.images ?? [], files: item.files ?? [] }
        : undefined;
    injectDraft(item.text, atts);
  }, [sendQueues, currentId, removeQueuedSend, injectDraft]);
  const handleQueueDelete = useCallback((id: string) => {
    removeQueuedSend(currentId, id);
  }, [currentId, removeQueuedSend]);
  const handleQueueSend = useCallback((id: string) => {
    const item = sendQueues[currentId]?.find((entry) => entry.id === id);
    if (!item) return;
    // 附件按引用透传（队列条目与发送参数共享同一数组——不复制 base64）
    const atts: SendAttachments | undefined =
      item.images?.length || item.files?.length
        ? { images: item.images ?? [], files: item.files ?? [] }
        : undefined;
    // 去向判定在 send-queue.ts 的 queueSendAction（纯函数，测试直测同一份）：
    // 空闲 → 立即按正常路径发（乐观气泡照常）；忙/发送中 → 置顶（自动发送在
    // busy true→false 时先发它）+ 一次性提示。
    if (queueSendAction({ busy: state.busy, sending: state.sending }) === "send-now") {
      removeQueuedSend(currentId, id);
      sendWithOptions(item.text, atts);
      return;
    }
    moveQueuedToFront(currentId, id);
    flashQueueNotice("当前会话正在生成，已置顶，本轮结束后立即发送");
  }, [sendQueues, currentId, state.busy, state.sending, removeQueuedSend, sendWithOptions, moveQueuedToFront, flashQueueNotice]);
  const currentQueue = sendQueues[currentId] ?? [];

  // ---- vision 可用性感知（图片批次 B，尽力而为）----
  // 判定链见 shared/attachments.ts 的 visionBlockNotice（纯函数，测试直测同一份）：
  // 当前会话**实际**模型（state.model——chat.history 的 Model 字段，与后端
  // sendImages 的 ModelFor 同一条解析路径的产物）优先，拿不到回落全局设置的模型。
  const visionBlocked = useMemo(
    () => visionBlockNotice(state.model || settings.model, providers.flatMap((p) => p.models)),
    [state.model, settings.model, providers],
  );

  // ---- 右侧「轮次」面板 ----
  // 分组来自纯函数：一条 user 块开一轮，其后的派发都归入该轮（判定与摘要都在
  // panels/turns.ts——提示条 notice 在历史里也是 user 角色消息，但它不开新轮）。
  const groups = useMemo(() => turnGroups(state.blocks), [state.blocks]);
  /** 面板头的轮次数（如「共 7 轮 · 12 次派发」）。 */
  const dispatchCount = groups.reduce((n, group) => n + group.agents.length, 0);
  /** 最近跳转的那个块（高亮跟随点击）。它可能已经不在列表里（撤回/切会话），
   *  所以取用时再确认一次存在性。 */
  const [jumpedUid, setJumpedUid] = useState<number | null>(null);
  /** 默认高亮最后一轮的组头（最近发的那条用户消息）——沿用原大纲的算法。
   *
   *  存在性判据必须**同时**覆盖组头与组内的子 Agent：点组内某个子 Agent 时 jumpedUid
   *  是那条派发卡的 uid，只在组头里找会把它判成「不存在」而回落到最后一轮——点谁谁不亮。
   *  最后一条 user 之前的「会话开始」组没有锚点（uid: null），跳过它继续往前找。 */
  const lastTurnUid = groups.reduce<number | null>((acc, group) => (group.uid !== null ? group.uid : acc), null);
  const activeUid =
    jumpedUid !== null && groups.some((group) => group.uid === jumpedUid || group.agents.some((agent) => agent.uid === jumpedUid))
      ? jumpedUid
      : lastTurnUid;
  const targetTimerRef = useRef<number | null>(null);
  /** 跳到某条已发送消息：这里**只记目标**，滚动与高亮交给 Thread。
   *
   *  为什么不在 App 里滚：线程是**窗口化渲染**（只挂底部若干块），目标可能根本没
   *  挂上 DOM——在这里按 data-uid 找不到元素就静默变成 no-op，而长会话恰恰是最需要
   *  跳转的场景（用户点大纲第一条却毫无反应）。扩窗要改 Thread 自己的 windowSize，
   *  所以整跳归它：先撑窗到包含目标，等元素真的挂上再滚 + 高亮。
   *  寻址仍按 data-uid **精确匹配**——不许按文本找元素（同一句话发两次会命中错的那条）。 */
  const handleOutlineJump = useCallback((uid: number) => {
    setJumpedUid(uid);
  }, []);

  const currentTitle = state.blocks.length === 0 ? "" : source.sessions().find((s: SessionMeta) => s.id === currentId)?.title ?? "任务";

  /** 子会话标签标题：Agent 名 + 任务摘要（如 `researcher · 通读 internal/agent`）。
   *
   *  数据来自**父会话主时间线里那张 dispatch 块**（childTabTitle 纯函数按 sessionId
   *  命中）。父 = 子→父映射查到的（dispatchStart 的 owner_session_id / 历史回放
   *  各记了一份）；映射缺失时回落 currentId——标签刚打开、父历史还没回放的窗口期。
   *  反查必须用父的 blocks：传当前活跃会话的 blocks 的话，切到别的会话后反查失败，
   *  子会话标签标题会回落成「子会话 <id 前 8 位>」（本批修掉的既有缺陷）。
   *  回放块的 agentName 是空串（展示名是 Agent 注册表的知识），所以按 agentId 回落——
   *  与 DispatchCard / TurnPanel 同款。找不到块时 childTabTitle 自己兜底
   *  「子会话 <id 前 8 位>」，**不许空白**。 */
  const childTabTitleOf = useCallback((sessionId: string) => {
    const parent = childParents[sessionId] ?? currentId;
    const blocks = sessionStates[parent]?.blocks ?? state.blocks;
    return childTabTitle(blocks, sessionId, (agentId) => agents.find((a) => a.id === agentId)?.name ?? "");
  }, [childParents, currentId, sessionStates, state.blocks, agents]);
  /** 子会话标签的 hover 提示：`<项目名> · 主会话「<主会话标题>」 · <子会话标签名>`。
   *
   *  主会话 = 该子会话的**父**（映射查父，与 childTabTitleOf 同一条链路——dispatch
   *  块就在父的主时间线里）；项目名按父会话的 workspace（项目 id）查 projects，
   *  未分组会话没有项目名，childTabHint 自动少一节。 */
  const childTabHintOf = useCallback((sessionId: string) => {
    const parent = childParents[sessionId] ?? currentId;
    const main = source.sessions().find((s) => s.id === parent);
    const projectName = main?.workspace
      ? source.projects().find((p) => p.id === main.workspace)?.name
      : undefined;
    return childTabHint(projectName, main?.title ?? "", childTabTitleOf(sessionId));
  }, [source, childParents, currentId, childTabTitleOf]);
  // 顶栏标题与标签栏标题走**同一个**函数：各算一次早晚分叉（标签栏写着 researcher · …，
  // 顶栏却写着另一个名字）
  // activeChildId/activeParent 已在 busyBySession 旁算出（TabBar 过滤与标题反查共用）
  // 子会话标题加「子会话 · 」前缀（用户报「子会话和主会话表明的不明显」）：标签栏那边靠
  // ↳ + 「子会话」胶囊 + 淡成功色底三个通道区分，而**顶栏只有一行文字**，没有那些通道，
  // 所以在这里补一个文字前缀。标题正文仍来自同一个 childTabTitleOf（不各算一次），
  // 这里只是显示层加前缀——两处不会给出**不同的标题**，只会一个带前缀一个带图标。
  const viewTitle = activeChildId !== null
    ? `子会话 · ${childTabTitleOf(activeChildId)}`
    : view === "agents" ? "Agent 名单" : view === "catalog" ? "拓展" : view === "git" ? "Git 管理" : view === "remote" ? "远程访问" : currentTitle;

  // 壳环境（Electron）= 真实窗口；浏览器 = 保留模拟壳（窗口模拟一层的差异，
  // 内部布局完全一致——同组件，不再两份 JSX）
  const isShell = typeof navigator !== "undefined" && navigator.userAgent.includes("Electron");
  // 操作提示条：请求级失败的一次性提示。**不复用 .error-block**——那条是
  // 时间线内的行内文本（Block/SettingsPanel 也在用），给它加底色会连带改掉
  // 那两处的排版。这里要的是一条独立的、可关闭的提示条（与 .job-notice 同款
  // 形态：左缘竖线 + 圆角 + 全宽），只是走危险色。
  const errorNotice = state.operationError && (
    <div className="op-notice" role="alert">
      <span className="op-notice-icon" aria-hidden>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
          <path d="M12 9v4" />
          <path d="M12 17h.01" />
        </svg>
      </span>
      <span className="op-notice-text">{state.operationError}</span>
      {/* 关闭走表单套件（.fd-btn-g），不裸写原生 button——裸写就是 OS 默认皮肤 */}
      <Button className="op-notice-close" onClick={clearError}>关闭</Button>
    </div>
  );

  // 斜杠命令集（命令面板）：页面导航。后端化时同一面板接会话/工具域
  // 命令（/resume /compact …）与选择器聚焦。
  const slashCommands: SlashCommand[] = useMemo(() => [
    { name: "new", desc: "开始新对话", run: () => { void source.newSession(filter); backToChat(); } },
    { name: "compact", desc: "压缩早期历史（腾出上下文）", run: () => { void handleCompact(); } },
    { name: "agents", desc: "打开 Agent 名单与组装", run: openAgents },
    { name: "catalog", desc: "打开拓展（工具/技能/模板/MCP）", run: openCatalog },
    { name: "settings", desc: "打开设置", run: () => setSettingsOpen(true) },
  ], [source, filter, openAgents, openCatalog, handleCompact]);

  const renderView = (activeView: WorkspaceView): ReactNode => {
    if (activeView === "chat") {
      // 左聊天列 + 右大纲栏。分栏而不是把面板浮在时间线上：面板要独立滚动，
      // 且不许把聊天列挤成 0 宽（聊天列 flex:1，面板固定宽度——见 panels.css）。
      return (
        <div className="chat-split">
          <div className="chat-main">
            {errorNotice}
            <div className="thread-scroll">
              <Thread state={state} onConfirm={handleConfirm} onAnswer={handleAnswer} onSuggestion={handleSend} projectName={filterProjectName} onEdit={handleEdit} onRewind={handleRewind} onOpenChild={openChildTab} revealUid={jumpedUid} />
            </div>
            {/* key=sessionId：切会话重挂载——输入框/附件区从该会话的草稿记账还原
                （initialDraft），B 的输入框是 B 自己的（没有就是空）。不重挂的话
                Composer 内部 state 跨会话存活，草稿隔离无从谈起。 */}
            <Composer
              key={currentId}
              busy={state.busy}
              sending={state.sending}
              todos={state.todos}
              context={state.context}
              stats={state.stats}
              onSend={handleSend}
              onCancel={() => source.cancel(currentId)}
              onCompact={() => { void handleCompact(); }}
              commands={slashCommands}
              draft={draft}
              editing={editDraft !== null}
              onCancelEdit={handleCancelEdit}
              queue={currentQueue}
              queueNotice={queueNotice}
              onQueue={(text, atts) => enqueueSend(currentId, text, atts)}
              onQueueEdit={handleQueueEdit}
              onQueueDelete={handleQueueDelete}
              onQueueSend={handleQueueSend}
              attsDraft={attsDraft}
              initialDraft={draftOf(drafts, currentId)}
              onDraftChange={handleDraftChange}
              visionBlocked={visionBlocked}
            />
          </div>
          <aside className="outline-aside" data-open={outlineOpen ? "true" : "false"}>
            <div className="outline-head">
              {/* 标题与轮次数竖排：240px 宽横排放不下「轮次」与「共 7 轮 · 12 次派发」两段字 */}
              {outlineOpen && (
                <div className="turn-head-text">
                  <span className="outline-title">轮次</span>
                  {groups.length > 0 && (
                    <span className="turn-count">
                      共 {groups.length} 轮 · {dispatchCount} 次派发
                    </span>
                  )}
                </div>
              )}
              {/* 开合按钮走表单套件（裸 button = OS 默认灰皮） */}
              <Button
                className="outline-toggle"
                aria-expanded={outlineOpen}
                aria-label={outlineOpen ? "收起轮次面板" : "展开轮次面板"}
                title={outlineOpen ? "收起轮次面板" : "展开轮次面板"}
                onClick={() => setOutlineOpen((open) => !open)}
              >
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                  <path d="m15 18-6-6 6-6" />
                </svg>
              </Button>
            </div>
            {/* 唯一的右栏面板：按轮次分组的树（一条用户消息一轮，其下的子 Agent 挂在组内）。
             *  跳转复用同一个 handleOutlineJump（扩窗 + 滚 + 高亮都在 Thread 里，面板不碰 DOM）。 */}
            {outlineOpen && <TurnPanel groups={groups} activeUid={activeUid} onJump={handleOutlineJump} />}
          </aside>
        </div>
      );
    }
    if (activeView === "agents") return <AgentsPage />;
    if (activeView === "catalog") return <CatalogPage />;
    if (activeView === "remote") {
      return <RemoteAccessPage sakuraBridge={sakuraBridge} tailscaleBridge={tailscaleBridge} remoteControlBridge={remoteControlBridge} />;
    }
    if (activeView === "git") {
      return (
        <GitWorkbenchPage
          key={gitProjectId}
          source={source}
          projects={projects}
          projectId={gitProjectId}
          onProjectChange={setGitProjectId}
          onOpenSession={focusSession}
        />
      );
    }
    // 剩下的只可能是子会话标签（键 child:<sessionId>）：渲染子会话自己的完整时间线。
    // key=childId：不同子会话各自一份实例（切标签不串历史，保活由 WorkspaceViewPanels 管）。
    const childId = childTabSession(activeView);
    if (childId === null) return null; // 坏键（不该出现）：不渲染，也不炸
    // state 来自 store 的 sessionStates[childId]——**实时**（store 把带 dispatch_id 的
    // 子事件同时归约进子会话自己的 state），历史由这一页装载（source.childHistory 发
    // historyLoaded）。裁决走这个子会话自己的 session id（父会话代理确认门）。
    return (
      <ChildSessionPage
        key={childId}
        sessionId={childId}
        source={source}
        state={sessionStates[childId]}
        onConfirm={(id, allow) => handleChildConfirm(childId, id, allow)}
        onAnswer={(id, text) => handleChildAnswer(childId, id, text)}
        onBack={backToChat}
      />
    );
  };

  const app = (
    <div className="app">
      <Topbar
        taskTitle={viewTitle}
        source={source}
        connected={false}
        merge={mergeEntry}
      />
      <TabBar
        source={source}
        currentId={currentId}
        busyBySession={busyBySession}
        onNewChat={() => { void source.newSession(filter); backToChat(); }}
        onFocusSession={focusSession}
        workspaceTabs={workspaceState.tabs}
        activeWorkspaceView={view}
        onFocusChat={backToChat}
        onFocusWorkspaceTab={openWorkspaceTab}
        onCloseWorkspaceTab={closeWorkspace}
        childTabTitle={childTabTitleOf}
        childTabHint={childTabHintOf}
        childParents={childParents}
        activeParent={activeParent}
      />
      <Sidebar
        source={source}
        currentId={currentId}
        busyBySession={busyBySession}
        filter={filter}
        setFilter={setFilter}
        onOpenSettings={() => setSettingsOpen(true)}
        agentsActive={view === "agents"}
        onOpenAgents={openAgents}
        onOpenChat={backToChat}
        catalogActive={view === "catalog"}
        onOpenCatalog={openCatalog}
        gitActive={view === "git"}
        onOpenGit={openGit}
        remoteActive={view === "remote"}
        onOpenRemote={openRemote}
      />
      <WorkspaceViewPanels
        activeView={view}
        openTabs={workspaceState.tabs}
        renderView={renderView}
      />
      <SettingsPanel open={settingsOpen} onClose={() => setSettingsOpen(false)} source={source} />
    </div>
  );

  if (isShell) return app;
  return (
    // 浏览器模式：应用窗口模拟（1440×900 自适应），外层暗底
    <div className="window-stage">
      <div className="app-window">{app}</div>
    </div>
  );
}

function WorkspaceViewPanels({
  activeView,
  openTabs,
  renderView,
}: {
  activeView: WorkspaceView;
  openTabs: WorkspaceTab[];
  renderView: (view: WorkspaceView) => ReactNode;
}) {
  const [displayedView, setDisplayedView] = useState<WorkspaceView>(activeView);
  const panels = useRef(new Map<WorkspaceView, HTMLDivElement>());
  const firstDisplay = useRef(true);

  useLayoutEffect(() => {
    if (displayedView === activeView) return;
    const outgoing = panels.current.get(displayedView);
    if (!outgoing || !motionAllowed()) {
      setDisplayedView(activeView);
      return;
    }
    const context = gsap.context(() => {
      gsap.to(outgoing, {
        opacity: 0,
        y: -5,
        duration: 0.14,
        ease: "power1.in",
        onComplete: () => setDisplayedView(activeView),
      });
    }, outgoing);
    return () => { context.revert(); };
  }, [activeView, displayedView]);

  useLayoutEffect(() => {
    if (firstDisplay.current) {
      firstDisplay.current = false;
      return;
    }
    const incoming = panels.current.get(displayedView);
    if (!incoming || !motionAllowed()) return;
    const context = gsap.context(() => {
      gsap.fromTo(incoming, { opacity: 0, y: 5 }, {
        opacity: 1,
        y: 0,
        duration: 0.18,
        ease: "power2.out",
        clearProps: "transform,opacity",
      });
    }, incoming);
    return () => { context.revert(); };
  }, [displayedView]);

  const renderedPages: WorkspaceTab[] = [...openTabs];
  if (displayedView !== "chat" && !renderedPages.includes(displayedView)) renderedPages.push(displayedView);
  const renderedViews: WorkspaceView[] = ["chat", ...renderedPages];

  // 冻结渲染：隐藏面板复用**上一次渲染的同一元素对象**——React 对引用相同的
  // 元素直接跳过整棵子树的协调。没有这层，每个聊天流式 delta 都会把所有
  // 已打开页面（Agents/拓展/Git/远程访问/子会话）重渲染一遍——「标签页多了
  // 切换就卡」的主因。重新激活时照常渲染新内容（组件状态不丢：同类型同
  // key，只是重渲染）。
  const frozen = useRef(new Map<WorkspaceView, ReactNode>());

  return (
    <main className="main">
      {renderedViews.map((view) => {
        const isActive = displayedView === view;
        // 活动页每次渲染新内容；从未渲染过的页（标签刚开还没轮到显示）也要
        // 先渲染一次，否则挂着空壳。
        if (isActive || !frozen.current.has(view)) {
          frozen.current.set(view, renderView(view));
        }
        return (
          <div
            key={view}
            ref={(element) => {
              if (element) panels.current.set(view, element);
              else {
                panels.current.delete(view);
                frozen.current.delete(view); // 页面关闭：连缓存一起清
              }
            }}
            className={"workspace-view-panel" + (view === "chat" ? " workspace-chat-panel" : "")}
            data-workspace-view={view}
            hidden={displayedView !== view}
          >
            {frozen.current.get(view) ?? null}
          </div>
        );
      })}
    </main>
  );
}
