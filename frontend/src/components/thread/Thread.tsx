// 对话流：块分组（工作折叠组）、滚动跟随状态机、空态。
// 单块渲染在 ./blocks，纯函数在 ./helpers。
import { useEffect, useRef, useState } from "react";
import type { UIState, ThreadBlock } from "../../shared/store";
import { ThinkingState } from "../../aicss/ThinkingState";
import { staggerIn, motionAllowed } from "../../shared/motion";
import { playEnter } from "../../shared/anim";
import { gsap } from "gsap";
import { Block } from "./blocks";

const SUGGESTIONS = [
  { icon: "构", title: "把工具循环加上超时兜底", sub: "单工具卡死不再拖住整轮" },
  { icon: "查", title: "读 config/local.json", sub: "看 default 绑定的是哪个模型" },
  { icon: "测", title: "全量测试有红的修掉", sub: "go test ./… 一轮到绿" },
  { icon: "解", title: "讲讲 runTools 的设计", sub: "为什么高危要先确认" },
];

/** 工作组收起态的定格样式（收起后保持 height:0，不能 clear 成 auto——
 *  否则内容 opacity 0 隐身但占位还在）。 */
const COLLAPSED = { height: 0, opacity: 0, overflow: "hidden", paddingTop: 0, paddingBottom: 0 } as const;
const EXPANDED_CLEAR = "height,opacity,overflow,paddingTop,paddingBottom";

type Item =
  | { kind: "single"; block: ThreadBlock }
  | { kind: "work"; blocks: ThreadBlock[]; live: boolean };

/** 连续的工具/确认/清单块收进一个 work 组（Codex 的工作折叠行）。 */
function groupBlocks(blocks: ThreadBlock[], busy: boolean): Item[] {
  const items: Item[] = [];
  let work: ThreadBlock[] | null = null;
  const isWork = (b: ThreadBlock) => b.kind === "tool" || b.kind === "confirm" || b.kind === "todo";
  for (const b of blocks) {
    if (isWork(b)) {
      (work ??= []).push(b);
    } else {
      if (work) {
        items.push({ kind: "work", blocks: work, live: false });
        work = null;
      }
      items.push({ kind: "single", block: b });
    }
  }
  if (work) {
    // 组尾还有进行中的块（无结果）→ live 态（"Working…"）
    const live = busy && work.some((b) => b.kind === "tool" && b.result === undefined);
    items.push({ kind: "work", blocks: work, live });
  }
  return items;
}

