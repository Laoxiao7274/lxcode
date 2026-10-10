// 演示数据源（M3 叙事）：主 Agent 只调度——思考选人 → agent_dispatch →
// dispatch 卡（子 Agent 全套执行：思考/读码/改码/确认门/跑测试）→ 验收
// 汇总。覆盖 UI 全部状态。事件形状与后端协议 1:1——接线换 WSAgent 即可。
import type { AgentEvent, AgentSource, ApprovalMode, ArchiveOutcome, CompactOutcome, ConfirmRequest, ContextUsage, GitOverview, HistoryMessage, HistorySnapshot, JobAdminSource, JobInfo, JobLogResult, ProjectInstructions, ProjectMeta, RewindOutcome, SendOptions, SessionMeta, SessionStats, TodoItem } from "../../shared/types";
import { JOB_NOTICE_PREFIX, sortJobs, upsertJob } from "../../shared/jobs";
import { normalizeApproval } from "../../shared/approval";
import { MAIN_REASONING, SUB_REASONING, SUB_RESULT, MAIN_ANSWER, TODO_INITIAL, TODO_LATER, FILES_CHANGED, SESSIONS } from "./data";

/** 演示的子会话 id 与它的模型（子 Agent = 独立会话，AGENTS.md §2.3）。
 *  卡上的子会话 id 徽标、子会话标签页、childHistory 三处共用这一个常量——
 *  各写一份字面量的代价是标签页打开的是另一个 id（读不到历史）。 */
const DEMO_CHILD_SESSION = "demo-child-d1";
const DEMO_CHILD_MODEL = "deepseek-chat";
/** 派给子 Agent 的任务说明书（= 子会话历史里的第一条 user 消息）。 */
const SUB_TASK = "给 internal/agent 的工具循环加 per-tool 120s 超时兜底（超时回填错误不中断整轮；bash 超时逻辑不动）。验收：新增回归用例 + 全量测试绿。";

/** 演示的后台任务输出（逐行追加——模拟 go test 的进度）。 */
const DEMO_JOB_LINES = [
  "?   \tgithub.com/moyunteng/lxcode/internal/jobs\t[no test files]",
  "ok  \tgithub.com/moyunteng/lxcode/internal/protocol\t0.412s",
  "ok  \tgithub.com/moyunteng/lxcode/internal/store\t3.882s",
  "ok  \tgithub.com/moyunteng/lxcode/internal/agent\t4.106s",
  "ok  \tgithub.com/moyunteng/lxcode/internal/server\t19.204s",
];

type Listener = (ev: AgentEvent) => void;
type SessionScopedEvent = Extract<AgentEvent, { sessionId: string }>;
type DemoEvent = AgentEvent | {
  [E in SessionScopedEvent as E["type"]]: Omit<E, "sessionId">;
}[SessionScopedEvent["type"]];

/** 演示用的上下文占用（与真实后端同形状：used 优先真实用量，分类是估算拆分
 *  且之和 == used）。演示不接模型注册表，窗口按 128k 假定。 */
function demoContext(used: number): ContextUsage {
  const window = 128_000;
  const system = Math.round(used * 0.22);
  const tools = Math.round(used * 0.05);
  const toolResults = Math.round(used * 0.34);
  const reasoning = Math.round(used * 0.06);
  return { used, window, system, tools, tool_results: toolResults, reasoning, messages: used - system - tools - toolResults - reasoning };
}

/** 演示用的整段会话统计累加（与真后端同一形状：后端读库折叠整段日志，这里按轮
 *  累加内存里的演示事实）。
 *
 *  为什么演示也必须发它：**演示模式要能全量跑 UI**（AGENTS.md §2 前端一栏）——
 *  真后端有统计胶囊而演示没有的话，演示模式就跑不出这个界面，改动它时也没有
 *  可视的验收面。数字本身是演示事实（与 FILES_CHANGED 那些一样），不假装精确。 */
function demoStats(prev: SessionStats | undefined, usage: number, llmMs: number, toolMs: number, ttftMs: number): SessionStats {
  const base: SessionStats = prev ?? {
    turns: 0, steps: 0, llm_ms: 0, tool_ms: 0, ttft_ms: 0, ttft_steps: 0,
    decode_ms: 0, decode_tokens: 0, input_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 0,
  };
  // 输入侧按输出量的固定倍数假造（演示不接 provider，没有真实分桶）：缓存读占大头，
  // 这样「缓存命中」这一栏在演示里也有意义
  const input = Math.round(usage * 1.2);
  const cacheRead = Math.round(input * 0.72);
  return {
    turns: base.turns + 1,
    steps: base.steps + 3,
    llm_ms: base.llm_ms + llmMs,
    tool_ms: base.tool_ms + toolMs,
    ttft_ms: base.ttft_ms + ttftMs,
    ttft_steps: base.ttft_steps + 3,
    decode_ms: base.decode_ms + Math.max(0, llmMs - ttftMs),
    decode_tokens: base.decode_tokens + usage,
    input_tokens: base.input_tokens + input - cacheRead,
    cache_read_tokens: base.cache_read_tokens + cacheRead,
    cache_write_tokens: base.cache_write_tokens,
    output_tokens: base.output_tokens + usage,
  };
}

