// UI 事件模型——字段语义与后端 internal/protocol 一一对应（demo 与 live
// 两个 AgentSource 实现都发这套事件，UI 层不感知数据来源）。

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
  | { type: "sessionChanged"; id: string; reason: string }
  /** 会话列表本身变了（重命名/归档/恢复）——UI 重读 sessions()。 */
  | { type: "sessionsChanged" }
  /** 项目列表变了（添加）——UI 重读 projects()。 */
  | { type: "projectsChanged" };

/** 会话列表条目（对齐 protocol.SessionMeta；workspace 用于侧栏按工作区分组）。 */
export interface SessionMeta {
  id: string;
  title: string;
  updatedAt: string;
  messages: number;
  /** 所属工作区（项目路径的末段；空 = 未分组）。 */
  workspace?: string;
  /** 归档态——侧栏不显示，设置「归档任务」里可恢复。 */
  archived?: boolean;
}

/** 项目（侧栏「项目」分组的数据源；对应后端 projects 表）。 */
export interface ProjectMeta {
  id: string;
  name: string;
  path: string;
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
  /** 新会话（可选归属项目 id——会话挂在项目分组下）。 */
  newSession(workspace?: string): void;
  /** 恢复会话。 */
  resumeSession(id: string): void;
  /** 重命名会话。 */
  renameSession(id: string, title: string): void;
  /** 归档会话（当前会话被归档时自动切到新会话）。 */
  archiveSession(id: string): void;
  /** 从归档恢复。 */
  unarchiveSession(id: string): void;
  /** 会话列表。 */
  sessions(): SessionMeta[];
  /** 项目列表。 */
  projects(): ProjectMeta[];
  /** 添加项目（注册目录为 git 仓库——已有仓库不动，没有则 init）。 */
  addProject(name: string, path: string): void;
  /** 显示名（顶栏徽标）。 */
  label: string;
}
