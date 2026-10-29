import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";import { gsap } from "gsap";
import { motionAllowed } from "./shared/motion";
import { LightboxHost } from "./shared/lightbox";
import {
  childTabSession,
  childTabTitle,
  focusHistoryView,
  parentTabChildView,
  type WorkspaceTab,
  type WorkspaceView,
} from "./shared/workspace-tabs";
import { getAgentSource } from "./agent";
import { useAgent, type ThreadBlock } from "./shared/store";
import { timelineOf } from "./shared/blocks";
import { draftOf } from "./shared/session-drafts";
import { visionBlockNotice } from "./shared/attachments";
import { AgentsProvider, useAgents } from "./shared/agents";
import { AgentsPage } from "./components/agents/AgentsPage";
import { CatalogPage } from "./components/catalog/CatalogPage";
import { GitWorkbenchPage } from "./components/git/GitWorkbenchPage";
import { RemoteAccessPage } from "./components/remote/RemoteAccessPage";
import { Topbar } from "./components/topbar";
import { Sidebar, LOOSE } from "./components/sidebar";
import { Thread } from "./components/thread";
import { Composer } from "./components/composer";
import type { SlashCommand } from "./components/composer/SlashPalette";
import { TabBar } from "./components/topbar/TabBar";
import { SettingsPanel } from "./components/settings";
import { Button } from "./components/form";
// 右栏面板：轮次树（TurnPanel）取代了原先并排的 OutlinePanel + SubAgentPanel——
// 那两个组件与它们的测试仍在（回退用），只是不再挂载。
import { TurnPanel } from "./components/panels/TurnPanel";
import { SessionTreePanel } from "./components/panels/SessionTreePanel";
import { RewindConfirmDialog, RewindConflictDialog } from "./components/panels/RewindDialogs";
import { latestSeq, truncationNote, type TreeRow } from "./components/panels/tree-model";
import type { RewindPlanFacts } from "./components/panels/rewind-confirm";
import { ChildSessionPage } from "./components/panels/ChildSessionPage";
import { SettingsProvider, useSettings } from "./shared/settings";
import { ConnectionsProvider } from "./shared/connections";
import { UpdateProvider } from "./shared/update";
import { SearchAdminProvider } from "./shared/search-admin";
import { JobsProvider } from "./shared/jobs-admin";
import { useConfirmFlow } from "./app-hooks/use-confirm-flow";
import { useWorkspaceNav } from "./app-hooks/use-workspace-nav";
import { useTurnOutline } from "./app-hooks/use-turn-outline";
import { useSessionTree } from "./app-hooks/use-session-tree";
import { useSendQueue } from "./app-hooks/use-send-queue";
import { useRewind } from "./app-hooks/use-rewind";
import { useCompaction } from "./app-hooks/use-compaction";
import { getSakuraBridge } from "./agent/sakura";
import { getTailscaleBridge } from "./agent/tailscale";
import { getRemoteControlBridge } from "./agent/host";
import { UpdateDialog } from "./components/update/UpdateDialog";
import type { AgentSource, SendAttachments, SendOptions, SessionMeta } from "./shared/types";

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
                <UpdateDialog />
                {/* 全局灯箱宿主：单实例（openRef 收口），附件缩略图统一入口 */}
                <LightboxHost />
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
  // 怎么发：请求级参数 effort/agent 都在 sendWithOptions 里补齐——approval
  // 不上车（会话级状态，见 sendToSession 的注释）。
  // atts = 队首条目携带的附件（图片批次 B——按引用透传）。
  const autoSendBridgeRef = useRef<(sessionId: string, text: string, atts?: SendAttachments) => void>(() => {});
  /** 发送失败的附件回滚（store 钩子 → Composer 的 attsDraft）：文本丢了附件不能丢。
   *  per-session 记账（drafts）之下它只服务「正看着的那个会话」——失败发生在别的
   *  会话时只落记账键（切回去由初始草稿还原），不装给挂载中的 Composer。 */
  /** 当前会话 id 的 ref：草稿记账的写入点要落在**正确的会话**上——失败回填可能
   *  在用户已切到别的会话之后到达（ws 异步），按 currentId 闭包写会串会话。
   *  初值在下方 useAgent 之后同步（state.currentId）——openProject 的幂等判定
   *  同样读这份 ref（见 currentIdRef 镜像注释）。 */
  const currentIdRef = useRef("");
  const onSendFailedBridgeRef = useRef<(sessionId: string, atts: SendAttachments) => void>(() => {});
  /** 右侧「已发送消息」大纲的开合：**纯 UI 状态**（不进会话历史、不落库、切会话不
   *  清——它只是当前窗口的查看方式）。 */
  const [outlineOpen, setOutlineOpen] = useState(true);
  const { state, sessionStates, childParents, sendQueues, send, resolve, clearError, reportError, enqueueSend, removeQueuedSend, moveQueuedToFront } = useAgent(source, {
    onAutoSend: (sessionId, text, atts) => autoSendBridgeRef.current(sessionId, text, atts),
    onSendFailed: (sessionId, atts) => onSendFailedBridgeRef.current(sessionId, atts),
  });
  // 传输层连接态（WSAgent 的 connectionChanged 事件）：顶栏脉冲点的真实数据源。
  // 旁路订阅（useAgent 的主订阅不归约它——那是会话域状态，这是连接域状态）。
  const [wsConnected, setWsConnected] = useState(false);
  useEffect(() => source.subscribe((ev) => {
    if (ev.type === "connectionChanged") setWsConnected(ev.connected);
  }), [source]);
  const { settings, providers } = useSettings();
  // agents：子会话临时视图的标题要按 agentId 回落显示名（回放块的 agentName 是空串）
  const { agents, activeAgentId } = useAgents();
  // 各会话 state 的 ref：子会话裁决要按 dispatch_id 反查持有挂起确认的那个会话，
  // 而回调身份必须稳定（Block 是 memo 的，每次新建回调会逐帧击穿它）——所以读 ref
  // 而不是把 sessionStates 放进 useCallback 的依赖里。
  const sessionStatesRef = useRef(sessionStates);
  sessionStatesRef.current = sessionStates;
  // currentId 的 ref 镜像（与上方草稿记账用的 currentIdRef 是同一个 ref）：
  // openProject（点击项目切换会话）要读**最新**的当前会话——useCallback 依赖
  // currentId 会把回调身份绑到每次切会话，击穿 Sidebar memo（同款问题）。
  currentIdRef.current = state.currentId;
  // ---- 工作区导航（use-workspace-nav：页签状态 + 真实浏览器历史 + 页签/视图回调）----
  const currentId = state.currentId;

  // ---- 会话切换的「装载中」态（防闪烁）----
  // 焦点已切到新会话（currentId 变了）但它的历史还没装载（historyReady=false，
  // blocks 是空数组）：这一帧如果照常渲染，Thread 会画空态（EmptyState「我们做
  // 点什么？」）——先看到旧内容瞬间消失、再看到空态、再被真历史顶掉，就是用户
  // 报的「切换瞬间闪两下」（ws.test.mjs 的帧序注释实测过 +20ms 空态出现 +48ms
  // 内容回来）。装载完成前保留旧内容 + loading 指示（CSS 淡化），装载后一次性
  // 替换——Thread 拿到的还是 state（空 blocks），只是外层在装载期不做视觉替换。
  // 快照未经装载（historyReady=false）时 blocks 恒为空，旧内容在那一帧其实已经
  // 没了（state 已切到新会话）——所以这层保护真正兜的是「渲染什么」：空态换成
  // 保留上次 DOM 的指示条。previousSessionRef 记录上一帧焦点；首轮渲染
  //（previous=""）不算切换。 */
  // 逻辑时间线（已定型块 + 正在流式的那一块）：**只给顺序敏感的派生计算**用
  // （大纲/轮次/标题反查/撤回原文）。渲染路径不拼它——Thread / ChildSessionPage
  // 分两段挂（已定型块 + 流式块），避免每帧 O(块数) 的数组复制。
  // 记忆化：流式期间 state.blocks 引用不变，只有 state.streaming 换对象——拼一次
  // 数组的代价因此按「流式块变化」而不是按帧计（每帧仍会新建一个短数组，
  // 但不再影响渲染路径；见下面的 useMemo 依赖）。
  const timeline = useMemo(() => timelineOf(state), [state.blocks, state.streaming]);
  const [switching, setSwitching] = useState(false);
  const prevFocusedRef = useRef(currentId);
  useEffect(() => {
    if (prevFocusedRef.current === currentId) return;
    prevFocusedRef.current = currentId;
    setSwitching(!state.historyReady && state.blocks.length === 0);
  }); // 每次渲染检查（state 变化本身不进依赖——ref 已挡住重复处理）
  useEffect(() => {
    if (switching && state.historyReady) setSwitching(false);
  }, [switching, state.historyReady]);

  const {
    view, workspaceState, settingsOpen, setSettingsOpen, filter, setFilter, gitProjectId, setGitProjectId,
    closeWorkspace, openWorkspaceTab, openChildView, openAgents, openCatalog, openGit, openRemote, backToChat, focusSession, openProject, openSettings,
  } = useWorkspaceNav({ source, currentId });

  // live 写失败桥（AgentsProvider 的乐观更新 WS 调用失败 → 一次性提示）
  useEffect(() => {
    const on = (e: Event) => reportError(String((e as CustomEvent<string>).detail ?? ""));
    window.addEventListener("lx-operation-error", on);
    return () => window.removeEventListener("lx-operation-error", on);
  }, [reportError]);
  // busy 汇总的稳定性（2026-12 渲染性能 Top1）：sessionStates 每条 delta 都换新
  // 对象，直接以它为依赖会每帧产出新 busyBySession → 击穿 Sidebar/TabBar 的 memo。
  // 这里以「每会话 busy 位的签名」为依赖：busy 位没变的帧返回同一份对象。
  // busyInit：会话列表携带的「此刻在跑」快照（session.list 的 running 位 →
  // sessionsChanged.running）——sessionStates 只覆盖**打开过的**会话，冷启动时
  // 正在生成的会话没 state，busy 位永远缺席，列表运行指示要点进会话才亮
  //（安卓端同一问题的桌面版）。事件到达时记下这份初始投影。
  const [busyInit, setBusyInit] = useState<Record<string, boolean>>({});
  useEffect(() => source.subscribe((ev) => {
    if (ev.type === "sessionsChanged" && ev.running) {
      setBusyInit(Object.fromEntries(ev.running.map((id) => [id, true])));
    }
  }), [source]);
  const busySignature = useMemo(
    () => Object.entries(sessionStates).map(([id, s]) => `${id}:${s.busy ? 1 : 0}`).join("|"),
    [sessionStates],
  );
  const busyBySession = useMemo(
    () => Object.fromEntries(Object.entries(sessionStates).map(([id, session]) => [id, session.busy])),
    // 依赖是 busy 签名（上一行）：签名不变就不重建（签名本身已含全部 busy 位）
    [busySignature], // eslint-disable-line react-hooks/exhaustive-deps
  );
  // 合并 busyInit：state 里打开过的会话以实时 busy 位为准（busy=false 的 state
  // 是后端 done/busy 事件收敛后的**事实**，快照是旧值），没 state 的回落快照。
  const busyBySessionMerged = useMemo(
    () => ({ ...busyInit, ...busyBySession }),
    [busyInit, busyBySession],
  );

  // 子会话**临时视图**活跃时（childTabSession(view) 非 null）：TabBar 侧主会话标签
  // 进**视图态**（↳ 子标题 + 描边角标，childView），顶栏标题按这个子会话算（viewTitle）。
  // 正在看子会话临时视图时 currentId 仍是父会话（focusChildView 不改 currentId）。
  const activeChildId = childTabSession(view);

  // 「合并请求」入口已随合并进程退役摘除（2026-10 架构修订：会话直连主检出，
  // 没有分支合并流程）。mergeRequest 的 source 方法与 JobsPanel 的 MergeRow 组件
  // 保留（协议方法在，后端如实回说明），但面板不再渲染这个入口。
  const projects = source.projects();
  useEffect(() => {
    if (view === "git" && !gitProjectId && projects.length > 0) setGitProjectId(projects[0].id);
  }, [view, gitProjectId, projects]);

  // 当前范围的项目名（「未分组」→ 空态不标——无归属不需要声明）
  const filterProjectName =
    filter === LOOSE ? undefined : projects.find((p) => p.id === filter)?.name;

  // 发送时携带请求级参数：effort 只在当前模型声明推理能力时上帧（后端
  // 能力门控会丢弃不匹配档位，不带上帧更诚实）；agent = 当前选用的 Agent
  //（主 Agent 也显式携带——后端按名单语境跑）。**approval 不上车**：它是
  // 后端持久化的会话级状态（chat.approval 改 / 新会话按 behavior 默认档
  // 初始化），请求级恒带会静默覆盖会话档且不广播。
  // atts = 随消息的附件（图片/文件，图片批次 B）：按引用进 opts，不复制 base64。
  // sendToSession：目标会话显式传入——缓冲区的自动发送可能由「当前会话」的
  // busy 翻转触发，但触发瞬间 currentId 与目标一致，显式传参不依赖这个巧合。
  // ---- 会话级模型（模型选择器的作用域 = 当前会话）----
  // 选中态取当前会话的 state.model（chat.history 的 model 键 / chat.modelChanged
  // 广播），**不是**全局 default 角色——后者是「新会话的默认模型」（设置页管）。
  // 没拿到（刚建会话/老后端）才回落全局设置里的值，与改动前的显示一致。
  const sessionModel = state.model || settings.model;
  const pickSessionModel = useCallback((id: string) => {
    // 切模型走会话级方法（chat.model：落库 + 广播，只改这一条会话）。
    // 选中态不在这里乐观改：后端成功才广播 chat.modelChanged，归约层按它对齐。
    source.setSessionModel(id).catch((e) => reportError(`切换模型失败: ${e instanceof Error ? e.message : String(e)}`));
  }, [source, reportError]);

  const sendToSession = useCallback((sessionId: string, text: string, atts?: SendAttachments) => {
    // 推理强度的能力门控按**这条会话的**模型判定（会话级选择优先，回落全局 default）：
    // 拿全局模型判会把 effort 发给一个不支持它的端点（400），或反过来丢掉档位。
    const current = providers.flatMap((p) => p.models).find((m) => m.id === sessionModel);
    const opts: SendOptions = { agent: activeAgentId };
    if (current && current.efforts.length > 0) opts.effort = settings.effort;
    if (atts?.images.length) opts.images = atts.images;
    if (atts?.files.length) opts.files = atts.files;
    if (sessionId) send(sessionId, text, opts);
  }, [providers, sessionModel, settings.effort, send, activeAgentId]);
  const sendWithOptions = useCallback((text: string, atts?: SendAttachments) => sendToSession(currentId, text, atts), [sendToSession, currentId]);
  // 桥接（渲染期同步赋值——store 的订阅闭包经 ref 读到最新身份）
  autoSendBridgeRef.current = sendToSession;

  // ---- 确认门/提问裁决流（use-confirm-flow）----
  const { handleConfirm, handleAnswer, handleChildConfirm, handleChildAnswer } = useConfirmFlow({
    source, resolve, reportError, currentId, sessionStatesRef,
  });

  // ---- 手动压缩（use-compaction）----
  // reloadHistory：压缩成功后重拉历史（检查点卡落位 + 旧气泡出时间线 + 上下文环
  // 由历史应答的 context 再对齐一次——与 chat.compacted 事件自带的 context 双保险）。
  const reloadCurrentHistory = useCallback(
    (sessionId: string) => source.reloadHistory ? source.reloadHistory(sessionId) : Promise.resolve(),
    [source],
  );
  const { handleCompact, handleCompactClick, compacting } = useCompaction({ source, currentId, reportError, reloadHistory: reloadCurrentHistory });

  // ---- 用户气泡的编辑/撤回 + 草稿域（use-rewind）----
  const {
    attsDraft, draft, drafts, editDraft, injectDraft,
    handleDraftChange, onSendFailed, syncBlocks,
    handleRewind, handleEdit, handleCancelEdit, handleSend,
  } = useRewind({ source, currentId, currentIdRef, reportError, sendWithOptions });
  // 发送失败的附件回滚（store 钩子 → 草稿域）：桥接（渲染期同步赋值——store 的
  // 订阅闭包经 ref 读到最新身份）
  onSendFailedBridgeRef.current = onSendFailed;
  // 最新时间线的引用（撤回要按锚点取原文）——保持与原实现相同的渲染期同步时机。
  // 用**逻辑时间线**（含正在流式的那一块）：撤回锚点恒是 user 块（在 blocks 里），
  // 但「最新时间线」的语义应当与用户看到的一致（流式块也在屏幕上）。
  syncBlocks(timeline);

  // ---- 发送缓冲区条目动作（use-send-queue）----
  const { queueNotice, handleQueueEdit, handleQueueDelete, handleQueueSend, currentQueue } = useSendQueue({
    sendQueues, currentId, removeQueuedSend, moveQueuedToFront, injectDraft, sendWithOptions,
    busy: state.busy, sending: state.sending,
  });


  // ---- vision 可用性感知（图片批次 B，尽力而为）----
  // 判定链见 shared/attachments.ts 的 visionBlockNotice（纯函数，测试直测同一份）：
  // 当前会话**实际**模型（state.model——chat.history 的 Model 字段，与后端
  // sendImages 的 ModelFor 同一条解析路径的产物）优先，拿不到回落全局设置的模型。
  const visionBlocked = useMemo(
    () => visionBlockNotice(state.model || settings.model, providers.flatMap((p) => p.models)),
    [state.model, settings.model, providers],
  );

  // ---- 右侧「轮次」大纲面板（use-turn-outline：分组 + 高亮 + 跳转目标）----
  const { groups, dispatchCount, jumpedUid, activeUid, activeSeq, seqToUid, handleOutlineJump } = useTurnOutline({ blocks: timeline });

  // ---- 右侧「会话树」面板（use-session-tree：chat.tree + 过滤/折叠 + 切叶）----
  //
  // 老后端没有 chat.tree → `tree === null` → 面板回落 TurnPanel（线性轮次视图，
  // 不报错、不空屏：AGENTS.md §5 坑 11「老后端 + 新前端不许炸」）。
  const sessionTree = useSessionTree({ source, sessionId: currentId });
  const treeTruncation = useMemo(() => truncationNote(sessionTree.tree), [sessionTree.tree]);
  /** 「回到最新」的落点 = 树里最大的 seq（**不是 0**——0 是空路径，会把用户送到第一条
   *  消息之前）。已经在最新处时按钮禁用（点了没有变化，不做成点了没反应）。 */
  const latestSeqInTree = useMemo(() => latestSeq(sessionTree.tree), [sessionTree.tree]);
  const atLatest = sessionTree.tree !== null && sessionTree.tree.leafSeq >= latestSeqInTree;
  /** 待确认的「回到这里 + 还原文件」（null = 没有待确认的动作）。
   *  facts 在**点击那一刻**算好并定格：确认框里显示的数字必须与随后执行的那一次
   *  请求一致（树可能在这期间被别的事件刷新——让弹窗跟着变会让用户看到"数字跳了"）。 */
  const [rewindPlan, setRewindPlan] = useState<{ row: TreeRow; facts: RewindPlanFacts } | null>(null);
  /** 冲突清单（fail-closed 的展示；null = 没有）。 */
  const [rewindConflicts, setRewindConflicts] = useState<{ conflicts: import("./shared/types").FileConflict[]; row: TreeRow } | null>(null);

  /** 树行的单击：**跳转**（在别的分支上则先切过去再跳——纯滚动跳转在别分支上是
   *  静默 no-op，比报错更坏，见 docs §D.5）。
   *
   *  跳转锚点只能落在 user 块上（只有它带 seq 的块有 data-uid）：行的 userSeq 就是
   *  它最近的 user 祖先。seqToUid 查不到 = 该块不在当前时间线里（在别的分支上）——
   *  先 setLeaf 切过去，等历史重载后再跳（这里用轮询式的重试一次：历史重载是异步的）。 */
  /** 切过去之后要跳的那条 seq（等历史重载、块真的挂上时间线再跳）。
   *
   *  为什么不用"等一会儿再查一次"：历史重载是异步的，超时长短都是猜的——猜短了跳不到
   *  （用户点了一个废弃分支，切过去了却没跳，看起来像"只切了一半"），猜长了白等。
   *  记下意图、由下面的 effect 在 seqToUid 真的出现时兑现，是唯一确定的做法。 */
  const pendingJumpSeqRef = useRef<number | null>(null);
  /** 最新一份 seqToUid：切分支是异步的，兑现跳转时必须读**重载之后**的映射
   *  （闭包里那份是切换之前的——废弃分支的块在旧时间线里根本不存在）。 */
  const seqToUidRef = useRef(seqToUid);
  seqToUidRef.current = seqToUid;
  const handleTreeJump = useCallback((row: TreeRow) => {
    if (row.onPath) {
      const uid = seqToUid.get(row.userSeq);
      if (uid !== undefined) {
        handleOutlineJump(uid);
        return;
      }
      // 在路径上却没有对应的块：可能是被窗口化渲染挡在外面/历史还没重载——只切不跳
      // 是静默 no-op，所以这里只报一句实话（不编一个"跳了"）。
      reportError("这条消息还没加载到时间线里（可能正在切换会话）");
      return;
    }
    // 废弃分支：**先切过去再跳**（纯滚动跳转在别分支上是静默 no-op——那个块在当前
    // 时间线里根本不存在，比报错更坏）。判据用 onPath 而不是「uid 查不到」：废弃分支
    // 上的行可能回落到一个**共享祖先**的 user 块（分支首节点是 notice 时），那时 uid
    // 查得到但跳过去等于没切分支——用户点的是"去那条分支"，不是"去它的祖先"。
    pendingJumpSeqRef.current = row.userSeq;
    void sessionTree.setLeaf(row.seq).then(() => {
      // 历史已重载：能立刻兑现就兑现（seqToUid 可能还没在 effect 里刷新）。
      const uid = seqToUidRef.current.get(row.userSeq);
      if (uid !== undefined) {
        pendingJumpSeqRef.current = null;
        handleOutlineJump(uid);
      }
    }).catch((e: unknown) => {
      pendingJumpSeqRef.current = null;
      reportError(`切到这条分支失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }, [seqToUid, handleOutlineJump, reportError, sessionTree]);

  // 待兑现的跳转：切分支之后历史重载完，块的 uid 才出现在 seqToUid 里。
  useEffect(() => {
    const seq = pendingJumpSeqRef.current;
    if (seq === null) return;
    const uid = seqToUid.get(seq);
    if (uid === undefined) return;
    pendingJumpSeqRef.current = null;
    handleOutlineJump(uid);
  }, [seqToUid, handleOutlineJump]);

  /** 「切到此」（不还原文件）：该节点留在路径上。 */
  const handleTreeSetLeaf = useCallback((row: TreeRow, files: boolean) => {
    if (files) {
      // 带文件是**破坏性**动作（磁盘 IO + fail-closed 整批拒绝）→ 先二次确认。
      // 数字来自 TreeRow.rollback*（**这一行之后**的改动量）：后端还原的是
      // `owner_seq > seq` 那一段，目标行自己的改动属于「目标状态」，不该被还原
      // （把本行徽标当成"将还原 X 个文件"会让确认框多出一轮）。
      setRewindPlan({
        row,
        facts: {
          ...(row.turn ? { turn: row.turn } : {}),
          files: row.rollbackFiles,
          ...(row.rollbackChildFiles ? { childFiles: row.rollbackChildFiles } : {}),
          external: row.rollbackExternal,
        },
      });
      return;
    }
    void sessionTree.setLeaf(row.seq).catch((e: unknown) => {
      reportError(`切换分支失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }, [sessionTree, reportError]);

  /** 二次确认之后的真正执行（切叶 + 还原文件）。冲突时弹清单、不静默覆盖。 */
  const runTreeSetLeafWithFiles = useCallback((row: TreeRow) => {
    setRewindPlan(null);
    void sessionTree.setLeaf(row.seq, true).then((r) => {
      const conflicts = r.files?.conflicts ?? [];
      if (conflicts.length > 0) setRewindConflicts({ conflicts, row });
    }).catch((e: unknown) => {
      reportError(`还原文件失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }, [sessionTree, reportError]);

  /** 「从此分支」：把该 user 节点自己移出路径 + 原文回填输入框（= 旧的「编辑后重发」）。
   *
   *  为什么走 rewind 而不是 setLeaf（docs §D.5，最容易做错的一处）：setLeaf(seq) 让
   *  该节点**留在**路径上，用户随后重发就会出现两条一模一样的用户消息（一条旧的、
   *  一条新的，同一个父亲）——那是明显的 bug。rewind(seq) 内部就是 setLeaf(parent(seq))，
   *  正是「我要把这条改一下重发」。
   *
   *  复用 handleRewind：它已经负责乐观截断 + 原文乐观回填 + 失败回滚（同一份逻辑，
   *  不另写一遍）。这里只需按 seq 找到对应的块。 */
  const handleTreeBranchFrom = useCallback((row: TreeRow) => {
    const uid = seqToUid.get(row.userSeq);
    const block = uid === undefined ? undefined : timeline.find((b) => b.uid === uid);
    if (!block) {
      reportError("这条消息还没加载到时间线里（可能正在切换会话）");
      return;
    }
    handleRewind(block);
  }, [seqToUid, timeline, handleRewind, reportError]);

  /** 「回到最新」：落点 = 树里最大的 seq（绝不传 0）。 */
  const handleTreeLatest = useCallback(() => {
    if (latestSeqInTree <= 0) return;
    void sessionTree.setLeaf(latestSeqInTree).catch((e: unknown) => {
      reportError(`回到最新失败: ${e instanceof Error ? e.message : String(e)}`);
    });
  }, [latestSeqInTree, sessionTree, reportError]);

  const currentTitle = timeline.length === 0 ? "" : source.sessions().find((s: SessionMeta) => s.id === currentId)?.title ?? "任务";

  /** 子会话临时视图的标题：Agent 名 + 任务摘要（如 `researcher · 通读 internal/agent`）。
   *
   *  数据来自**父会话主时间线里那张 dispatch 块**（childTabTitle 纯函数按 sessionId
   *  命中）。父 = 子→父映射查到的（dispatchStart 的 owner_session_id / 历史回放
   *  各记了一份）；映射缺失时回落 currentId——视图刚打开、父历史还没回放的窗口期。
   *  反查必须用父的 blocks：传当前活跃会话的 blocks 的话，切到别的会话后反查失败，
   *  顶栏标题会回落成「子会话 <id 前 8 位>」（本批修掉的既有缺陷）。
   *  回放块的 agentName 是空串（展示名是 Agent 注册表的知识），所以按 agentId 回落——
   *  与 DispatchCard / TurnPanel 同款。找不到块时 childTabTitle 自己兜底
   *  「子会话 <id 前 8 位>」，**不许空白**。 */
  const childTabTitleOf = useCallback((sessionId: string) => {
    const parent = childParents[sessionId] ?? currentId;
    const parentState = sessionStates[parent] ?? state;
    return childTabTitle(timelineOf(parentState), sessionId, (agentId) => agents.find((a) => a.id === agentId)?.name ?? "");
  }, [childParents, currentId, sessionStates, state, agents]);
  // 顶栏标题与临时视图页头身份走**同一个**函数：各算一次早晚分叉。
  // 子会话标题加「子会话 · 」前缀（用户报「子会话和主会话表明的不明显」）：临时视图
  // 有自己的页头与「返回主会话」按钮，而**顶栏只有一行文字**，所以在这里补一个文字前缀。
  // 标题正文仍来自同一个 childTabTitleOf（不各算一次）。
  const viewTitle = activeChildId !== null
    ? `子会话 · ${childTabTitleOf(activeChildId)}`
    : view === "agents" ? "Agent 名单" : view === "catalog" ? "拓展" : view === "git" ? "Git 管理" : view === "remote" ? "远程访问" : currentTitle;

  // TabBar 侧主会话标签的「视图态」信息：临时视图活跃时给父会话标签追加
  // 「↳ 子标题」与描边角标（sessionTabAction 让点击 = 返回主会话）。标题与
  // viewTitle 同源（childTabTitleOf），映射缺失时 parentTabChildView 回落信任
  // currentId（视图刚打开、父历史还没回放的窗口期，见 workspace-tabs.ts）。
  const childView = useMemo(
    () => parentTabChildView(view, currentId, (id) => childParents[id], childTabTitleOf),
    [view, currentId, childParents, childTabTitleOf],
  );

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

  // 压缩进行中状态条（/compact 斜杠命令与弹层按钮共用同一条链路——入口在哪触发
  // 都能看到）。与 errorNotice 同层同形态，走中性色：它不是失败，是「正在做」。
  // 完成后由 compacting 复位摘除（检查点卡由 chat.compacted 事件 + 历史重拉接管呈现）。
  const compactingNotice = compacting && view === "chat" && (
    <div className="op-notice compacting" role="status">
      <span className="mset-spinner" aria-hidden />
      <span className="op-notice-text">正在压缩历史…（要等一次摘要调用，大会话可能要几十秒）</span>
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
            {compactingNotice}
            <div className="thread-scroll" data-switching={switching ? "true" : undefined}>
              <Thread state={state} source={source} onConfirm={handleConfirm} onAnswer={handleAnswer} onSuggestion={handleSend} projectName={filterProjectName} onEdit={handleEdit} onRewind={handleRewind} onOpenChild={openChildView} revealUid={jumpedUid} />
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
              onCompact={handleCompactClick}
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
              model={sessionModel}
              onPickModel={pickSessionModel}
            />
          </div>
          <aside className="outline-aside" data-open={outlineOpen ? "true" : "false"} data-tree={sessionTree.tree !== null ? "true" : "false"}>
            <div className="outline-head">
              {/* 标题与轮次数竖排：窄栏横排放不下「会话树」与「共 7 轮 · 12 次派发」两段字 */}
              {outlineOpen && (
                <div className="turn-head-text">
                  <span className="outline-title">{sessionTree.tree !== null ? "会话树" : "轮次"}</span>
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
            {/* 右栏面板，两条数据源：
             *  ① 有 chat.tree（新后端）→ 会话树（分支结构 + 废弃分支灰化 + 切叶）；
             *  ② 没有（老后端 / 纯内存）→ 回落 TurnPanel 的线性轮次视图（不报错、不空屏）。
             *  跳转复用同一个 handleOutlineJump（扩窗 + 滚 + 高亮都在 Thread 里，面板不碰 DOM）。 */}
            {outlineOpen && sessionTree.tree !== null && (
              <SessionTreePanel
                rows={sessionTree.rows}
                filter={sessionTree.view.filter ?? "all"}
                onFilter={sessionTree.setFilter}
                activeSeq={activeSeq}
                onJump={handleTreeJump}
                onToggle={(row) => sessionTree.toggle(row.seq, row.onPath)}
                onSetLeaf={handleTreeSetLeaf}
                onBranchFrom={handleTreeBranchFrom}
                onLoadAll={() => { void sessionTree.refresh({ loadAll: true }); }}
                loadedAll={sessionTree.loadedAll}
                onLatest={handleTreeLatest}
                canLatest={!atLatest}
                truncation={treeTruncation}
                loading={sessionTree.loading}
                error={sessionTree.error}
                busy={state.busy}
              />
            )}
            {outlineOpen && sessionTree.tree === null && (
              <TurnPanel groups={groups} activeUid={activeUid} onJump={handleOutlineJump} />
            )}
          </aside>
        </div>
      );
    }
    if (activeView === "agents") return <AgentsPage />;
    if (activeView === "catalog") return <CatalogPage />;
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
    if (activeView === "remote") {
      return <RemoteAccessPage sakuraBridge={sakuraBridge} tailscaleBridge={tailscaleBridge} remoteControlBridge={remoteControlBridge} />;
    }
    // 剩下的只可能是子会话**临时视图**（键 child:<sessionId>，不进标签栏——
    // focusChildView 只切 active）：渲染子会话自己的完整时间线。
    // key=childId：不同子会话各自一份实例（切走再回来不串历史；返回后视图卸载，
    // store 里的子会话 state 与实时流缓存不受影响，重新打开由 ChildSessionPage 重新装载）。
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
        connected={wsConnected}
      />
      <TabBar
        source={source}
        currentId={currentId}
        busyBySession={busyBySessionMerged}
        onNewChat={() => { void source.newSession(filter); backToChat(); }}
        onFocusSession={focusSession}
        workspaceTabs={workspaceState.tabs}
        activeWorkspaceView={view}
        childView={childView}
        onFocusChat={backToChat}
        onFocusWorkspaceTab={openWorkspaceTab}
        onCloseWorkspaceTab={closeWorkspace}
      />
      <Sidebar
        source={source}
        currentId={currentId}
        busyBySession={busyBySessionMerged}
        filter={filter}
        setFilter={setFilter}
        onOpenProject={openProject}
        onOpenSettings={openSettings}
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
      {/* 「回到这里 + 还原文件」的二次确认（破坏性动作：磁盘 IO + fail-closed 整批拒绝）。
          数字来自树节点（这一轮的文件/外部操作计数）——文案由 panels/rewind-confirm 的
          纯函数给出（三段骨架固定、只填数字），组件不自己拼。 */}
      {rewindPlan && (
        <RewindConfirmDialog
          facts={rewindPlan.facts}
          onConfirm={() => runTreeSetLeafWithFiles(rewindPlan.row)}
          onCancel={() => setRewindPlan(null)}
        />
      )}
      {/* 冲突清单（fail-closed：整批未执行，对话与文件都没动）——逐文件给路径与原因，
          不静默覆盖用户的手改。 */}
      {rewindConflicts && (
        <RewindConflictDialog
          conflicts={rewindConflicts.conflicts}
          onClose={() => setRewindConflicts(null)}
          onHistoryOnly={() => {
            const row = rewindConflicts.row;
            setRewindConflicts(null);
            // 「只切历史，不动文件」= files:false 重试（失败时的退路，不是默认动作）
            void sessionTree.setLeaf(row.seq).catch((e: unknown) => {
              reportError(`切换分支失败: ${e instanceof Error ? e.message : String(e)}`);
            });
          }}
        />
      )}
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
  // 已打开页面（Agents/拓展/Git/远程访问/子会话临时视图）重渲染一遍——「标签页多了
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