export function Thread({
  state,
  onConfirm,
  onSuggestion,
}: {
  state: UIState;
  onConfirm: (id: string, allow: boolean) => void;
  onSuggestion?: (text: string) => void;
}) {
  const endRef = useRef<HTMLDivElement>(null);
  const emptyRef = useRef<HTMLDivElement>(null);
  // 跟随状态机：用户贴底 → sticky 跟随；上翻 → 解除；滚回底部 → 恢复。
  // 用户意图 = wheel/touch/keydown（程序置底绝不触发）+ scroll 兜底
  // （覆盖滚动条拖动；prog 时间窗跳过程序置底自身的事件，防自激）。
  // 内容增高（流式增量、工具/确认卡挂载、gsap 展开逐帧撑高、工作组收起）
  // 全走 ResizeObserver → 每帧置底——只要 sticky 还在。
  // 依赖 [empty]：空态↔会话切换会替换 endRef 所在子树与滚动容器首个子
  // 节点，effect 必须随边界重装——[] 会在空态挂载时因 endRef 为空
  // 而永不安装跟随（页面加载即失效的根因）。
  const stickyRef = useRef(true);
  const scrollRef = useRef<HTMLElement | null>(null);
  // prog：程序置底时间窗（scroll 兜底判定跳过用）；两个 effect 共享
  const progRef = useRef(-1e9);
  // 发送缓动滚底：窗口内 RO 不瞬跳，由 tween 自己推进
  const smoothUntilRef = useRef(-1e9);
  const scrollTweenRef = useRef<gsap.core.Tween | null>(null);
  const empty = state.blocks.length === 0;
  useEffect(() => {
    const el = endRef.current?.parentElement?.parentElement ?? null;
    scrollRef.current = el;
    if (!el) return;
    // sticky 解除只认用户的主动滚动——程序置底也触发 scroll，但那不是
    // 用户意图（prog 时间窗内的 scroll 一律跳过；wheel/touch/keydown 不会
    // 被程序滚动触发，双保险）。
    const toBottom = () => {
      progRef.current = performance.now();
      el.scrollTop = el.scrollHeight;
    };
    const userIntent = () => {
      const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 200;
      stickyRef.current = atBottom;
    };
    const onWheel = () => userIntent();
    const onTouch = () => userIntent();
    const onKey = (e: KeyboardEvent) => {
      if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End"].includes(e.key)) {
        // 键盘滚动后延迟一帧再判定（滚动尚未发生）
        requestAnimationFrame(userIntent);
      }
    };
    const onScroll = () => {
      if (performance.now() - progRef.current < 120) return;
      userIntent();
    };
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchmove", onTouch, { passive: true });
    el.addEventListener("keydown", onKey);
    el.addEventListener("scroll", onScroll, { passive: true });
    const ro = new ResizeObserver(() => {
      // 发送缓动窗口内让位给 tween，避免瞬跳打断滚底动画
      if (performance.now() < smoothUntilRef.current) return;
      if (stickyRef.current) toBottom();
    });
    ro.observe(el.firstElementChild ?? el);
    // 装上先贴一次底：首条消息挂载早于任何 RO 回调，先对齐
    toBottom();
    return () => {
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchmove", onTouch);
      el.removeEventListener("keydown", onKey);
      el.removeEventListener("scroll", onScroll);
      ro.disconnect();
      // 容器即将换代：杀掉进行中的发送缓动，别让 onUpdate 写游离节点
      scrollTweenRef.current?.kill();
    };
  }, [empty]);

  // 用户发出新消息 → 恢复跟随并**缓动滚底**（瞬跳是发送生硬感的来源之一）。
  const prevBlocksRef = useRef(state.blocks);
  useEffect(() => {
    const prev = prevBlocksRef.current;
    prevBlocksRef.current = state.blocks;
    if (state.blocks.length > prev.length && state.blocks[prev.length]?.kind === "user") {
      stickyRef.current = true;
      const el = scrollRef.current;
      if (!el) return;
      if (!motionAllowed()) {
        el.scrollTop = el.scrollHeight;
        return;
      }
      scrollTweenRef.current?.kill();
      smoothUntilRef.current = performance.now() + 460;
      const proxy = { v: el.scrollTop };
      scrollTweenRef.current = gsap.to(proxy, {
        v: el.scrollHeight,
        duration: 0.4,
        ease: "power2.out",
        onUpdate: () => {
          el.scrollTop = proxy.v;
          progRef.current = performance.now();
        },
        onComplete: () => {
          // 动画期间内容若又长高，收尾对齐一次
          el.scrollTop = el.scrollHeight;
          progRef.current = performance.now();
        },
      });
    }
  }, [state.blocks]);

  // 空态入场：标题 → 副文 → 卡片交错浮现
  useEffect(() => {
    if (state.blocks.length > 0 || !emptyRef.current) return;
    const items = emptyRef.current.querySelectorAll<HTMLElement>(".empty-state > *, .suggest-card");
    staggerIn(items, { each: 0.07 });
  }, [state.blocks.length]);

  if (state.blocks.length === 0) {
    return (
      <div className="empty-state" ref={emptyRef}>
        <h2>我们做点什么？</h2>
        <p>读写代码、改文件、跑命令——高危操作先过你这一关。</p>
        <div className="suggest-grid">
          {SUGGESTIONS.map((s) => (
            <button key={s.title} type="button" className="suggest-card" onClick={() => onSuggestion?.(s.title)}>
              <span className="suggest-icon" aria-hidden>{s.icon}</span>
              <span className="suggest-text">
                <span className="suggest-title">{s.title}</span>
                <span className="suggest-sub">{s.sub}</span>
              </span>
            </button>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="thread">
      {groupBlocks(state.blocks, state.busy).map((item) =>
        item.kind === "single" ? (
          <Block key={item.block.uid} block={item.block} onConfirm={onConfirm} />
        ) : (
          <WorkGroup key={"work-" + item.blocks[0].uid} item={item} busy={state.busy} onConfirm={onConfirm} />
        ),
      )}
      {/* 进行中且还没有任何输出时显示思考 shimmer */}
      {state.busy && !lastIsStreamingAssistant(state.blocks) && (
        <div className="msg">
          <div className="msg-role assistant">lxcode</div>
          <ThinkingState text="正在处理…" />
        </div>
      )}
      <div ref={endRef} />
    </div>
  );
}

function lastIsStreamingAssistant(blocks: ThreadBlock[]): boolean {
  const last = blocks[blocks.length - 1];
  return last?.kind === "assistant" && last.streaming;
}

/** 工作组：折叠行（"读了文件、跑了命令 · 用时 8 秒"）+ 展开的工具块。
 *  整个回合没结束（busy=true）绝不自动收起——工具确认卡就藏在这段过程里；
 *  回合结束（busy true→false）自动收起成摘要行一次，用户可再展开。 */
function WorkGroup({ item, busy, onConfirm }: { item: Extract<Item, { kind: "work" }>; busy: boolean; onConfirm: (id: string, allow: boolean) => void }) {
  const [open, setOpen] = useState(busy);
  const bodyRef = useRef<HTMLDivElement>(null);
  const rowRef = useRef<HTMLButtonElement>(null);
  const { blocks, live } = item;
  // 回合结束翻转 → 自动收起（一次性；只收不回弹，用户再展开后不再打扰）。
  // 不能用 live：它只盯"有没有工具在等结果"，确认卡到达时已经 false，
  // 收起会把待裁决的确认卡整个藏掉。
  const prevBusy = useRef(busy);
  useEffect(() => {
    if (prevBusy.current && !busy) setOpen(false);
    prevBusy.current = busy;
  }, [busy]);

  // 工作组行挂载淡入
  useEffect(() => {
    playEnter(rowRef.current, { opacity: 0 }, { opacity: 1, duration: 0.3, ease: "power2.out", clearProps: "opacity" });
  }, []);

  // 工作摘要：动词归纳（读了文件 / 改了文件 / 跑了命令 / 等待确认…）
  const names = blocks.map((b) => b.kind === "tool" ? b.name : b.kind);
  const verbs = new Set(names.map((n) =>
    n === "read_file" ? "读文件" : n === "search" ? "搜索" : n === "bash" ? "跑命令"
    : n === "edit" || n === "write_file" ? "改文件" : n === "session_search" ? "查历史" : n,
  ));
  const summary = [...verbs].join("、");
  const count = blocks.filter((b) => b.kind === "tool").length;
  const seconds = Math.max(1, Math.round(count * 2.3));

  // 展开/收起：gsap 高度动画。状态由 effect 单向驱动（不用 style prop——
  // React 内联样式和 gsap 补间会互相覆写；收起后保持 height:0，不能 clear
  // 成 auto，否则内容 opacity 0 隐身但占位还在）。
  const toggle = () => setOpen((o) => !o);
  const firstRun = useRef(true);
  useEffect(() => {
    const el = bodyRef.current;
    if (!el) return;
    const first = firstRun.current;
    firstRun.current = false;
    // 卸载时杀补间（会话切换可能撞上动画中途）；完成的补间自行回收
    const kill = () => gsap.killTweensOf(el);
    if (first || !motionAllowed()) {
      if (!open) gsap.set(el, COLLAPSED);
      else if (!first) gsap.set(el, { clearProps: EXPANDED_CLEAR });
      return kill;
    }
    if (open) {
      gsap.set(el, { overflow: "hidden" });
      gsap.fromTo(el, { height: 0, opacity: 0, paddingTop: 0, paddingBottom: 0 }, {
        height: "auto", opacity: 1, paddingTop: 6, paddingBottom: 6, duration: 0.32, ease: "power2.out",
        // 收尾清内联——padding 回归 CSS 值，height 回 auto（后续内容增减自然伸展）
        onComplete: () => gsap.set(el, { clearProps: EXPANDED_CLEAR }),
      });
    } else {
      // border-box 下 height:0 压不掉 padding——COLLAPSED 一起归零，否则留 12px 占位
      gsap.to(el, { ...COLLAPSED, duration: 0.24, ease: "power2.in" });
    }
    return kill;
  }, [open]);

  return (
    <div className="work-group">
      <button type="button" className="work-row" aria-expanded={open} onClick={toggle} ref={rowRef}>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          {live ? (
            <path d="M12 3a9 9 0 1 0 9 9" />
          ) : (
            <path d="M20 6 9 17l-5-5" />
          )}
        </svg>
        {live ? (
          <span>正在{summary}…</span>
        ) : (
          <span>{summary} · 用时 {seconds} 秒</span>
        )}
        <span className="work-chevron">{open ? "▾" : "▸"}</span>
      </button>
      <div className="work-body" ref={bodyRef}>
        {blocks.map((b, i) => (
          <Block key={i} block={b} onConfirm={onConfirm} />
        ))}
      </div>
    </div>
  );
}
