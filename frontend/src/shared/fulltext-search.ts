// 侧栏「消息命中」区的数据编排（A1 全文搜索露出 UI）：
// 输入 query → 防抖 → session.fulltext → 分组（按会话）→ 结果。
//
// 与标题过滤（Sidebar 内已有的 globalHits）的分工：标题命中不需要请求后端
//（列表内存里就有），消息命中要走 RPC——两者在侧栏里是同一搜索框下的两组。
//
// 防抖纪律：全库扫描是重查询（629MB 真库平均 4s，后端 2500ms 限时截断），
// 每击键一发会把后端拖死。300ms 静默期 + 竞态丢弃（迟到的旧应答不覆盖新
// query 的结果）是这里的最低配置。

import type { FulltextHit, FulltextSource } from "../shared/types";

/** 防抖毫秒：输入静默 300ms 才发请求。 */
export const FULLTEXT_DEBOUNCE_MS = 300;

/** 按会话分组的命中（侧栏「消息命中」区的一行 = 一个会话 + 它的命中片段）。 */
export interface FulltextGroup {
  sessionId: string;
  sessionTitle: string;
  updatedAt: string;
  /** 该会话内的命中片段（命中顺序保留——后端按 seq 排）。 */
  hits: FulltextHit[];
}

/** 把扁平命中按会话分组（后端按会话从近到远返回——分组的顺序保留它，
 *  会话内按 seq）。供侧栏渲染与测试共用。 */
export function groupFulltextHits(hits: FulltextHit[]): FulltextGroup[] {
  const groups: FulltextGroup[] = [];
  const byId = new Map<string, FulltextGroup>();
  for (const h of hits) {
    let g = byId.get(h.session_id);
    if (!g) {
      g = { sessionId: h.session_id, sessionTitle: h.session_title, updatedAt: h.updated_at, hits: [] };
      byId.set(h.session_id, g);
      groups.push(g);
    }
    g.hits.push(h);
  }
  return groups;
}

/** 防抖 + 竞态丢弃的检索钩子核心（不依赖 React——输入一个 setter，方便测试）。
 *  返回清理函数（卸载/换源时调用，取消未发的 timer）。
 *
 *  onResult 回调只在「应答的 query 仍是最新 query」时触发：迟到的旧应答
 *  丢弃（否则先搜 A 再搜 B，A 的结果晚到会把 B 的结果顶掉）。 */
export function createFulltextSearcher(
  source: FulltextSource | undefined,
  onResult: (query: string, groups: FulltextGroup[], total: number, timedOut: boolean) => void,
) {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let seq = 0;
  const run = (query: string) => {
    const my = ++seq;
    if (!source) {
      // 能力缺席（demo/老后端）：如实回空——调用方不显示消息命中区。
      // 不等防抖：没有请求可发，等待只会让 UI 空转 300ms。
      if (my === seq) onResult(query, [], 0, false);
      return;
    }
    source.searchMessages(query).then(
      (r) => {
        if (my !== seq) return; // 竞态丢弃：有更新的查询在路上/已回
        onResult(query, groupFulltextHits(r.hits), r.total, r.timed_out === true);
      },
      () => {
        // 检索失败（断连/超时）：如实回空 + timedOut 语义不给（不是「可能
        // 不全」，是「搜不了」——UI 按「消息检索失败」提示，不误导）。
        if (my !== seq) return;
        onResult(query, [], -1, false);
      },
    );
  };
  return {
    /** 输入变化入口：静默 FULLTEXT_DEBOUNCE_MS 后发请求。空 query 立即清结果。 */
    onQuery(query: string) {
      if (timer) { clearTimeout(timer); timer = null; }
      if (!query.trim()) {
        seq++; // 使在途应答失效
        onResult(query, [], 0, false);
        return;
      }
      if (!source) {
        // 能力缺席：立即回空（run 的 same-path 分支——见下）
        seq++;
        onResult(query, [], 0, false);
        return;
      }
      timer = setTimeout(() => { timer = null; run(query); }, FULLTEXT_DEBOUNCE_MS);
    },
    dispose() {
      if (timer) { clearTimeout(timer); timer = null; }
      seq++; // 卸载后迟到的应答不再回调
    },
  };
}
