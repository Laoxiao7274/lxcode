// WSAgent：真实后端对接（WS JSON-RPC :7789——协议与 Go 后端 internal/protocol 一致）。
// 事件流 → AgentEvent 映射，与 DemoAgent 可互换（App.tsx 一行切换）。
import type { AgentEvent, AgentSource, ConfirmRequest, SessionMeta, TodoItem } from "../../shared/types";

/** WS JSON-RPC 帧结构（与 Go internal/protocol 对齐）。 */
interface WsRequest {
  jsonrpc: string;
  id?: number;
  method: string;
  params?: unknown;
}

interface WsResponse {
  jsonrpc: string;
  id?: number;
  result?: unknown;
  error?: { code: number; message: string };
  method?: string; // 事件帧（无 id）
  params?: unknown;
}

type Listener = (ev: AgentEvent) => void;

export class WSAgent implements AgentSource {
  label = "已连接";
  private ws: WebSocket | null = null;
  private listeners = new Set<Listener>();
  private nextId = 1;
  private pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  private connected = false;
  private sessionsCache: SessionMeta[] = [];
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(private addr = "127.0.0.1:7789") {}

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    this.connect();
    return () => this.listeners.delete(listener);
  }

  /** 连接 WS（自动重连）。 */
  private connect() {
    if (this.ws?.readyState === WebSocket.OPEN || this.ws?.readyState === WebSocket.CONNECTING) return;
    const url = `ws://${this.addr}/rpc`;
    this.ws = new WebSocket(url);

    this.ws.onopen = () => {
      this.connected = true;
      // 握手 + 拉初始状态
      this.call("connection.hello", { client: "lxcode-web", version: "1" })
        .then(() => this.call("chat.history"))
        .then((r) => {
          const h = r as { session_id?: string; messages?: unknown[]; busy?: boolean; todos?: TodoItem[] };
          if (h.session_id) {
            this.sessionsCache = [{ id: h.session_id, title: "当前会话", updatedAt: "刚刚", messages: h.messages?.length ?? 0 }];
          }
        })
        .catch(() => {});
    };

    this.ws.onmessage = (e) => {
      const msg: WsResponse = JSON.parse(e.data);
      // 应答帧（有 id）
      if (msg.id !== undefined && msg.id !== null) {
        const p = this.pending.get(msg.id);
        if (p) {
          this.pending.delete(msg.id);
          if (msg.error) p.reject(new Error(msg.error.message));
          else p.resolve(msg.result);
        }
        return;
      }
      // 事件帧（无 id）
      if (msg.method) {
        this.handleEvent(msg.method, msg.params);
      }
    };

    this.ws.onclose = () => {
      this.connected = false;
      this.emit({ type: "error", message: "后端连接断开", aborted: true });
      // 5s 重连
      this.reconnectTimer = setTimeout(() => this.connect(), 5000);
    };

    this.ws.onerror = () => {
      // onclose 会跟着触发
    };
  }

  /** 后端事件 → AgentEvent 映射。 */
  private handleEvent(method: string, params: unknown) {
    const p = params as Record<string, unknown>;
    switch (method) {
      case "chat.userMessage":
        this.emit({ type: "userMessage", text: String(p.text ?? "") });
        break;
      case "chat.delta":
        this.emit({ type: "delta", kind: String(p.kind) as "text" | "reasoning", text: String(p.text ?? "") });
        break;
      case "chat.toolCall":
        this.emit({ type: "toolCall", id: String(p.id), name: String(p.name), arguments: String(p.arguments ?? "") });
        break;
      case "chat.toolResult":
        this.emit({ type: "toolResult", id: String(p.id), name: String(p.name), content: String(p.content ?? ""), isError: Boolean(p.is_error) });
        break;
      case "chat.confirmRequest":
        this.emit({ type: "confirmRequest", request: p as unknown as ConfirmRequest });
        break;
      case "todo.updated":
        this.emit({ type: "todoUpdated", items: (p.items as TodoItem[]) ?? [] });
        break;
      case "chat.done":
        this.emit({ type: "done", usageTokens: Number(p.usage_tokens ?? 0), finishReason: String(p.finish_reason ?? "stop") });
        break;
      case "chat.error":
        this.emit({ type: "error", message: String(p.message ?? ""), aborted: Boolean(p.aborted) });
        break;
      case "chat.busy":
        this.emit({ type: "busy", busy: Boolean(p.busy) });
        break;
      case "session.changed":
        this.emit({ type: "sessionChanged", id: String(p.id ?? ""), reason: String(p.reason ?? "") });
        break;
      case "model.changed":
        // 模型变更——暂无 AgentEvent 对应（可扩展）
        break;
    }
  }

  /** 发 JSON-RPC 请求并等应答。 */
  private call(method: string, params?: unknown): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        reject(new Error("后端未连接"));
        return;
      }
      const id = this.nextId++;
      this.pending.set(id, { resolve, reject });
      const req: WsRequest = { jsonrpc: "2.0", id, method };
      if (params !== undefined) req.params = params;
      this.ws.send(JSON.stringify(req));
      // 10s 超时
      setTimeout(() => {
        if (this.pending.has(id)) {
          this.pending.delete(id);
          reject(new Error("请求超时"));
        }
      }, 10_000);
    });
  }

  private emit(ev: AgentEvent) {
    this.listeners.forEach((l) => l(ev));
  }

  // ---- AgentSource 接口 ----

  send(text: string): void {
    if (!text.trim()) return;
    this.call("chat.send", { text }).catch((e) => {
      this.emit({ type: "error", message: `发送失败: ${e.message}`, aborted: false });
    });
  }

  confirm(id: string, allow: boolean): void {
    this.call("tool.confirm", { id, allow }).catch(() => {});
  }

  cancel(): void {
    this.call("chat.cancel").catch(() => {});
  }

  newSession(): void {
    this.call("session.new").catch(() => {});
  }

  resumeSession(id: string): void {
    this.call("session.resume", { id }).catch(() => {});
  }

  sessions(): SessionMeta[] {
    // 从缓存返回（连接时通过 chat.history 更新）
    return this.sessionsCache;
  }
}
