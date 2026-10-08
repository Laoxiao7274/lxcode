// 对话流：滚动跟随状态机、窗口化渲染、空态（EmptyState）。
// 工具行直接平铺（DSH 形态——每个工具一个独立可折叠的 trow，见
// blocks.tsx 的 ToolBlock；原 WorkGroup「读文件 · 用时 N 秒」外层摘要行
// 是 Codex 语言，已随 DSH 对齐移除）。
// 单块渲染在 ./blocks，纯函数在 ./helpers，空态在 ./EmptyState。
import { useEffect, useRef, useState } from "react";
import type { UIState, ThreadBlock } from "../../shared/store";
import { useAgents } from "../../shared/agents";
import { effectiveDelegates } from "../../shared/agent-delegation";
import { ThinkingState } from "../../aicss/ThinkingState";
import { staggerIn, motionAllowed } from "../../shared/motion";
import { gsap } from "gsap";
import { Block } from "./blocks";
import { EmptyState } from "./EmptyState";
import { ScrollToBottom } from "./ScrollToBottom";
import { useStickyFollow } from "./useStickyFollow";

export function Thread({
  state,
  onConfirm,
  onSuggestion,
  projectName,
  onEdit,
  onRewind,
  onOpenChild,
  revealUid,
}: {
  state: UIState;
  onConfirm: (id: string, allow: boolean) => void;
  onSuggestion?: (text: string) => void;
  /** 新对话空态的归属项目名（选中项目时显示——新会话将建在该项目下）。 */
  projectName?: string;
  /** 用户气泡的动作（复制在 Block 内自足）——Thread 只做透传，判定与副作用
   *  全在 App 与 shared/blocks 的纯函数里（这一层不碰历史）。 */
  onEdit?: (block: ThreadBlock) => void;
  onRewind?: (block: ThreadBlock) => void;
  /** 把子会话作为独立工作区标签打开（DispatchCard 的「打开子会话」）——同样只透传，
   *  标签状态是 App 的事（workspace-tabs 的纯函数）。子会话的**实时过程**在它自己的
   *  标签页里（store 把带 dispatch_id 的子事件同时归约进子会话自己的 state），
   *  卡里不再有卡内子时间线（2026-09-30 用户拍板去掉展开/收起）。 */
  onOpenChild?: (sessionId: string) => void;
  /** 大纲跳转的目标块 uid（null = 没有待处理的跳转）。
   *
   *  为什么由 Thread 而不是 App 做滚动：线程是**窗口化渲染**（只挂底部 windowSize
   *  块），目标块可能根本没挂上 DOM——App 那边按 data-uid 找不到元素，跳转就静默
   *  变成 no-op，而长会话恰恰是最需要跳转的场景（用户点大纲第一条却毫无反应）。
   *  扩窗要改 Thread 自己的 windowSize 状态，所以这一跳归它管：先把窗口撑到包含
   *  目标，等它真的挂上 DOM 再滚动 + 高亮。 */
  revealUid?: number | null;
}) {
  // 跟随状态机：共用实现（useStickyFollow——子会话页同款）。用户贴底 →
  // sticky 跟随；上翻 → 解除；滚回底部 → 恢复。内容增高走 RO 每帧置底。
  const empty = state.blocks.length === 0;
  // 发送缓动滚底：窗口内 RO 不瞬跳，由 tween 自己推进
  const smoothUntilRef = useRef(-1e9);
  const scrollTweenRef = useRef<gsap.core.Tween | null>(null);
  const { endRef, stickyRef, scrollRef, progRef } = useStickyFollow({
    empty,
    composerScope: ".main",
    holdScroll: () => performance.now() < smoothUntilRef.current,
    onDetach: () => scrollTweenRef.current?.kill(),
  });
  // 换会话（含冷启动 "" → id）= 整块历史回放，不是「刚发出」：这一帧挂载的块
  // 不播发送动效（气泡回弹 + 缓动滚底）。不区分的话，点开一条项目会话会白跑一次
  // 0.4s 的滚底动画——CDP 采样实测它就是这次点击里最大的一块主线程开销
  // （gsap _getComputedProperty 11%+ Thread onUpdate 5%，合计约占非空闲样本六成），
  // 用户感觉就是「卡一下」。同一会话内追加新消息（发送）currentId 不变，照常播。
  const prevSessionRef = useRef(state.currentId);
  const replayed = prevSessionRef.current !== state.currentId;
  useEffect(() => {
    prevSessionRef.current = state.currentId;
  });
  const emptyRef = useRef<HTMLDivElement>(null);
  // 渲染窗口：底部 windowSize 块（会话切换重置回初始窗口——上翻扩过的窗口
  // 不重置的话，切进另一条长会话要一次性挂载一大片块，切换就是「卡一下」）
  const [windowSize, setWindowSize] = useState<number>(WINDOW_INITIAL);
  useEffect(() => { setWindowSize(WINDOW_INITIAL); }, [empty, state.currentId]);
  const hidden = Math.max(0, state.blocks.length - windowSize);
  const visible = hidden > 0 ? state.blocks.slice(hidden) : state.blocks;
  // 大纲跳转：目标可能在窗口外（没挂 DOM）——先把窗口撑到包含它，下一帧再滚。
  // handledRef 防重入：扩窗会改 windowSize → 本 effect 重跑，不记「这一跳已处理」就会
  // 每次都重新滚一遍（用户手动滚动会被立刻拽回去）。
  const handledJumpRef = useRef<number | null>(null);
  useEffect(() => {
    if (revealUid === null || revealUid === undefined) { handledJumpRef.current = null; return; }
    if (handledJumpRef.current === revealUid) return;
    const idx = state.blocks.findIndex((b) => b.uid === revealUid);
    if (idx < 0) return;
    if (idx < hidden) {
      // 一次撑够（含一批余量）：逐批 40 个会让长会话连点多次才到位
      setWindowSize(state.blocks.length - idx + WINDOW_BATCH);
      return; // 元素这一帧还不在 DOM 里，扩窗后本 effect 会再跑一次
    }
    const el = document.querySelector<HTMLElement>(`[data-uid="${revealUid}"]`);
    if (!el) return;
    handledJumpRef.current = revealUid;
    el.scrollIntoView({ block: "center" });
    el.classList.add("outline-target");
    window.setTimeout(() => el.classList.remove("outline-target"), 1200);
  }, [revealUid, hidden, state.blocks, windowSize]);
  // 空态的身份芯片：当前 Agent（谁来干活）+ 归属项目；主 Agent 带有效委派计数
  const { agents, activeAgentId, sessionDelegates } = useAgents();
  const activeAgent = agents.find((a) => a.id === activeAgentId) ?? agents.find((a) => a.isMain);
  const delegateCount = activeAgent?.isMain ? effectiveDelegates(agents, sessionDelegates).length : 0;

  // 用户消息挂载 → 缓动滚底（发出「已发送」的手感；贴底跟随由 RO 接管）
  const prevBlocksRef = useRef(state.blocks);
  useEffect(() => {
    const prev = prevBlocksRef.current;
    prevBlocksRef.current = state.blocks;
    // 回放的一屏不做发送缓动：挂载时的 toBottom() 已经贴底，动画只会白白占主线程
    if (replayed) return;
    if (state.blocks.length > prev.length && state.blocks[prev.length]?.kind === "user") {
      stickyRef.current = true;
      const el = scrollRef.current;
      if (!el) return;
      const motion = motionAllowed();
      if (!motion) {
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
    staggerIn(items, { each: 0.045 });
  }, [state.blocks.length]);

  if (empty) {
    return (
      <EmptyState
        emptyRef={emptyRef}
        agent={activeAgent ? { name: activeAgent.name, color: activeAgent.color, isMain: activeAgent.isMain === true } : null}
        delegateCount={delegateCount}
        projectName={projectName}
        onSuggestion={onSuggestion}
      />
    );
  }

  return (
    <div className="thread">
      {/* 窗口化渲染：只渲染底部 window 块 + 顶部哨兵（进入视口前插一批）。
       *  上翻不回收（回收会跳滚动位置）；新块追加在窗口内自然出现。
       *  「很多会话×长会话」的前提——见 docs/frontend-review.md §四-①。 */}
      {hidden > 0 && <WindowSentinel onExpand={() => setWindowSize((n) => n + WINDOW_BATCH)} label={`前面还有 ${hidden} 条…`} />}
      {visible.map((block) => (
        <Block key={block.uid} block={block} onConfirm={onConfirm} replayed={replayed} onEdit={onEdit} onRewind={onRewind} onOpenChild={onOpenChild} data-uid={block.uid} />
      ))}
      {/* 进行中且还没有任何输出时显示思考 shimmer（无角色标签——DSH 形态） */}
      {state.busy && !lastIsStreamingAssistant(state.blocks) && (
        <div className="msg">
          <ThinkingState text="正在处理…" />
        </div>
      )}
      <div ref={endRef} />
      {/* 回到底部：与子会话页共用同一份实现（见 ScrollToBottom.tsx 的说明）。
       *  自己找最近的滚动祖先（.thread-scroll），所以这里不需要传容器。 */}
      <ScrollToBottom />
    </div>
  );
}

/** 首屏窗口与每批前插的块数（视口内块数远小于此——含工具行展开态）。
 *  导出给子会话页：同一份窗口化参数（两处不一致就是两套手感）。 */
export const WINDOW_INITIAL = 40;
export const WINDOW_BATCH = 40;

/** 顶部哨兵：进入视口即请求扩大窗口（IntersectionObserver——比滚动
 *  位置判断便宜且不与跟随状态机打架）。子会话页共用。 */
export function WindowSentinel({ onExpand, label }: { onExpand: () => void; label: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) onExpand();
    }, { rootMargin: "600px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [onExpand]);
  return (
    <div className="window-more" ref={ref} onClick={onExpand} role="button" tabIndex={0} onKeyDown={(e) => e.key === "Enter" && onExpand()}>
      {label}
    </div>
  );
}

function lastIsStreamingAssistant(blocks: ThreadBlock[]): boolean {
  const last = blocks[blocks.length - 1];
  return last?.kind === "assistant" && last.streaming;
}
