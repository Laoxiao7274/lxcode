// 流式 delta 合帧（2026-12 渲染性能 Top1）。
//
// 后端的 chat.delta 是 token 级（一轮几百条），每条都 emit = store 每条一次
// setState + 全量 blocks 归约——流式渲染风暴的主力。合帧后同一帧内到达的
// 同类 delta 合并成一次 emit（同键 = session|kind|dispatch 拼接文本）。
//
// 契约（tests/ws-coalesce.test.mjs 钉住）：
// 1. 合帧收益：一次同步突发 N 条 delta → emit 次数远小于 N，文本拼接无丢失；
// 2. 保序：非 delta 事件 emit 前先冲刷挂起的 delta（先到先发）——done/toolCall
//    不能插到已到达文本的前面；
// 3. 终态一致：合帧后的最终 blocks 与逐条归约完全一致（reduceDelta 对同键
//    delta 就是文本拼接，合并只是把 N 次拼接并成 1 次）；
// 4. 异键不合并：不同会话 / 不同 kind（text vs reasoning）/ 子会话 dispatch
//    各占一位，互不串文本。
import type { AgentEvent } from "../../shared/types";
import { documentHidden } from "../../shared/stream-reveal";

// delta 合帧的兜底间隔：浏览器可见时优先 rAF（与绘制对齐，见 scheduleFlush）；
// 无 DOM（node 测试）/页面隐藏时走这个 setTimeout。
const DELTA_COALESCE_MS = 16;

/** 合帧器：delta 入队同键拼接，帧末一次 flush（保序纪律在 WsConnection.emit）。 */
export class DeltaCoalescer {
  /** 队列：key = `session\0kind\0dispatch`，值 = 合并中的 delta 事件（同键文本拼接）。 */
  private queue = new Map<string, Extract<AgentEvent, { type: "delta" }>>();
  /** 冲刷已排程标志 + 取消钩子（rAF/setTimeout 二选一，见 scheduleFlush）。 */
  private scheduled = false;
  private cancel: (() => void) | null = null;
  /** 冲刷进行中标志（防重入：冲刷期间监听器再触发的 emit 不再嵌套冲刷）。 */
  private flushing = false;

  constructor(private readonly emitRaw: (ev: AgentEvent) => void) {}

  /** 事件入口：delta 走合帧，其他事件先冲刷挂起的 delta 再直发（保序）。 */
  emit(ev: AgentEvent): void {
    if (ev.type === "delta") {
      this.enqueue(ev);
      return;
    }
    // 非 delta 事件先冲刷挂起的 delta 再发（保序纪律：先到先发，delta 合并
    // 不打乱它与其他事件的相对顺序——比如 done 前积压的文本必须先归约，
    // 否则 done 会把还在流式中的块定格成缺文）。
    this.flush();
    this.emitRaw(ev);
  }

  /** 流式 delta 合帧入队（同键拼接，异键各占一位——不同会话/不同 kind/主子会话
   *  的 delta 互不合并）。 */
  private enqueue(ev: Extract<AgentEvent, { type: "delta" }>): void {
    const key = `${ev.sessionId}\u0000${ev.kind}\u0000${ev.dispatchId ?? ""}`;
    const queued = this.queue.get(key);
    this.queue.set(key, queued ? { ...queued, text: queued.text + ev.text } : ev);
    this.scheduleFlush();
  }

  /** 排程帧末冲刷：浏览器可见时用 rAF（与绘制对齐，一帧最多一次 emit）；
   *  页面隐藏 rAF 停转、无 DOM（node 测试）没有 rAF——都走 setTimeout 兜底
   *  （事件不能因为页面隐藏就不到达，只是降频；reveal 层自己会 hold 渲染）。 */
  private scheduleFlush(): void {
    if (this.scheduled) return;
    this.scheduled = true;
    const w = globalThis as { requestAnimationFrame?: (cb: () => void) => number; cancelAnimationFrame?: (h: number) => void };
    if (typeof w.requestAnimationFrame === "function" && !documentHidden()) {
      const handle = w.requestAnimationFrame(() => {
        this.scheduled = false;
        this.cancel = null;
        this.flush();
      });
      this.cancel = () => w.cancelAnimationFrame?.(handle);
    } else {
      const handle = setTimeout(() => {
        this.scheduled = false;
        this.cancel = null;
        this.flush();
      }, DELTA_COALESCE_MS);
      this.cancel = () => clearTimeout(handle);
    }
  }

  /** 冲刷合帧队列：按到达序逐条 emit（队列插入序 = 各键首条 delta 的到达序）。
   *  冲刷后队列清空；flushing 防重入（冲刷期间新 delta 只入队等下一帧）。 */
  private flush(): void {
    if (this.cancel) {
      this.cancel();
      this.cancel = null;
    }
    this.scheduled = false;
    if (this.flushing || this.queue.size === 0) return;
    this.flushing = true;
    const queued = [...this.queue.values()];
    this.queue.clear();
    try {
      for (const ev of queued) this.emitRaw(ev);
    } finally {
      this.flushing = false;
    }
  }

  /** 清账（dispose = 卸载语义）：挂起的 delta 没有投递对象了，留着只会在之后
   *  的一帧对空集 emit。 */
  reset(): void {
    if (this.cancel) {
      this.cancel();
      this.cancel = null;
    }
    this.scheduled = false;
    this.queue.clear();
  }
}