export class DemoAgent implements AgentSource, JobAdminSource {
  label = "演示模式";
  readonly jobAdmin: JobAdminSource = this;
  private listeners = new Set<Listener>();
  private timers = new Map<string, ReturnType<typeof setTimeout>[]>();
  private confirmCallbacks = new Map<string, (allow: boolean) => void>();
  private pendingConfirms = new Map<string, ConfirmRequest>();
  private busySessions = new Set<string>();
  private sessions_ = SESSIONS;
  private currentSession = SESSIONS[0].id;
  private pendingNew = new Map<string, string>();
  /** 演示态每会话的权限档（真后端持在会话上；这里只记「用户改过什么」）。 */
  private approvals_ = new Map<string, ApprovalMode>();
  private emittingSession = "";
  /** 子会话的消息（演示态的「子会话历史」，见 childEmit / childHistory）。 */
  private childMsgs_: HistoryMessage[] = [];
  /** 演示态每个会话独立计轮（compact 用：累计过几轮就当作有可压区间）。 */
  private turns = new Map<string, number>();
  /** 演示态的整段会话统计（每会话一份——与真后端"整段日志折叠"同形状）。 */
  private stats_ = new Map<string, SessionStats>();
  /** 演示态的 seq 分配器（每会话独立自增）：撤回锚点必须由**产生消息的那一方**
   *  给号，前端不自己编号——真后端也一样（seq 是库里的序号，客户端编不出）。 */
  private seqBySession = new Map<string, number>();
  /** 演示的后台任务（内存态：与真实后端同一形状的 JobInfo 快照）。 */
  private jobs_: JobInfo[] = [];
  /** 每会话当前的后台任务 id（runTurn 起、finishDispatch 收尾——中间隔着
   *  确认门，拿不到局部变量）。 */
  private jobBySession_ = new Map<string, string>();
  private jobListeners = new Set<() => void>();
  /** 任务输出（逐行累积——readJobLog 返回「此刻的全量」，与真后端落盘日志同语义）。 */
  private jobOutput_ = new Map<string, string[]>();
  private projects_: ProjectMeta[] = [
    { id: "proj-demo-lxcode", name: "lxcode", path: "C:\\Users\\xzy\\Desktop\\my\\lxcode" },
    { id: "proj-demo-agent", name: "local-myt-agent", path: "C:\\Users\\xzy\\Desktop\\gs\\local-myt-agent" },
  ];

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    this.emit({ type: "ready", server: "lxcode", version: "2", busy: this.busySessions.has(this.currentSession) });
    this.emit({ type: "sessionFocused", id: this.currentSession });
    return () => this.listeners.delete(listener);
  }

  send(sessionId: string, text: string, opts?: SendOptions): void {
    // 附件（图片批次 B）：演示模式没有后端可落盘——附件不进演示流水线，
    // 只在乐观呈现层折算成一行摘要文本（「[图片]×N [文件]×M」），保证
    // 附件选择/预览全链路可玩、发送不炸。
    const hasAtts = Boolean(opts?.images?.length || opts?.files?.length);
    const attsText = !hasAtts ? "" : [
      opts?.images?.length ? `[图片]×${opts.images.length}` : "",
      opts?.files?.length ? `[文件]×${opts.files.length}` : "",
    ].filter(Boolean).join(" ");
    const display = text.trim() || attsText;
    if (!sessionId || !display || this.busySessions.has(sessionId)) return;
    // 演示模式**不需要**乐观用户气泡：下面的 userMessage 是同步 emit 的——
    // 没有可感知的空窗，先插 pending 块反而要靠 FIFO 去重兜一层（真后端的
    // 乐观气泡在 ws/index.ts 的 send 里发）。
    this.busySessions.add(sessionId);
    if (this.pendingNew.has(sessionId)) {
      const ws = this.pendingNew.get(sessionId) ?? "";
      this.pendingNew.delete(sessionId);
      this.sessions_ = [
        { id: sessionId, title: display.length > 24 ? display.slice(0, 24) + "…" : display, updatedAt: "刚刚", messages: 1, workspace: ws },
        ...this.sessions_,
      ];
      this.emit({ type: "sessionsChanged" });
    }
    this.emit({ type: "userMessage", sessionId, text: display, seq: this.nextSeq(sessionId) });
    this.emit({ type: "busy", sessionId, busy: true });
    this.runTurn(sessionId);
  }

  /** 下一条消息的 seq（每会话自增——与真后端的库内序号同语义：只要求同一会话内
   *  唯一且递增，撤回按它锚定）。 */
  private nextSeq(sessionId: string): number {
    const n = (this.seqBySession.get(sessionId) ?? 0) + 1;
    this.seqBySession.set(sessionId, n);
    return n;
  }

  /** 中途改权限档（演示模式没有后端）：本地记下 + 广播同形状的事件。
   *
   *  广播这一步是刻意的——真后端改档会广播给所有端，演示里照做，那条
   *  「广播 → 设置反向同步」的路径才在演示模式里也走得到（无后端也能验 UI）。 */
  async setApproval(mode: ApprovalMode): Promise<void> {
    const sessionId = this.currentSession;
    const approval = normalizeApproval(mode);
    this.approvals_.set(sessionId, approval);
    this.emit({ type: "approvalChanged", sessionId, approval });
  }

  async confirm(sessionId: string, id: string, allow: boolean): Promise<void> {
    if (this.pendingConfirms.get(sessionId)?.id !== id) throw new Error("确认请求已失效");
    const cb = this.confirmCallbacks.get(sessionId);
    this.pendingConfirms.delete(sessionId);
    this.confirmCallbacks.delete(sessionId);
    cb?.(allow);
  }

  /** 回答 ask_user 的提问（演示模式）：演示脚本不产生 ask_user 调用，这里
   *  按「已回答」放行挂起请求——接口形状与真后端一致（Backend 同构），行为
   *  简化（没有文本可回传，演示流水线也消费不到它）。 */
  async answer(sessionId: string, id: string, _text: string): Promise<void> {
    if (this.pendingConfirms.get(sessionId)?.id !== id) throw new Error("提问请求已失效");
    const cb = this.confirmCallbacks.get(sessionId);
    this.pendingConfirms.delete(sessionId);
    this.confirmCallbacks.delete(sessionId);
    cb?.(true);
  }

  cancel(sessionId: string): void {
    if (!this.busySessions.has(sessionId)) return;
    this.clearTimers(sessionId);
    this.pendingConfirms.delete(sessionId);
    this.confirmCallbacks.delete(sessionId);
    this.emit({ type: "error", sessionId, message: "已取消（保留已生成部分）", aborted: true });
    this.finish(sessionId);
  }

  /** 手动压缩（演示）：每会话独立的轮次与压缩事件。 */
  async compact(sessionId: string): Promise<CompactOutcome> {
    if (this.busySessions.has(sessionId)) throw new Error("生成中不能压缩（先停止）");
    const rounds = this.turns.get(sessionId) ?? 0;
    this.turns.set(sessionId, rounds + 1);
    if (rounds < 1) return { compacted: false };
    const before = 38_400 + rounds * 900;
    const after = Math.round(before * 0.3);
    this.emit({
      type: "compacted", sessionId, before, after, shadowed: rounds * 4, manual: true,
      summary: "## 主要请求与意图\n- 演示：压缩早期历史\n\n## 当前工作\n- 演示模式的压缩标记块",
    });
    return { compacted: true, before, after, shadowed: rounds * 4 };
  }

  /** 会话回退（演示模式没有后端）：本地截断 + 广播同形状事件——UI 走的是与真实
   *  链路**完全同一条**归约路径（发 rewound → 归约器 planRewind 截断）。
   *
   *  演示不落库，所以「后端对齐」退化成"内存里本来就只有这一份时间线"，没有
   *  失败路径可对齐（真后端那份对齐见 agent/ws/index.ts 的 rewind）。 */
  async rewind(sessionId: string, seq: number): Promise<RewindOutcome> {
    this.emit({ type: "rewound", sessionId, seq, removed: 0 });
    return { removed: 0 };
  }

  async newSession(workspace?: string): Promise<string> {
    const id = "20260911-" + new Date().toTimeString().slice(0, 8).replaceAll(":", "") + "-n" + Math.floor(Math.random() * 90 + 10);
    this.pendingNew.set(id, workspace ?? "");
    this.currentSession = id;
    this.emit({ type: "sessionFocused", id });
    return id;
  }

  async releaseWorktree(_id: string): Promise<void> {
    // 演示源没有真实文件系统；只提供与 live 模式一致的能力接口。
  }

  /** git.overview（演示）：简化假数据——保持 Git 管理页在无后端时全量可看，
   *  不假装是真实仓库状态（路径/分支名用演示项目的占位值）。 */
  async gitOverview(projectId?: string): Promise<GitOverview> {
    const project = this.projects_.find((p) => p.id === projectId) ?? this.projects_[0];
    if (!project) throw new Error("演示模式没有可展示的项目");
    return {
      path: project.path,
      branch: "main",
      dirty: [
        { path: "internal/agent/session.go", kind: "modified" },
        { path: "docs/demo-notes.md", kind: "untracked" },
      ],
      branches: [
        { name: "main", current: true, ahead: 0, behind: 0 },
        {
          name: `lxcode/session-${SESSIONS[0]?.id ?? "demo"}`, current: false, ahead: 2, behind: 0,
          session_id: SESSIONS[0]?.id, session_title: SESSIONS[0]?.title,
          has_worktree: true, worktree_path: `${project.path}\\worktrees\\demo`, dirty_count: 1,
        },
        { name: "lxcode/session-demo-archived", current: false, ahead: 1, behind: 0, session_id: "demo-archived", session_title: "已归档的演示会话", archived: true, merged: true },
      ],
      commits: [
        { hash: "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c", message: "演示：接入 Git 管理页真实数据", author: "demo", when: new Date().toISOString() },
        { hash: "1029384756afbccddeeff0011223344556677889", message: "演示：子会话标签页实时流", author: "demo", when: new Date(Date.now() - 86_400_000).toISOString() },
      ],
    };
  }

  /** git.diff（演示）：返回一小段演示 diff 文本，渲染链路照常可走。 */
  async gitDiff(_projectId: string, path: string): Promise<string> {
    return `+++ b/${path}\n@@ -1,2 +1,3 @@\n-演示模式没有真实仓库\n+演示 diff：这是演示数据，不是真实文件内容\n+渲染链路与真实模式同一条\n`;
  }

  /** 起合并进程（演示）：没有后端可发起——回一个演示任务 id，UI 链路照常可走
   *  （入口可用、按钮有反馈），但不假造任何后端事件。 */
  async mergeRequest(_sessionId: string, _targetBranch?: string): Promise<string> {
    return "demo-merge";
  }

  async resumeSession(id: string): Promise<void> {
    this.currentSession = id;
    this.emit({ type: "sessionFocused", id });
  }

  renameSession(id: string, title: string): void {
    const t = title.trim();
    if (!t) return;
    this.sessions_ = this.sessions_.map((s) => (s.id === id ? { ...s, title: t, updatedAt: "刚刚" } : s));
    this.emit({ type: "sessionsChanged" });
  }

  archiveSession(id: string, _releaseWorktree = false): Promise<ArchiveOutcome> {
    this.sessions_ = this.sessions_.map((s) => (s.id === id ? { ...s, archived: true } : s));
    if (this.currentSession === id) this.newSession();
    this.emit({ type: "sessionsChanged" });
    // 演示源没有真实文件系统：只回答能力接口（归档成功、没有可释放的工作区）。
    return Promise.resolve({ archived: true, releasedWorktree: false, releaseError: "" });
  }

  unarchiveSession(id: string): void {
    this.sessions_ = this.sessions_.map((s) => (s.id === id ? { ...s, archived: false } : s));
    this.emit({ type: "sessionsChanged" });
  }

  sessions(): SessionMeta[] {
    return this.sessions_;
  }

  /** 读子会话历史（演示态：由**录制的事件序**重建，见 childEmit）。
   *
   *  子 Agent = 独立会话（AGENTS.md §2.3）：真实后端把它的 messages 落进库，
   *  演示数据源不落库——所以派发过程一边发事件一边记进 childMsgs_，这里按同一个
   *  子会话 id 返回。经 historyBlocks 映射后与真实链路的回放**共用一份映射**。
   *
   *  别的 id（不存在/老调用方）返回**空快照**，不编内容；也不抛错：演示模式里
   *  根本没有那个子会话，报错会把"演示没有后端"说成"读取失败"。 */
  async childHistory(sessionId: string): Promise<HistorySnapshot> {
    const snapshot: HistorySnapshot = sessionId !== DEMO_CHILD_SESSION
      ? { sessionId, messages: [], busy: false, pending: null, todos: [] }
      : {
        sessionId,
        messages: this.childMsgs_,
        busy: false,
        // 挂起确认挂在**父会话**（确认门代理，AGENTS.md §2.3）——子会话自己没有 pending，
        // 所以这里如实报 null；子会话标签页里那张待裁决卡来自实时事件（store 的双投）。
        pending: null,
        todos: [],
        model: DEMO_CHILD_MODEL,
        // 子会话**自己的**两份读数（与真链路的 chat.history{子会话} 同契约）：
        // 真后端按子会话自己的 id 折叠它的日志、并把子会话自己的 context 挂上
        //（session_ops.go 的 history / emit.go 的 sessionStatsOf）。
        // 缺席时子会话页会显示中性态（"—" / 不渲染统计胶囊）——演示要能跑出这个界面。
        context: demoContext(9_800),
        stats: this.stats_.get(sessionId) ?? demoStats(undefined, 730, 2_400, 1_100, 380),
      };
    // 与 WSAgent.childHistory **同一份契约**：发 historyLoaded（sessionId 是子会话自己的），
    // 让 store 把子会话自己的 state 建起来——子会话标签页要看到实时流，就得先有它自己的
    // state（历史是基线、随后的事件是增量）。漏了这一条的后果在真链路上就是用户报的
    //「点开之后他里面就没有接着思考」；在演示态就是标签页永远显示"没有可显示的历史"。
    this.emit({ type: "historyLoaded", sessionId, history: snapshot });
    return snapshot;
  }

  /** 子会话消息的录制（演示态的「子会话历史」）。
   *
   *  录的时机就是事件发出的时机——记的是"子 Agent 真产出过什么"，不预先编一份：
   *    delta(reasoning/text) → 续写当前 assistant 消息（reasoning_content / content）；
   *    toolCall             → 新起一条带 tool_calls 的 assistant 消息；
   *    toolResult           → 一条 tool 消息（按 tool_call_id 配对）。
   *  其余事件（confirmRequest / done）不是消息，只发不记。 */
  private childEmit(ev: DemoEvent) {
    const last = this.childMsgs_[this.childMsgs_.length - 1];
    if (ev.type === "delta") {
      if (last && last.role === "assistant" && !last.tool_calls) {
        if (ev.kind === "text") last.content += ev.text;
        else last.reasoning_content = (last.reasoning_content ?? "") + ev.text;
      } else {
        this.childMsgs_.push({
          role: "assistant",
          content: ev.kind === "text" ? ev.text : "",
          reasoning_content: ev.kind === "reasoning" ? ev.text : "",
        });
      }
    } else if (ev.type === "toolCall") {
      this.childMsgs_.push({
        role: "assistant", content: "",
        tool_calls: [{ id: ev.id, function: { name: ev.name, arguments: ev.arguments } }],
      });
    } else if (ev.type === "toolResult") {
      this.childMsgs_.push({ role: "tool", tool_call_id: ev.id, content: ev.content });
    }
    this.emit(ev);
  }

  projects(): ProjectMeta[] {
    return this.projects_;
  }

  /** 演示模式的项目守则（内存态：项目 id → 内容）。 */
  private instructions_ = new Map<string, string>();

  addProject(name: string, path: string): void {
    // 演示模式：本地数组操作（浏览器样式可验；不真碰文件系统/git）
    this.projects_ = [
      { id: "proj-" + Math.random().toString(36).slice(2, 8), name, path },
      ...this.projects_,
    ];
    this.emit({ type: "projectsChanged" });
  }

  /** 读项目守则（演示模式：内存态——可编辑可保存，刷新即失）。 */
  async readInstructions(projectId: string): Promise<ProjectInstructions> {
    const p = this.projects_.find((x) => x.id === projectId);
    const path = p ? `${p.path}\\AGENTS.md` : "AGENTS.md";
    const content = this.instructions_.get(projectId) ?? "";
    return { path, content, exists: content !== "" };
  }

  /** 写项目守则（演示模式：内存态）。 */
  async saveInstructions(projectId: string, content: string): Promise<void> {
    this.instructions_.set(projectId, content);
  }

  get currentId(): string {
    return this.currentSession;
  }

  // ---- JobAdminSource（后台任务——演示也要能全量跑 UI） ----

  jobs(): JobInfo[] {
    return this.jobs_;
  }

  onJobsChanged(listener: () => void): () => void {
    this.jobListeners.add(listener);
    return () => this.jobListeners.delete(listener);
  }

  async listJobs(sessionId?: string): Promise<JobInfo[]> {
    return sessionId ? this.jobs_.filter((j) => j.session_id === sessionId) : this.jobs_;
  }

  /** 用户点「结束」：与真后端同语义（Manager.Kill(id, EndedUser)）——
   *  置 killed/ended_by=user 并广播 settled，唤醒语义由后端负责（演示不唤醒）。 */
  async killJob(id: string): Promise<JobInfo> {
    const job = this.jobs_.find((j) => j.id === id);
    if (!job) throw new Error("任务不存在（可能已结束）");
    return this.settleDemoJob(id, "killed", "user", "用户请求结束");
  }

  /** 读全量输出（演示：内存里累积到此刻的行）。 */
  async readJobLog(id: string): Promise<JobLogResult> {
    const job = this.jobs_.find((j) => j.id === id);
    if (!job) throw new Error("任务不存在（可能已结束）");
    return { data: (this.jobOutput_.get(id) ?? []).join("\n"), truncated: false };
  }

  /** 起一个演示后台任务（runTurn 里调用——UI 的「运行中卡片 + 顶栏角标」由此点亮）。 */
  private startDemoJob(sessionId: string): string {
    const id = "job-demo-" + Math.random().toString(36).slice(2, 7);
    this.jobOutput_.set(id, []);
    const job: JobInfo = {
      id,
      kind: "bash",
      label: "go test ./... -count=1",
      status: "running",
      ended_by: "",
      detail: "",
      session_id: sessionId,
      // 演示没有派发（没有子会话），时间线归属就是当前会话
      owner_session_id: sessionId,
      started_at: new Date().toISOString(),
      finished_at: "",
      output_tail: "",
      output_path: `sessions/jobs/${id}.log`,
    };
    this.applyJob(job);
    this.emit({ type: "jobStarted", sessionId, job });
    return id;
  }

  /** 追加一行输出（时间线卡片与面板都不轮询——点「查看输出」拉此刻的全量）。 */
  private appendDemoJobOutput(id: string, line: string) {
    const lines = this.jobOutput_.get(id);
    if (!lines) return;
    lines.push(line);
  }

  /** 收尾（定时收尾与用户点「结束」共用一条路径——与后端一致）。
   *  **已结束的任务不再被改写**：契约里 Settle 重复调用是 no-op，用户先点了
   *  「结束」之后脚本的定时收尾不能把它改回「正常结束」（否则「你停的」会
   *  在几秒后自己变成「正常结束」，用户会以为按钮没生效）。 */
  private settleDemoJob(id: string, status: JobInfo["status"], by: JobInfo["ended_by"], detail: string): JobInfo {
    const job = this.jobs_.find((j) => j.id === id);
    if (!job) throw new Error("任务不存在（可能已结束）");
    if (job.status !== "running" && job.status !== "stopping") return job;
    const lines = this.jobOutput_.get(id) ?? [];
    const next: JobInfo = {
      ...job,
      status,
      ended_by: by,
      detail,
      finished_at: new Date().toISOString(),
      output_tail: lines.join("\n"),
    };
    this.applyJob(next);
    this.emit({ type: "jobSettled", sessionId: next.session_id, job: next });
    return next;
  }

  /** 任务快照 → 缓存 + 通知（与 WSAgent 的 applyJob 同款：面板订阅这份缓存）。 */
  private applyJob(job: JobInfo) {
    this.jobs_ = sortJobs(upsertJob(this.jobs_, job));
    this.jobListeners.forEach((l) => l());
  }

  // ---- 编排（M3：主 Agent 调度叙事） ----

  private emit(ev: DemoEvent) {
    const global = ["ready", "sessionFocused", "operationError", "sessionChanged", "sessionsChanged", "projectsChanged"].includes(ev.type);
    const scoped = "sessionId" in ev || !this.emittingSession || global
      ? ev
      : { ...ev, sessionId: this.emittingSession };
    this.listeners.forEach((l) => l(scoped as unknown as AgentEvent));
  }

  private at(sessionId: string, ms: number, fn: () => void) {
    const timers = this.timers.get(sessionId) ?? [];
    const timer = setTimeout(() => {
      this.timers.set(sessionId, (this.timers.get(sessionId) ?? []).filter((item) => item !== timer));
      const previous = this.emittingSession;
      this.emittingSession = sessionId;
      try { fn(); } finally { this.emittingSession = previous; }
    }, ms);
    timers.push(timer);
    this.timers.set(sessionId, timers);
  }

  private clearTimers(sessionId: string) {
    for (const timer of this.timers.get(sessionId) ?? []) clearTimeout(timer);
    this.timers.delete(sessionId);
  }

  private finish(sessionId: string) {
    this.busySessions.delete(sessionId);
    this.turns.set(sessionId, (this.turns.get(sessionId) ?? 0) + 1);
    this.emit({ type: "busy", sessionId, busy: false });
  }

  private runTurn(sessionId: string) {
    // ---- 主 Agent：思考（选人与拟任务）----
    let t = 300;
    MAIN_REASONING.forEach((line) => {
      this.at(sessionId, t, () => this.emit({ type: "delta", kind: "reasoning", text: line + "\n" }));
      t += 480 + Math.random() * 260;
    });

    // ---- 主 Agent：任务清单（调度视角）----
    this.at(sessionId, t + 300, () => this.emit({ type: "todoUpdated", items: TODO_INITIAL }));

    // ---- 主 Agent：派发（dispatch 卡开）----
    this.at(sessionId, t + 900, () => this.emit({ type: "delta", kind: "text", text: "这个任务边界清晰，我派**代码 Agent**去做，稍等。\n\n" }));
    // 事件序与真实后端一致：模型先发工具调用（agent_dispatch），内核再开
    // 子上下文。store 对 agent_dispatch 不建工具行（卡才是它的渲染形态）
    // ——这里照发，保证 demo 复现真实链路的事件序（重复渲染类回归可测）。
    this.at(sessionId, t + 1500, () => {
      this.emit({
        type: "toolCall", id: "d1", name: "agent_dispatch",
        arguments: JSON.stringify({ agent: "coder", task: "给 internal/agent 的工具循环加 per-tool 120s 超时兜底" }),
      });
    });
    this.at(sessionId, t + 1600, () => {
      // 子会话的任务说明书 = 它历史里的第一条 **user** 消息（真实后端同样如此：
      // openChildSession 把任务作为 history[0] 投进去）。先记再发——childHistory
      // 打开标签页时要能读到它。
      this.childMsgs_ = [{ role: "user", content: SUB_TASK }];
      this.emit({
        type: "dispatchStart", dispatchId: "d1", childSessionId: DEMO_CHILD_SESSION,
        agentId: "coder", agentName: "代码 Agent",
        agentColor: "#3b82f6",
        task: SUB_TASK,
      });
    });

    // ---- 后台任务（jobs）：长命令走 run_in_background，不占这一轮 ----
    // 起任务挂在时间轴上（与真实链路一致：工具返回 job id 是在那一轮的中段，
    // 不是轮一开始就凭空多出一个任务），输出随进程推进逐行增长——点「查看输出」
    // 能拉到此刻的全量。用户点「结束」也能提前收尾。
    this.at(sessionId, t + 2000, () => {
      const id = this.startDemoJob(sessionId);
      this.jobBySession_.set(sessionId, id);
      let jt = 400;
      for (const line of DEMO_JOB_LINES) {
        this.at(sessionId, jt, () => this.appendDemoJobOutput(id, line));
        jt += 320 + Math.random() * 260;
      }
    });

    // ---- 子 Agent：思考（进子会话自己的时间线；卡里只有状态摘要）----
    let s = t + 2800;
    SUB_REASONING.forEach((line) => {
      this.at(sessionId, s, () => this.childEmit({ type: "delta", kind: "reasoning", text: line + "\n", dispatchId: "d1" }));
      s += 460 + Math.random() * 240;
    });

    // ---- 子 Agent：读代码（低危自动）----
    this.at(sessionId, s + 300, () => {
      this.childEmit({ type: "toolCall", dispatchId: "d1", id: "d-c1", name: "read_file", arguments: JSON.stringify({ path: "internal/agent/session.go", offset: 296, limit: 40 }) });
    });
    this.at(sessionId, s + 1300, () => {
      this.childEmit({
        type: "toolResult", dispatchId: "d1", id: "d-c1", name: "read_file", isError: false,
        content: "296→// runTools 执行本轮工具调用（高危先确认）；返回 false 表示被取消。\n297→func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n…（共 548 行，已显示 296-335 行）",
      });
    });

    // ---- 子 Agent：改代码（edit，低危自动——diff 呈现）----
    this.at(sessionId, s + 2300, () => {
      this.childEmit({
        type: "toolCall", dispatchId: "d1", id: "d-c2", name: "edit",
        arguments: JSON.stringify({
          path: "internal/agent/session.go",
          old_string: "func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n\tfor _, tc := range calls {\n\t\tif ctx.Err() != nil {\n\t\t\treturn false\n\t\t}",
          new_string: "func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n\tfor _, tc := range calls {\n\t\tif ctx.Err() != nil {\n\t\t\treturn false\n\t\t}\n\t\t// per-tool 超时兜底：单工具卡死不让整轮挂住（tools 层超时不动，这层管循环）\n\t\ttoolCtx, cancel := context.WithTimeout(ctx, 120*time.Second)\n\t\tdefer cancel()",
        }),
      });
    });
    this.at(sessionId, s + 3400, () => {
      this.childEmit({ type: "toolResult", dispatchId: "d1", id: "d-c2", name: "edit", isError: false, content: "已替换 internal/agent/session.go（1 处唯一匹配）" });
    });

    // ---- 子 Agent：跑测试（高危 → 确认门，带 dispatchId 归属）----
    // 注意：confirmRequest 之前必须先发配对的 toolCall——真实后端就是这样
    //（streamRound 先发 toolCall，runTools 的确认门再发 confirmRequest）。
    // 早期 demo 只为 d-c3 发 confirmRequest，掩盖了「确认卡与工具行同 id 并存」
    // 导致的重复行（批准后一条永远停在"执行中…"），故此处还原真实顺序。
    this.at(sessionId, s + 4000, () => {
      this.childEmit({ type: "toolCall", dispatchId: "d1", id: "d-c3", name: "bash", arguments: JSON.stringify({ command: "go test ./internal/agent/ -count=1" }) });
    });
    this.at(sessionId, s + 4400, () => {
      const req: ConfirmRequest = {
        id: "d-c3",
        name: "bash",
        arguments: JSON.stringify({ command: "go test ./internal/agent/ -count=1" }),
        prompt: "将执行命令: go test ./internal/agent/ -count=1",
        dispatch_id: "d1",
      };
      this.pendingConfirms.set(sessionId, req);
      this.emit({ type: "confirmRequest", request: req });
      // 等用户裁决（confirm 回调里续播）；演示模式不设自动超时。
      // 结果**延迟**发出：真实后端是 ack 返回 → 工具真跑（bash 起进程）→ 才发
      // toolResult，所以正常顺序是「卡先定格成工具行、结果随后回填」。同步 emit
      // 会把顺序倒过来（结果早于 ack），那是竞态而非正常路径。
      this.confirmCallbacks.set(sessionId, (allow) => {
        this.at(sessionId, 500, () => {
          if (allow) {
            this.childEmit({ type: "toolResult", dispatchId: "d1", id: "d-c3", name: "bash", isError: false, content: "ok  github.com/moyunteng/lxcode/internal/agent\t2.081s\nPASS" });
            this.finishDispatch(sessionId, true);
          } else {
            this.childEmit({
              type: "toolResult", dispatchId: "d1", id: "d-c3", name: "bash", isError: true,
              content: "用户拒绝执行。改用读测试源码核对的方式验证。",
            });
            this.finishDispatch(sessionId, false);
          }
        });
      });
    });
  }

  /** dispatch 收尾 → 主 Agent 验收汇总（allow = 测试是否真跑了）。 */
  private finishDispatch(sessionId: string, allow: boolean) {
    // 子 Agent 最终回复（进子会话自己的时间线）+ dispatchEnd（卡上的结果回填）
    this.at(sessionId, 600, () => this.childEmit({ type: "delta", kind: "text", text: allow ? "全量绿了，没有回归。" : "按源码核对，改动路径正确。", dispatchId: "d1" }));
    this.at(sessionId, 1400, () => {
      // 子会话**自己的**统计 + 占用：真后端在子会话 done 时按它自己的 session_id 折叠
      // 它的日志、并把子会话自己的 context 挂上（emit.go / turn.go），演示按同一契约在
      // 它自己的 id 上记一份——子会话标签页的「会话统计」与上下文环读的就是这一份。
      const childStats = demoStats(this.stats_.get(DEMO_CHILD_SESSION), 730, 2_400, 1_100, 380);
      this.stats_.set(DEMO_CHILD_SESSION, childStats);
      this.childEmit({
        type: "done", usageTokens: 730, finishReason: "stop",
        // dispatchId **必须带**：它是子事件的归属键（store 双投按它分流——带 dispatch_id
        // 才同时进卡与子会话自己的 state；不带就会被当成主会话的事件，把主指示器写脏）
        dispatchId: "d1",
        context: demoContext(9_800),
        stats: childStats,
      });
      this.emit({
        type: "dispatchEnd", dispatchId: "d1", isError: false, usageTokens: 730,
        result: allow ? SUB_RESULT : "已完成（源码核对版）：改动与回归用例如上；测试未执行——用户拒绝了 bash，需要时可以说一声我再跑。",
      });
      // 真实后端在子上下文收尾后还会回填一条工具结果（id = 主轮的
      // agent_dispatch 调用 id）。store 对 agent_dispatch 不建工具行，
      // 这条结果会被安全丢弃（卡的结果来自 dispatchEnd）——照发以保证
      // demo 与真实链路的事件序完全一致。
      this.emit({
        type: "toolResult", id: "d1", name: "agent_dispatch", isError: false,
        content: allow ? SUB_RESULT : "已完成（源码核对版）",
      });
      this.emit({ type: "todoUpdated", items: TODO_LATER });
    });

    // ---- 主 Agent：验收汇总 ----
    this.at(sessionId, 2600, () => this.emit({ type: "delta", kind: "text", text: "代码 Agent 完成了，我核对过结果：\n\n" }));
    let t = 3200;
    MAIN_ANSWER.forEach((p) => {
      this.at(sessionId, t, () => this.emit({ type: "delta", kind: "text", text: (p === "" ? "\n" : p) + "\n" }));
      t += 240 + p.length * 6;
    });
    // 产物汇总（子 Agent 的改动——验收视图）+ 轮完成
    this.at(sessionId, t + 300, () => {
      this.emit({ type: "filesChanged", files: FILES_CHANGED });
    });
    this.at(sessionId, t + 800, () => {
      // 后台任务收尾（与真后端一致：进程退出 → settle → 广播 job.settled）。
      // 用户若已经点过「结束」，这里是 no-op——不会把「你停的」改写成「正常结束」。
      const jobId = this.jobBySession_.get(sessionId);
      if (jobId) {
        const settled = this.settleDemoJob(jobId, "completed", "self", "退出码 0");
        // 唤醒通告：后端在 settle 后把通告作为**真实 user 角色消息**投进归属会话
        //（模型要当作用户回合才能回应），前端按文本前缀渲染成通告条——不是气泡。
        // 措辞对齐契约 §5：EndedBy=self → 结束通告；EndedBy=user → 「不要重启」。
        this.emit({
          type: "userMessage",
          sessionId,
          // 通告在历史里是**真实 user 角色消息**（模型要当用户回合才能回应），
          // 所以它同样占一个 seq——撤回一条更早的消息会把它一起带走。
          seq: this.nextSeq(sessionId),
          text: JOB_NOTICE_PREFIX + (settled.ended_by === "user"
            ? `用户主动结束了后台任务 ${settled.label}。这不是失败，不要重启它；等用户指示。`
            : `后台任务 ${settled.label} 结束（退出码 0）。用 job_output 读输出。`),
        });
      }
      const usage = 2545 + Math.floor(Math.random() * 400);
      // 整段统计按轮累加（演示事实——与真后端的「读库折叠整段日志」同形状）
      const stats = demoStats(this.stats_.get(sessionId), usage, 3_200, 1_100, 420);
      this.stats_.set(sessionId, stats);
      this.emit({ type: "done", usageTokens: usage, finishReason: "stop", context: demoContext(38_400 + usage), stats });
      this.finish(sessionId);
    });
  }
}
