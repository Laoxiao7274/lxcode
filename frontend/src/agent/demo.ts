// 演示数据源：脚本化编排一轮完整交互，覆盖 UI 全部状态
// （流式正文/思考链、低危工具自动执行、高危确认门两分支、任务清单、
// 完成/取消/错误）。事件形状与后端协议 1:1——接线时换 WSAgent 即可。
import type { AgentEvent, AgentSource, ConfirmRequest, SessionMeta, TodoItem } from "./types";

type Listener = (ev: AgentEvent) => void;

const REASONING = [
  "用户想给工具循环加个超时保护，先看一下现在的 session.go 是怎么跑工具的。",
  "工具循环在 runTools 里串行执行，每次调用用 ctx 控制取消——但单工具卡死会让整轮挂住。",
  "方案：在 Execute 外再包一层 per-tool 超时（默认 120s），超时视为工具错误回填模型，不取消整轮。",
  "还需要一个回归用例：假工具 sleep 超过超时，断言回填的是超时错误而不是死等。",
  "计划清晰了：改 session.go、补测试、跑全量。开始动手。",
];

const ANSWER = [
  "已经加上了：runTools 里每个工具调用现在有独立的 120 秒超时（tools 层的 bash 超时逻辑不动，这层是兜底）。",
  "",
  "改动两处：",
  "1. `internal/agent/session.go` 的 runTools 包了 per-tool ctx，超时回填自解释错误文本给模型，整轮不中断；",
  "2. 新增 `TestToolTimeout` 用假工具验证超时回填路径。",
  "",
  "全量测试 152 通过（新增 2 个），门禁全绿。旧的 bash 工具超时行为不受影响——那层管命令执行，这层管模型循环的兜底。",
];

const TODO_INITIAL: TodoItem[] = [
  { content: "读 session.go 的工具循环现状", status: "done" },
  { content: "加 per-tool 超时兜底", status: "active" },
  { content: "补回归测试并跑全量", status: "pending" },
];

const TODO_LATER: TodoItem[] = [
  { content: "读 session.go 的工具循环现状", status: "done" },
  { content: "加 per-tool 超时兜底", status: "done" },
  { content: "补回归测试并跑全量", status: "active" },
];

const SESSIONS: SessionMeta[] = [
  { id: "20260911-103024-a1b2", title: "给工具循环加超时保护", updatedAt: "刚刚", messages: 9 },
  { id: "20260910-225918-0a9e", title: "前后台分离的协议层评审", updatedAt: "昨天", messages: 14 },
  { id: "20260910-164246-c3d4", title: "edit 工具的唯一匹配校验设计", updatedAt: "3 天前", messages: 22 },
  { id: "20260909-090102-e5f6", title: "选型：Tauri 壳的边界", updatedAt: "上周", messages: 8 },
];

export class DemoAgent implements AgentSource {
  label = "演示模式";
  private listeners = new Set<Listener>();
  private timers: ReturnType<typeof setTimeout>[] = [];
  private reasoningIdx = 0;
  private confirmCb: ((allow: boolean) => void) | null = null;
  private pendingConfirm: ConfirmRequest | null = null;
  private busy = false;
  private sessions_ = SESSIONS;
  private currentSession = SESSIONS[0].id;

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    // 接入即 ready（模拟 connection.ready）
    this.emit({ type: "ready", server: "myt-harness", version: "1", busy: this.busy });
    return () => this.listeners.delete(listener);
  }

  send(text: string): void {
    if (this.busy) return;
    this.busy = true;
    this.emit({ type: "userMessage", text });
    this.emit({ type: "busy", busy: true });
    this.runTurn();
  }

  confirm(id: string, allow: boolean): void {
    if (this.pendingConfirm?.id !== id) return;
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

  newSession(): void {
    const id = "20260911-" + new Date().toTimeString().slice(0, 8).replaceAll(":", "") + "-n" + Math.floor(Math.random() * 90 + 10);
    this.sessions_ = [
      { id, title: "新会话", updatedAt: "刚刚", messages: 0 },
      ...this.sessions_,
    ];
    this.currentSession = id;
    this.emit({ type: "sessionChanged", id, reason: "new" });
  }

  resumeSession(id: string): void {
    this.currentSession = id;
    this.emit({ type: "sessionChanged", id, reason: "resumed" });
  }

  sessions(): SessionMeta[] {
    return this.sessions_;
  }

  get currentId(): string {
    return this.currentSession;
  }

  // ---- 编排 ----

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
    this.reasoningIdx = 0;
    this.emit({ type: "busy", busy: false });
  }

  private runTurn() {
    // 阶段 1：思考链逐句流出（reasoning delta）
    let t = 300;
    REASONING.forEach((line) => {
      this.at(t, () => this.emit({ type: "delta", kind: "reasoning", text: line + "\n" }));
      t += 520 + Math.random() * 300;
    });

    // 阶段 2：低危工具（read_file）自动执行
    this.at(t + 400, () => {
      this.emit({ type: "toolCall", id: "c1", name: "read_file", arguments: JSON.stringify({ path: "internal/agent/session.go", offset: 296, limit: 40 }) });
    });
    this.at(t + 1400, () => {
      this.emit({
        type: "toolResult", id: "c1", name: "read_file", isError: false,
        content: "296→// runTools 执行本轮工具调用（高危先确认）；返回 false 表示被取消。\n297→func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall) bool {\n…（共 548 行，已显示 296-335 行）",
      });
    });

    // 阶段 3：任务清单更新
    this.at(t + 2200, () => this.emit({ type: "todoUpdated", items: TODO_INITIAL }));

    // 阶段 4：高危工具（bash）→ 确认门
    this.at(t + 3000, () => {
      const req: ConfirmRequest = {
        id: "c2",
        name: "bash",
        arguments: JSON.stringify({ command: "go test ./internal/agent/ -run TestToolTimeout -count=1" }),
        prompt: "将执行命令: go test ./internal/agent/ -run TestToolTimeout -count=1",
      };
      this.pendingConfirm = req;
      this.emit({ type: "confirmRequest", request: req });
      // 等用户裁决（confirm 回调里续播）；演示模式不设自动超时
      this.confirmCb = (allow) => {
        if (allow) {
          this.emit({
            type: "toolResult", id: "c2", name: "bash", isError: false,
            content: "ok  github.com/moyunteng/myt-harness/internal/agent\t1.204s\nPASS",
          });
          this.emit({ type: "todoUpdated", items: TODO_LATER });
          this.streamAnswer();
        } else {
          this.emit({
            type: "toolResult", id: "c2", name: "bash", isError: true,
            content: "用户拒绝执行。已改用读测试源码核对的方式验证。",
          });
          this.streamAnswer(true);
        }
      };
    });
  }

  private streamAnswer(denied = false) {
    const parts = denied
      ? ["好的，不跑测试了。我从测试源码层面核对了改动路径：", "", "`TestToolTimeout` 覆盖了超时回填的两个断言（错误文本 + 整轮不中断），与实现一致。", "", "需要我稍后再跑全量验证的话，随时说。"]
      : ANSWER;
    let t = 400;
    parts.forEach((p) => {
      this.at(t, () => this.emit({ type: "delta", kind: "text", text: (p === "" ? "\n" : p) + "\n" }));
      t += 260 + p.length * 6;
    });
    this.at(t + 300, () => {
      this.emit({ type: "done", usageTokens: 2545 + Math.floor(Math.random() * 400), finishReason: "stop" });
      this.finish();
    });
  }
}
