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
import { isNearBottom } from "../../shared/scroll-metrics";
import { gsap } from "gsap";
import { Block } from "./blocks";
import { EmptyState } from "./EmptyState";
import { ScrollToBottom } from "./ScrollToBottom";

export function Thread({
  state,
  onConfirm,
  onSuggestion,
  projectName,
  onEdit,
  onRewind,
  onLoadChild,
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
  /** 子会话历史的懒加载入口（DispatchCard 展开时调一次）——Thread **只做透传**，
   *  与 onEdit/onRewind 同款：数据源与映射（source.childHistory + historyBlocks）
   *  都在 App 那一层，渲染层不碰数据源。 */
  onLoadChild?: (sessionId: string) => Promise<ThreadBlock[]>;
  /** 把子会话作为独立工作区标签打开（DispatchCard 的「打开子会话」）——同样只透传，
   *  标签状态是 App 的事（workspace-tabs 的纯函数）。 */
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
  const endRef = useRef<HTMLDivElement>(null);
  const emptyRef = useRef<HTMLDivElement>(null);
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
  // 跟随状态机：用户贴底 → sticky 跟随；上翻 → 解除；滚回底部 → 恢复。
  // 用户意图 = wheel/touch/keydown（程序置底绝不触发）+ scroll 兜底
  // （覆盖滚动条拖动；prog 时间窗跳过程序置底自身的事件，防自激）。
  // 内容增高（流式增量、工具/确认卡挂载、gsap 展开逐帧撑高）全走
  // ResizeObserver → 每帧置底——只要 sticky 还在。
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
  // 渲染窗口：底部 windowSize 块（会话切换重置回初始窗口）
  const [windowSize, setWindowSize] = useState<number>(WINDOW_INITIAL);
  useEffect(() => { setWindowSize(WINDOW_INITIAL); }, [empty]);
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
      // 贴底判定与「回到底部」按钮（ScrollToBottom）**同源**：同一个常量、同一个函数
      // （shared/scroll-metrics）。在这里再写一个 200 就是分叉的开始——按钮会在
      // "其实已经在底部"时还亮着。
      stickyRef.current = isNearBottom(el);
    };
    const onWheel = () => userIntent();
    const onTouch = () => userIntent();
    const onKey = (e: KeyboardEvent) => {
      if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End"].includes(e.key)) {
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
    // 输入区（含任务清单卡）高度变化也要跟随：它只改线程区的
    // padding-bottom（不留心观察不到——内容尺寸没变），贴底时若不让位，
    // 清单展开就会压住对话内容（对话该整体上移，收缩时下移回来）。
    const composerZone = el.closest(".main")?.querySelector<HTMLElement>(".composer-zone");
    if (composerZone) ro.observe(composerZone);
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
        <Block key={block.uid} block={block} onConfirm={onConfirm} replayed={replayed} onEdit={onEdit} onRewind={onRewind} onLoadChild={onLoadChild} onOpenChild={onOpenChild} data-uid={block.uid} />
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

/** 首屏窗口与每批前插的块数（视口内块数远小于此——含工具行展开态）。 */
const WINDOW_INITIAL = 40;
const WINDOW_BATCH = 40;

/** 顶部哨兵：进入视口即请求扩大窗口（IntersectionObserver——比滚动
 *  位置判断便宜且不与跟随状态机打架）。 */
function WindowSentinel({ onExpand, label }: { onExpand: () => void; label: string }) {
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
