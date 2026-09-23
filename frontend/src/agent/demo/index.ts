// 演示数据源（M3 叙事）：主 Agent 只调度——思考选人 → agent.dispatch →
// dispatch 卡（子 Agent 全套执行：思考/读码/改码/确认门/跑测试）→ 验收
// 汇总。覆盖 UI 全部状态。事件形状与后端协议 1:1——接线换 WSAgent 即可。
import type { AgentEvent, AgentSource, CompactOutcome, ConfirmRequest, ContextUsage, ProjectInstructions, ProjectMeta, SendOptions, SessionMeta, TodoItem } from "../../shared/types";
import { MAIN_REASONING, SUB_REASONING, SUB_RESULT, MAIN_ANSWER, TODO_INITIAL, TODO_LATER, FILES_CHANGED, SESSIONS } from "./data";

type Listener = (ev: AgentEvent) => void;

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
  private timers: ReturnType<typeof setTimeout>[] = [];
  private confirmCb: ((allow: boolean) => void) | null = null;
  private pendingConfirm: ConfirmRequest | null = null;
  private busy = false;
  private sessions_ = SESSIONS;
  private currentSession = SESSIONS[0].id;
  private pendingNewId: string | null = null;
  private pendingNewWorkspace = "";
  /** 演示态的轮次计数（compact 用：累计过几轮就当作有可压区间）。 */
  private turns = 0;
  private projects_: ProjectMeta[] = [
    { id: "proj-demo-lxcode", name: "lxcode", path: "C:\\Users\\xzy\\Desktop\\my\\lxcode" },
    { id: "proj-demo-agent", name: "local-myt-agent", path: "C:\\Users\\xzy\\Desktop\\gs\\local-myt-agent" },
  ];

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    // 接入即 ready（模拟 connection.ready）
    this.emit({ type: "ready", server: "lxcode", version: "1", busy: this.busy });
    return () => this.listeners.delete(listener);
  }

  send(text: string, _opts?: SendOptions): void {
    if (this.busy) return;
    this.busy = true;
    // 懒建会话：新会话在首条消息时才落进侧栏列表（Codex 惯例——
    // 空会话不占列表位）
    if (this.pendingNewId) {
      const id = this.pendingNewId;
      const ws = this.pendingNewWorkspace;
      this.pendingNewId = null;
      this.pendingNewWorkspace = "";
      this.sessions_ = [
        { id, title: text.length > 24 ? text.slice(0, 24) + "…" : text, updatedAt: "刚刚", messages: 1, workspace: ws },
        ...this.sessions_,
      ];
      this.currentSession = id;
    }
    this.emit({ type: "userMessage", text });
    this.emit({ type: "busy", busy: true });
    this.runTurn();
  }

  async confirm(id: string, allow: boolean): Promise<void> {
    if (this.pendingConfirm?.id !== id) throw new Error("确认请求已失效");
    const cb = this.confirmCb;
    this.pendingConfirm = null;
    this.confirmCb = null;
    cb?.(allow);
  }

  cancel(): void {
    if (!this.busy) return;
    this.clearTimers();
    this.emit({
      type: "error",
      message: "已取消（保留已生成部分）",
      aborted: true,
    });
    this.finish();
  }

  /** 手动压缩（演示）：按真实后端语义回一条 compacted=false 或一条压缩事件。
   *  演示态不真压历史（内存块是 UI 真源），只演示「事件 → 标记块」这条链路。 */
  async compact(): Promise<CompactOutcome> {
    if (this.busy) throw new Error("生成中不能压缩（先停止）");
    // 演示：累计过几轮就当作"有可压区间"
    const rounds = this.turns++;
    if (rounds < 1) return { compacted: false };
    const before = 38_400 + rounds * 900;
    const after = Math.round(before * 0.3);
    this.emit({
      type: "compacted", before, after, shadowed: rounds * 4, manual: true,
      summary: "## 主要请求与意图\n- 演示：压缩早期历史\n\n## 当前工作\n- 演示模式的压缩标记块",
    });
    return { compacted: true, before, after, shadowed: rounds * 4 };
  }

  newSession(workspace?: string): void {
    // 只切到空态 + 记一个待定 id——首条消息时才建列表条目
    const id = "20260911-" + new Date().toTimeString().slice(0, 8).replaceAll(":", "") + "-n" + Math.floor(Math.random() * 90 + 10);
    this.pendingNewId = id;
    this.pendingNewWorkspace = workspace ?? "";
    this.currentSession = id;
    this.emit({ type: "sessionChanged", id, reason: "new" });
  }

  resumeSession(id: string): void {
    this.currentSession = id;
    this.emit({ type: "sessionChanged", id, reason: "resumed" });
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

  private emit(ev: AgentEvent) {
    this.listeners.forEach((l) => l(ev));
  }

  private at(ms: number, fn: () => void) {
    this.timers.push(setTimeout(fn, ms));
  }

  private clearTimers() {
    this.timers.forEach(clearTimeout);
    this.timers = [];
  }

  private finish() {
    this.busy = false;
    this.turns++;
    this.emit({ type: "busy", busy: false });
  }

  private runTurn() {
    // ---- 主 Agent：思考（选人与拟任务）----
    let t = 300;
    MAIN_REASONING.forEach((line) => {
      this.at(t, () => this.emit({ type: "delta", kind: "reasoning", text: line + "\n" }));
      t += 480 + Math.random() * 260;
    });

    // ---- 主 Agent：任务清单（调度视角）----
    this.at(t + 300, () => this.emit({ type: "todoUpdated", items: TODO_INITIAL }));

    // ---- 主 Agent：派发（dispatch 卡开）----
    this.at(t + 900, () => this.emit({ type: "delta", kind: "text", text: "这个任务边界清晰，我派**代码 Agent**去做，稍等。\n\n" }));
    // 事件序与真实后端一致：模型先发工具调用（agent.dispatch），内核再开
    // 子上下文。store 对 agent.dispatch 不建工具行（卡才是它的渲染形态）
    // ——这里照发，保证 demo 复现真实链路的事件序（重复渲染类回归可测）。
    this.at(t + 1500, () => {
      this.emit({
        type: "toolCall", id: "d1", name: "agent.dispatch",
        arguments: JSON.stringify({ agent: "coder", task: "给 internal/agent 的工具循环加 per-tool 120s 超时兜底" }),
      });
    });
    this.at(t + 1600, () => {
      this.emit({
        type: "dispatchStart", dispatchId: "d1", agentId: "coder", agentName: "代码 Agent",
        agentColor: "#3b82f6",
        task: "给 internal/agent 的工具循环加 per-tool 120s 超时兜底（超时回填错误不中断整轮；bash 超时逻辑不动）。验收：新增回归用例 + 全量测试绿。",
      });
    });

    // ---- 子 Agent：思考（挂卡内）----
    let s = t + 2800;
    SUB_REASONING.forEach((line) => {
      this.at(s, () => this.emit({ type: "delta", kind: "reasoning", text: line + "\n", dispatchId: "d1" }));
      s += 460 + Math.random() * 240;
    });

    // ---- 子 Agent：读代码（低危自动）----
    this.at(s + 300, () => {
      this.emit({ type: "toolCall", dispatchId: "d1", id: "d-c1", name: "read_file", arguments: JSON.stringify({ path: "internal/agent/session.go", offset: 296, limit: 40 }) });
    });
    this.at(s + 1300, () => {
      this.emit({
        type: "toolResult", dispatchId: "d1", id: "d-c1", name: "read_file", isError: false,
        content: "296→// runTools 执行本轮工具调用（高危先确认）；返回 false 表示被取消。\n297→func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n…（共 548 行，已显示 296-335 行）",
      });
    });

    // ---- 子 Agent：改代码（edit，低危自动——diff 呈现）----
    this.at(s + 2300, () => {
      this.emit({
        type: "toolCall", dispatchId: "d1", id: "d-c2", name: "edit",
        arguments: JSON.stringify({
          path: "internal/agent/session.go",
          old_string: "func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n\tfor _, tc := range calls {\n\t\tif ctx.Err() != nil {\n\t\t\treturn false\n\t\t}",
          new_string: "func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n\tfor _, tc := range calls {\n\t\tif ctx.Err() != nil {\n\t\t\treturn false\n\t\t}\n\t\t// per-tool 超时兜底：单工具卡死不让整轮挂住（tools 层超时不动，这层管循环）\n\t\ttoolCtx, cancel := context.WithTimeout(ctx, 120*time.Second)\n\t\tdefer cancel()",
        }),
      });
    });
    this.at(s + 3400, () => {
      this.emit({ type: "toolResult", dispatchId: "d1", id: "d-c2", name: "edit", isError: false, content: "已替换 internal/agent/session.go（1 处唯一匹配）" });
    });

    // ---- 子 Agent：跑测试（高危 → 确认门，带 dispatchId 归属卡内）----
    // 注意：confirmRequest 之前必须先发配对的 toolCall——真实后端就是这样
    //（streamRound 先发 toolCall，runTools 的确认门再发 confirmRequest）。
    // 早期 demo 只为 d-c3 发 confirmRequest，掩盖了「确认卡与工具行同 id 并存」
    // 导致的重复行（批准后一条永远停在"执行中…"），故此处还原真实顺序。
    this.at(s + 4000, () => {
      this.emit({ type: "toolCall", dispatchId: "d1", id: "d-c3", name: "bash", arguments: JSON.stringify({ command: "go test ./internal/agent/ -count=1" }) });
    });
    this.at(s + 4400, () => {
      const req: ConfirmRequest = {
        id: "d-c3",
        name: "bash",
        arguments: JSON.stringify({ command: "go test ./internal/agent/ -count=1" }),
        prompt: "将执行命令: go test ./internal/agent/ -count=1",
        dispatch_id: "d1",
      };
      this.pendingConfirm = req;
      this.emit({ type: "confirmRequest", request: req });
      // 等用户裁决（confirm 回调里续播）；演示模式不设自动超时。
      // 结果**延迟**发出：真实后端是 ack 返回 → 工具真跑（bash 起进程）→ 才发
      // toolResult，所以正常顺序是「卡先定格成工具行、结果随后回填」。同步 emit
      // 会把顺序倒过来（结果早于 ack），那是竞态而非正常路径。
      this.confirmCb = (allow) => {
        this.at(500, () => {
          if (allow) {
            this.emit({ type: "toolResult", dispatchId: "d1", id: "d-c3", name: "bash", isError: false, content: "ok  github.com/moyunteng/lxcode/internal/agent\t2.081s\nPASS" });
            this.finishDispatch(true);
          } else {
            this.emit({
              type: "toolResult", dispatchId: "d1", id: "d-c3", name: "bash", isError: true,
              content: "用户拒绝执行。改用读测试源码核对的方式验证。",
            });
            this.finishDispatch(false);
          }
        });
      };
    });
  }

  /** dispatch 收尾 → 主 Agent 验收汇总（allow = 测试是否真跑了）。 */
  private finishDispatch(allow: boolean) {
    // 子 Agent 最终回复 + dispatchEnd（结果回填）
    this.at(600, () => this.emit({ type: "delta", kind: "text", text: allow ? "全量绿了，没有回归。" : "按源码核对，改动路径正确。", dispatchId: "d1" }));
    this.at(1400, () => {
      this.emit({ type: "done", usageTokens: 730, finishReason: "stop", dispatchId: "d1" });
      this.emit({
        type: "dispatchEnd", dispatchId: "d1", isError: false, usageTokens: 730,
        result: allow ? SUB_RESULT : "已完成（源码核对版）：改动与回归用例如上；测试未执行——用户拒绝了 bash，需要时可以说一声我再跑。",
      });
      // 真实后端在子上下文收尾后还会回填一条工具结果（id = 主轮的
      // agent.dispatch 调用 id）。store 对 agent.dispatch 不建工具行，
      // 这条结果会被安全丢弃（卡的结果来自 dispatchEnd）——照发以保证
      // demo 与真实链路的事件序完全一致。
      this.emit({
        type: "toolResult", id: "d1", name: "agent.dispatch", isError: false,
        content: allow ? SUB_RESULT : "已完成（源码核对版）",
      });
      this.emit({ type: "todoUpdated", items: TODO_LATER });
    });

    // ---- 主 Agent：验收汇总 ----
    this.at(2600, () => this.emit({ type: "delta", kind: "text", text: "代码 Agent 完成了，我核对过结果：\n\n" }));
    let t = 3200;
    MAIN_ANSWER.forEach((p) => {
      this.at(t, () => this.emit({ type: "delta", kind: "text", text: (p === "" ? "\n" : p) + "\n" }));
      t += 240 + p.length * 6;
    });
    // 产物汇总（子 Agent 的改动——验收视图）+ 轮完成
    this.at(t + 300, () => {
      this.emit({ type: "filesChanged", files: FILES_CHANGED });
    });
    this.at(t + 800, () => {
      const usage = 2545 + Math.floor(Math.random() * 400);
      this.emit({ type: "done", usageTokens: usage, finishReason: "stop", context: demoContext(38_400 + usage) });
      this.finish();
    });
  }
}
