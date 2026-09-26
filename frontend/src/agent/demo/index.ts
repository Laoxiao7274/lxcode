// 演示数据源（M3 叙事）：主 Agent 只调度——思考选人 → agent_dispatch →
// dispatch 卡（子 Agent 全套执行：思考/读码/改码/确认门/跑测试）→ 验收
// 汇总。覆盖 UI 全部状态。事件形状与后端协议 1:1——接线换 WSAgent 即可。
import type { AgentEvent, AgentSource, CompactOutcome, ConfirmRequest, ContextUsage, ProjectInstructions, ProjectMeta, SendOptions, SessionMeta, TodoItem } from "../../shared/types";
import { MAIN_REASONING, SUB_REASONING, SUB_RESULT, MAIN_ANSWER, TODO_INITIAL, TODO_LATER, FILES_CHANGED, SESSIONS } from "./data";

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
  const toolResults = Math.round(used * 0.34);
  const reasoning = Math.round(used * 0.06);
  return { used, window, system, tool_results: toolResults, reasoning, messages: used - system - toolResults - reasoning };
}

export class DemoAgent implements AgentSource {
  label = "演示模式";
  private listeners = new Set<Listener>();
  private timers = new Map<string, ReturnType<typeof setTimeout>[]>();
  private confirmCallbacks = new Map<string, (allow: boolean) => void>();
  private pendingConfirms = new Map<string, ConfirmRequest>();
  private busySessions = new Set<string>();
  private sessions_ = SESSIONS;
  private currentSession = SESSIONS[0].id;
  private pendingNew = new Map<string, string>();
  private emittingSession = "";
  /** 演示态每个会话独立计轮（compact 用：累计过几轮就当作有可压区间）。 */
  private turns = new Map<string, number>();
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

  send(sessionId: string, text: string, _opts?: SendOptions): void {
    if (!sessionId || !text.trim() || this.busySessions.has(sessionId)) return;
    this.busySessions.add(sessionId);
    if (this.pendingNew.has(sessionId)) {
      const ws = this.pendingNew.get(sessionId) ?? "";
      this.pendingNew.delete(sessionId);
      this.sessions_ = [
        { id: sessionId, title: text.length > 24 ? text.slice(0, 24) + "…" : text, updatedAt: "刚刚", messages: 1, workspace: ws },
        ...this.sessions_,
      ];
      this.emit({ type: "sessionsChanged" });
    }
    this.emit({ type: "userMessage", sessionId, text });
    this.emit({ type: "busy", sessionId, busy: true });
    this.runTurn(sessionId);
  }

  async confirm(sessionId: string, id: string, allow: boolean): Promise<void> {
    if (this.pendingConfirms.get(sessionId)?.id !== id) throw new Error("确认请求已失效");
    const cb = this.confirmCallbacks.get(sessionId);
    this.pendingConfirms.delete(sessionId);
    this.confirmCallbacks.delete(sessionId);
    cb?.(allow);
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

  archiveSession(id: string): void {
    this.sessions_ = this.sessions_.map((s) => (s.id === id ? { ...s, archived: true } : s));
    if (this.currentSession === id) this.newSession();
    this.emit({ type: "sessionsChanged" });
  }

  unarchiveSession(id: string): void {
    this.sessions_ = this.sessions_.map((s) => (s.id === id ? { ...s, archived: false } : s));
    this.emit({ type: "sessionsChanged" });
  }

  sessions(): SessionMeta[] {
    return this.sessions_;
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
      this.emit({
        type: "dispatchStart", dispatchId: "d1", agentId: "coder", agentName: "代码 Agent",
        agentColor: "#3b82f6",
        task: "给 internal/agent 的工具循环加 per-tool 120s 超时兜底（超时回填错误不中断整轮；bash 超时逻辑不动）。验收：新增回归用例 + 全量测试绿。",
      });
    });

    // ---- 子 Agent：思考（挂卡内）----
    let s = t + 2800;
    SUB_REASONING.forEach((line) => {
      this.at(sessionId, s, () => this.emit({ type: "delta", kind: "reasoning", text: line + "\n", dispatchId: "d1" }));
      s += 460 + Math.random() * 240;
    });

    // ---- 子 Agent：读代码（低危自动）----
    this.at(sessionId, s + 300, () => {
      this.emit({ type: "toolCall", dispatchId: "d1", id: "d-c1", name: "read_file", arguments: JSON.stringify({ path: "internal/agent/session.go", offset: 296, limit: 40 }) });
    });
    this.at(sessionId, s + 1300, () => {
      this.emit({
        type: "toolResult", dispatchId: "d1", id: "d-c1", name: "read_file", isError: false,
        content: "296→// runTools 执行本轮工具调用（高危先确认）；返回 false 表示被取消。\n297→func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n…（共 548 行，已显示 296-335 行）",
      });
    });

    // ---- 子 Agent：改代码（edit，低危自动——diff 呈现）----
    this.at(sessionId, s + 2300, () => {
      this.emit({
        type: "toolCall", dispatchId: "d1", id: "d-c2", name: "edit",
        arguments: JSON.stringify({
          path: "internal/agent/session.go",
          old_string: "func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n\tfor _, tc := range calls {\n\t\tif ctx.Err() != nil {\n\t\t\treturn false\n\t\t}",
          new_string: "func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n\tfor _, tc := range calls {\n\t\tif ctx.Err() != nil {\n\t\t\treturn false\n\t\t}\n\t\t// per-tool 超时兜底：单工具卡死不让整轮挂住（tools 层超时不动，这层管循环）\n\t\ttoolCtx, cancel := context.WithTimeout(ctx, 120*time.Second)\n\t\tdefer cancel()",
        }),
      });
    });
    this.at(sessionId, s + 3400, () => {
      this.emit({ type: "toolResult", dispatchId: "d1", id: "d-c2", name: "edit", isError: false, content: "已替换 internal/agent/session.go（1 处唯一匹配）" });
    });

    // ---- 子 Agent：跑测试（高危 → 确认门，带 dispatchId 归属卡内）----
    // 注意：confirmRequest 之前必须先发配对的 toolCall——真实后端就是这样
    //（streamRound 先发 toolCall，runTools 的确认门再发 confirmRequest）。
    // 早期 demo 只为 d-c3 发 confirmRequest，掩盖了「确认卡与工具行同 id 并存」
    // 导致的重复行（批准后一条永远停在"执行中…"），故此处还原真实顺序。
    this.at(sessionId, s + 4000, () => {
      this.emit({ type: "toolCall", dispatchId: "d1", id: "d-c3", name: "bash", arguments: JSON.stringify({ command: "go test ./internal/agent/ -count=1" }) });
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
            this.emit({ type: "toolResult", dispatchId: "d1", id: "d-c3", name: "bash", isError: false, content: "ok  github.com/moyunteng/lxcode/internal/agent\t2.081s\nPASS" });
            this.finishDispatch(sessionId, true);
          } else {
            this.emit({
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
    // 子 Agent 最终回复 + dispatchEnd（结果回填）
    this.at(sessionId, 600, () => this.emit({ type: "delta", kind: "text", text: allow ? "全量绿了，没有回归。" : "按源码核对，改动路径正确。", dispatchId: "d1" }));
    this.at(sessionId, 1400, () => {
      this.emit({ type: "done", usageTokens: 730, finishReason: "stop", dispatchId: "d1" });
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
      const usage = 2545 + Math.floor(Math.random() * 400);
      this.emit({ type: "done", usageTokens: usage, finishReason: "stop", context: demoContext(38_400 + usage) });
      this.finish(sessionId);
    });
  }
}
