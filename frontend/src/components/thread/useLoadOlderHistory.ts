// 「上翻到顶：先补后端历史，再扩本地窗口」（主时间线与子会话页**共用**）。
//
// 为什么需要这一层（2026-12 装载性能）：首屏只拉**尾部窗口**（chat.history 的
// tail=HISTORY_TAIL，见 agent/ws/history-snapshot.ts）。用户上翻越过这份窗口的
// 头部之后，本地扩窗就没有更早的块可挂了——必须按 before_seq 向后端要更早一页
// （协议 ChatHistoryParams.before_seq + tail，应答带 has_more/first_seq）。
//
// 四条语义：
// ① **顺序**：还有后端历史就先补一页；补不到（已经到头 / 没有分页能力）才扩
//    本地窗口。反过来做的话，一次上翻会先白扩一批不存在的窗口（没有新块，
//    视口不动），用户感觉「滑了没反应」。
// ② **滚动锚定复用 useWindowedHistory 的同一套**：分页请求发出前记 scrollTop/
//    scrollHeight（captureAnchor），应答经 historyLoaded 前插进时间线后，由那个
//    hook 的 useLayoutEffect 按高度差补回 scrollTop——**在绘制前**，视口内容不动。
//    分页是异步的，所以锚点必须在请求前记（不能等应答）。
// ③ **在途锁 + 冷却**：滚动触发的调用可能同一批里连发（IO 交叉进出 / 布局
//    重评）——不锁就是一次上滑连发好几页（与 WindowSentinel 同一条纪律）。
// ④ **失败必须收敛**：请求失败 = 这一页没补上，但**不能**把 historyMore 误置
//    false（那等于宣布"已经到头"）；锁在 finally 里释放，用户再滑一次可以重试。
//    失败时丢弃锚点：没有前插发生，留着它会让下一次无关的布局变化错补一次。
import { useCallback, useRef } from "react";
import type { AgentSource } from "../../shared/types";

/** 同一批里的重复触发在这个时间窗内被吞掉（毫秒）。与 WindowSentinel 的
 *  REARM_MS 同量级：一帧 IO 回调 + 一次布局重评都在 120ms 内。 */
const REARM_MS = 120;

export function useLoadOlderHistory(opts: {
  /** 会话 id（主会话 / 子会话——同一个 chat.history 方法，按 id 寻址）。 */
  sessionId: string;
  source: AgentSource;
  /** 后端还有更早的历史（state.historyMore——只有 before_seq 应答会置真）。 */
  hasMore: boolean;
  /** 已装载历史里最旧一条消息的 seq（下一次 before_seq 的 anchor；0 = 没有）。 */
  firstSeq: number;
  /** 本地扩窗（没有后端分页可补时的回退——纯前端窗口化）。 */
  expand: () => void;
  /** 记滚动锚点（请求发出前；见 useWindowedHistory 的 captureAnchor）。 */
  captureAnchor: () => void;
  /** 丢弃锚点（请求失败、没有前插发生）。 */
  clearAnchor: () => void;
  /** 自定义装载器（缺省 = source.historyBefore）。
   *
   *  子会话页必须传 `(seq) => source.childHistory(sessionId, seq)`：子会话历史要带
   *  **child 标记**（user 消息是主 Agent 派发的任务说明书，渲染成「任务」块而不是
   *  用户气泡）。走通用的 historyBefore 会丢掉那个标记——分页补进来的任务说明书
   *  会变成用户气泡（同一段历史两种画法）。 */
  load?: (beforeSeq: number) => Promise<void>;
}) {
  const { sessionId, source, hasMore, firstSeq, expand, captureAnchor, clearAnchor, load } = opts;
  // 回调走 ref：调用方常传内联箭头，身份每帧都变——进依赖会让哨兵 churn
  // （与 WindowSentinel 同款理由）。这里直接读最新值即可。
  const latest = useRef({ sessionId, source, hasMore, firstSeq, expand, captureAnchor, clearAnchor, load });
  latest.current = { sessionId, source, hasMore, firstSeq, expand, captureAnchor, clearAnchor, load };
  const inflight = useRef(false);
  const lastAt = useRef(-1e9);

  const loadOlder = useCallback(() => {
    const now = performance.now();
    if (inflight.current || now - lastAt.current < REARM_MS) return;
    const cur = latest.current;
    const loader = cur.load ?? (cur.source.historyBefore
      ? (seq: number) => cur.source.historyBefore!(cur.sessionId, seq)
      : null);
    // 没有分页能力（老后端 / 演示源）或已经到头：只扩本地窗口（既有行为）
    if (!cur.hasMore || cur.firstSeq <= 0 || !loader) {
      cur.expand();
      return;
    }
    inflight.current = true;
    lastAt.current = now;
    // 锚点必须在请求前记：应答到达时 scrollTop 可能已被流式内容/布局变化动过。
    cur.captureAnchor();
    void loader(cur.firstSeq)
      .catch(() => {
        // 这一页没补上：不宣布"到头"，也不弹窗（滚动触发的高频动作）；
        // 冷却窗口过后用户再滑一次就是重试。没有前插 → 丢弃锚点。
        cur.clearAnchor();
        lastAt.current = performance.now();
      })
      .finally(() => {
        inflight.current = false;
      });
  }, []);

  return loadOlder;
}

/** 顶部哨兵的文案（「加载更早的历史…」/「前面还有 N 条…」/「已加载全部历史」）。
 *  纯函数——Thread 与 ChildSessionPage 共用一份，不各写一套措辞。 */
export function loadOlderLabel(hasMore: boolean, hidden: number): string {
  if (hasMore) return "加载更早的历史…";
  if (hidden > 0) return `前面还有 ${hidden} 条…`;
  return "已加载全部历史";
}
