// UI 事件模型——字段语义与后端 internal/protocol 一一对应（demo 与 live
// 两个 AgentSource 实现都发这套事件，UI 层不感知数据来源）。

export type ChatRole = "user" | "assistant" | "tool";

/** 任务清单项（对齐 tools.TodoItem）。 */
export interface TodoItem {
  content: string;
  status: "pending" | "active" | "done";
}

/** 确认请求（对齐 protocol.ConfirmRequest）。 */
export interface ConfirmRequest {
  id: string;
  name: string;
  arguments: string;
  prompt: string;
}

/** AgentSource 推给 UI 的事件流（对齐服务端广播事件）。 */
export type AgentEvent =
  | { type: "ready"; server: string; version: string; busy: boolean }
  | { type: "userMessage"; text: string }
  | { type: "delta"; kind: "text" | "reasoning"; text: string }
  | { type: "toolCall"; id: string; name: string; arguments: string }
  | { type: "toolResult"; id: string; name: string; content: string; isError: boolean }
  | { type: "confirmRequest"; request: ConfirmRequest }
  | { type: "todoUpdated"; items: TodoItem[] }
  | { type: "done"; usageTokens: number; finishReason: string }
  | { type: "error"; message: string; aborted: boolean }
  | { type: "busy"; busy: boolean }
  | { type: "sessionChanged"; id: string; reason: string };

/** 会话列表条目（对齐 protocol.SessionMeta）。 */
export interface SessionMeta {
  id: string;
  title: string;
  updatedAt: string;
  messages: number;
}

/**
 * AgentSource 是数据源抽象：demo（脚本编排）与 live（WS 连后端）实现
 * 同一接口。将来 Tauri 壳接入真实后端时只换实现，UI 不动。
 */
export interface AgentSource {
  /** 订阅事件流（返回退订函数）。 */
  subscribe(listener: (ev: AgentEvent) => void): () => void;
  /** 发送消息（一轮开始）。 */
  send(text: string): void;
  /** 裁决确认门。 */
  confirm(id: string, allow: boolean): void;
  /** 取消当前生成。 */
  cancel(): void;
  /** 新会话。 */
  newSession(): void;
  /** 恢复会话。 */
  resumeSession(id: string): void;
  /** 会话列表。 */
  sessions(): SessionMeta[];
  /** 显示名（顶栏徽标）。 */
  label: string;
}
