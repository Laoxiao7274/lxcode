// 历史消息全文检索（session.fulltext——侧栏搜索的「消息命中」组）。

/** 一条消息命中（wire 形状与 protocol.SessionFulltextHit = sessiondata.SearchHit
 *  1:1；Go 侧 Search 的 Context 我们不消费——UI 只显示命中行本身）。 */
export interface FulltextHit {
  session_id: string;
  session_title: string;
  /** 「2006-01-02 15:04」形态（后端已格式化）。 */
  updated_at: string;
  /** 会话内序号（前端不消费，保留 wire 对齐）。 */
  index: number;
  /** user / assistant / tool。 */
  role: string;
  /** 命中内容（后端已截一行 120 字符 + 省略号）。 */
  content: string;
}

/** session.fulltext 的结果。timed_out=true = 扫描未完整（2500ms 限时）——
 *  UI 提示「结果可能不全」，不把截断结果说成全集。 */
export interface FulltextResult {
  hits: FulltextHit[];
  total: number;
  timed_out?: boolean;
}

/** 全文检索能力口（AgentSource 的可选能力，与 logsAdmin 同模式）：
 *  WSAgent 直通 session.fulltext；DemoAgent 回自解释拒绝。 */
export interface FulltextSource {
  searchMessages(query: string, max?: number): Promise<FulltextResult>;
}
