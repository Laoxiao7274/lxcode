// LogsSource 域（后端日志——logs.tail，桌面错误日志查看器 2026-12）：
// 无状态直通（一次请求一次应答，无缓存无事件——日志是即时读，不是订阅流）。
import type { LogsSource, LogsTailResult } from "../../shared/types";
import type { WsConnection } from "./connection";

export class LogsAdmin implements LogsSource {
  constructor(private readonly conn: WsConnection) {}

  async tail(bytes?: number): Promise<LogsTailResult> {
    const r = (await this.conn.call("logs.tail", bytes ? { bytes } : undefined)) as
      | { path?: string; content?: string; truncated?: boolean }
      | null;
    return {
      path: r?.path ?? "",
      content: r?.content ?? "",
      truncated: Boolean(r?.truncated),
    };
  }
}
