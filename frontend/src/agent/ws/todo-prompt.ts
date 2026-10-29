// TodoPromptSource 域（todo-first 档位——行为开关）：请求编排 + 缓存。
// applyTodoPrompt 双路：握手并行组的 settings.todoPrompt.get 刷新（onInitList
// 注册）与 settings.todoPrompt.changed 事件的副作用（onEventSideEffect 注册——
// 载荷就是 {todoPrompt}，直接采用）。
import type { TodoPromptMode, TodoPromptSource } from "../../shared/types";
import type { WsConnection } from "./connection";

export class TodoPrompt implements TodoPromptSource {
  /** todo-first 档位快照（settings.todoPrompt.changed 驱动刷新；设置面板数据源）。 */
  private todoPromptCache: TodoPromptMode = "strict";
  private listeners = new Set<() => void>();

  constructor(private readonly conn: WsConnection) {
    conn.onInitList("settings.todoPrompt.get", (r) => this.applyTodoPrompt(r));
    conn.onEventSideEffect((method, params) => {
      if (method !== "settings.todoPrompt.changed") return;
      this.applyTodoPrompt(params);
    });
  }

  mode(): TodoPromptMode {
    return this.todoPromptCache;
  }

  onChange(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  set(mode: TodoPromptMode): Promise<void> {
    return this.conn.call("settings.todoPrompt.set", { todoPrompt: mode }).then(() => undefined);
  }

  /** settings.todoPrompt.get 结果 / settings.todoPrompt.changed 载荷 → 缓存 + 通知。 */
  private applyTodoPrompt(result: unknown) {
    const r = result as { todoPrompt?: string } | null;
    if (!r || typeof r.todoPrompt !== "string") return;
    if (r.todoPrompt !== "strict" && r.todoPrompt !== "normal" && r.todoPrompt !== "off") return;
    this.todoPromptCache = r.todoPrompt;
    this.listeners.forEach((l) => l());
  }
}
