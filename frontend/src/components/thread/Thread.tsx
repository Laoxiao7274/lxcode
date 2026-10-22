// 对话流：滚动跟随状态机、窗口化渲染、空态（EmptyState）。
// 工具行直接平铺（DSH 形态——每个工具一个独立可折叠的 trow，见
// blocks.tsx 的 ToolBlock；原 WorkGroup「读文件 · 用时 N 秒」外层摘要行
// 是 Codex 语言，已随 DSH 对齐移除）。
// 单块渲染在 ./blocks，纯函数在 ./helpers，空态在 ./EmptyState。
import { useEffect, useLayoutEffect, useRef, useState } from "react";
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
import { useWindowedHistory, WINDOW_INITIAL, WINDOW_BATCH } from "./useWindowedHistory";
import { useLoadOlderHistory, loadOlderLabel } from "./useLoadOlderHistory";
import { timelineOf } from "../../shared/blocks";
import type { AgentSource } from "../../shared/types";

export function Thread({
  state,
  source,
  onConfirm,
  onAnswer,
  onSuggestion,
  projectName,
  onEdit,
  onRewind,
  onOpenChild,
  revealUid,
}: {
  state: UIState;
  /** 数据源：上翻触顶时要按 before_seq 补更早一页历史（首屏只拉尾部窗口）。 */
  source: AgentSource;
  onConfirm: (id: string, allow: boolean) => void;
  /** ask_user 提问的回答（ask 变体确认卡）——透传给 Block（判定与副作用在 App）。 */
  onAnswer?: (id: string, text: string) => void;
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
  // **渲染不拼数组**（2026-12 流式性能）：流式块单独在 state.streaming 里，
  // 渲染时把 visible（已定型窗口）与它**分两段**挂上去——`[...blocks, streaming]`
  // 那种「拼一份逻辑时间线」每帧都是 O(块数) 复制，正好把 reduceDelta 省下来的
  // 又还回去。只有顺序敏感的派生计算（大纲/轮次/标题反查）才用 timelineOf。
  const streaming = state.streaming;
  const total = state.blocks.length + (streaming ? 1 : 0);
  // 跟随状态机：共用实现（useStickyFollow——子会话页同款）。用户贴底 →
  // sticky 跟随；上翻 → 解除；滚回底部 → 恢复。内容增高走 RO 每帧置底。
  const empty = total === 0;
  // 发送缓动滚底：窗口内 RO 不瞬跳，由 tween 自己推进
  const smoothUntilRef = useRef(-1e9);
  const scrollTweenRef = useRef<gsap.core.Tween | null>(null);
  const { endRef, stickyRef, scrollRef, progRef } = useStickyFollow({
    empty,
    composerScope: ".main",
    sessionKey: state.currentId,
    holdScroll: () => performance.now() < smoothUntilRef.current,
    onDetach: () => scrollTweenRef.current?.kill(),
  });
  // **换会话 = 程序置底而不是用户滚动**：useStickyFollow 的 scroll 兜底判定会在
  // 置底后 120ms 内把任何 scroll 事件当成程序自己的（prog 时间窗），但窗口重置
  // （windowSize 40）与全量重建让置底后的一帧里内容高度还会变——RO 在 sticky=true
  // 时会继续每帧置底，此时若用户恰好开始滚动会被拽回（切换瞬间的「滚不动」感）。
  // 换会话的那一帧显式把 sticky 置真（新会话应从底部看起），并把 prog 窗口拉长，
  // 让重建期间的 scroll 噪声不污染用户意图判定。
  const prevIdRef = useRef(state.currentId);
  if (prevIdRef.current !== state.currentId) {
    prevIdRef.current = state.currentId;
    stickyRef.current = true;
    progRef.current = performance.now();
    // 旧会话可能还挂着发送滚底 tween：不杀的话它的 onUpdate 会一直写 scrollTop，
    // 跟新会话的置底（useStickyFollow 的 sessionKey 重装 + 双 rAF 贴底）拉扯。
    scrollTweenRef.current?.kill();
  }
  // 换会话（含冷启动 "" → id）= 整块历史回放，不是「刚发出」：这一帧挂载的块
  // 不播发送动效（气泡回弹 + 缓动滚底）。不区分的话，点开一条项目会话会白跑一次
  // 0.4s 的滚底动画——CDP 采样实测它就是这次点击里最大的一块主线程开销
  // （gsap _getComputedProperty 11%+ Thread onUpdate 5%，合计约占非空闲样本六成），
  // 用户感觉就是「卡一下」。同一会话内追加新消息（发送）currentId 不变，照常播。
  //
  // **同会话全量重建也不播**（2026-12「撤回闪回来」）：历史重拉（rewind/setLeaf
  // 副作用）会把 blocks 整体换掉（全新 uid），只看 currentId 的话这些重建块会被
  // 当成「刚发出」逐个弹一遍——时间线闪回来 = 界面抽风。判据：本会话内
  // rebuiltGen 变了（reduce 层只在 historyLoaded 的重建分支递增）→ replayed；
  // 换会话（prevSessionRef 变）→ 旧会话的 gen 与新会话无从比较，同款置真。
  // gen 相同且会话没换 = 正常的实时追加（发送），照常播。
  const prevSessionRef = useRef(state.currentId);
  const prevGenRef = useRef(state.rebuiltGen);
  const replayed = prevSessionRef.current !== state.currentId || prevGenRef.current !== state.rebuiltGen;
  useEffect(() => {
    prevSessionRef.current = state.currentId;
    prevGenRef.current = state.rebuiltGen;
  });
  // **同会话重建的滚动锚定**（2026-12「撤回闪回来」的配套，useWindowedHistory 的
  // captureAnchor 同款思路）：全量重建（rewind/setLeaf 副作用重拉）换掉整批块，
  // 内容高度几乎必然变化——浏览器保持 scrollTop 不变的话，视口内容会整体跳到
  // 另一段历史（用户上翻看着某一段时尤其明显；贴底时无所谓，跟随置底自己会处理）。
  //
  // 时序（为什么捕获在渲染期、补偿在 layout effect）：gen 变化的那一帧，渲染期
  // DOM 还是**旧内容**（提交在后），此刻读 scrollHeight 拿到的正是重建前的高度；
  // 提交后 useLayoutEffect（绘制前）按高度差补回 scrollTop，视口内容不动。
  // 渲染期读 scrollHeight 强制一次布局回收——只在 gen 变化帧发生（低频），不能
  // 放进每帧都跑的路径。
  const prevGenForAnchor = useRef(state.rebuiltGen);
  const anchorForRebuild = useRef<{ top: number; height: number } | null>(null);
  if (prevGenForAnchor.current !== state.rebuiltGen) {
    // 重建帧：DOM 此刻还是旧内容，记下滚动位置；换会话的重建（gen 与会话一起变）
    // 不锚定——新会话从底部看起，跟随状态机负责置底。
    prevGenForAnchor.current = state.rebuiltGen;
    const el = scrollRef.current;
    anchorForRebuild.current = el && prevSessionRef.current === state.currentId
      ? { top: el.scrollTop, height: el.scrollHeight }
      : null;
  } else if (anchorForRebuild.current) {
    // 重建已完成补偿：旧锚点清掉，别让下一次无关的布局变化拿它错补一次。
    anchorForRebuild.current = null;
  }
  useLayoutEffect(() => {
    const anchor = anchorForRebuild.current;
    if (!anchor) return;
    const el = scrollRef.current;
    // 容器缺席（如整条撤光后的空态换代）同样清账，别把锚点带进下一次布局变化。
    if (!el) { anchorForRebuild.current = null; return; }
    // 贴底时不动：sticky 跟随的 RO 会把视口带回底部，锚定补上反而打架。
    if (stickyRef.current) { anchorForRebuild.current = null; return; }
    const grew = el.scrollHeight - anchor.height;
    if (grew !== 0) el.scrollTop = Math.max(0, anchor.top + grew);
    anchorForRebuild.current = null;
  });
  const emptyRef = useRef<HTMLDivElement>(null);
  // 渲染窗口 + 上翻扩窗的滚动锚定（共用实现见 ./useWindowedHistory：
  // 窗口按会话派生——切会话的首帧就是 40 块，不靠 effect 事后重置；
  // 前插更早的块时按高度差补偿 scrollTop，视口内容不动）。
  const { windowSize, hidden, update: updateWindow, expandOlder, captureAnchor, clearAnchor } = useWindowedHistory({
    sessionKey: state.currentId,
    total,
    scrollRef,
    isSticky: () => stickyRef.current,
  });
  // 窗口只切**已定型块**；流式块恒在末尾、永远落在窗口内——所以 hidden（按
  // 逻辑时间线算，已包含流式那一块）直接就是 blocks 的切片起点：
  //   hidden = total - windowSize = blocks.length + 1 - windowSize
  //   ⇒ blocks.slice(hidden) 给出 windowSize - 1 块，加上流式块正好一窗。
  const visible = hidden > 0 ? state.blocks.slice(hidden) : state.blocks;
  // 上翻触顶时先补**更早一页历史**（后端还有），补不到再扩本地窗口——
  // 分页的结果经 historyLoaded 增量前插（reduce.ts 的 mergeOlderHistory），
  // 前插后的滚动锚定由 expandOlder 的同一套补偿负责（见 useWindowedHistory）。
  const loadOlder = useLoadOlderHistory({
    sessionId: state.currentId,
    source,
    hasMore: state.historyMore,
    firstSeq: state.historyFirstSeq,
    expand: expandOlder,
    captureAnchor,
    clearAnchor,
  });
  // 大纲跳转：目标可能在窗口外（没挂 DOM）——先把窗口撑到包含它，下一帧再滚。
  // handledRef 防重入：扩窗会改 windowSize → 本 effect 重跑，不记「这一跳已处理」就会
  // 每次都重新滚一遍（用户手动滚动会被立刻拽回去）。
  // 软上限（2026-12 渲染性能 Top4）：一次提交最多多挂 WINDOW_SOFT_CAP 块——
  // 目标比这更远时分批推进（本 effect 随 windowSize 重跑续扩），跳转目标
  // 最终仍要到达；一次性挂 990 块的「跳一下卡一秒」就是这么来的。
  const handledJumpRef = useRef<number | null>(null);
  useEffect(() => {
    if (revealUid === null || revealUid === undefined) { handledJumpRef.current = null; return; }
    if (handledJumpRef.current === revealUid) return;
    // 流式块也在逻辑时间线里（它恒在末尾）：先查已定型块（常见的跳转目标都在
    // 这里），没命中再比一次流式块——不为此拼一份全量数组。
    let idx = state.blocks.findIndex((b) => b.uid === revealUid);
    if (idx < 0 && streaming && streaming.uid === revealUid) idx = state.blocks.length;
    if (idx < 0) return;
    if (idx < hidden) {
      updateWindow((prev) => expandWindowForJump(prev, idx, total));
      return; // 元素这一帧还不在 DOM 里，扩窗后本 effect 会再跑一次
    }
    const el = document.querySelector<HTMLElement>(`[data-uid="${revealUid}"]`);
    if (!el) return;
    handledJumpRef.current = revealUid;
    el.scrollIntoView({ block: "center" });
    el.classList.add("outline-target");
    window.setTimeout(() => el.classList.remove("outline-target"), 1200);
  }, [revealUid, hidden, state.blocks, streaming, total, windowSize]);
  // 空态的身份芯片：当前 Agent（谁来干活）+ 归属项目；主 Agent 带有效委派计数
  const { agents, activeAgentId, sessionDelegates } = useAgents();
  const activeAgent = agents.find((a) => a.id === activeAgentId) ?? agents.find((a) => a.isMain);
  const delegateCount = activeAgent?.isMain ? effectiveDelegates(agents, sessionDelegates).length : 0;

  // 用户消息挂载 → 缓动滚底（发出「已发送」的手感；贴底跟随由 RO 接管）
  // 发送缓动只关心「新追加的块是不是用户气泡」——流式块不参与（它是 assistant）。
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
    if (total > 0 || !emptyRef.current) return;
    const items = emptyRef.current.querySelectorAll<HTMLElement>(".empty-state > *, .suggest-card");
    staggerIn(items, { each: 0.045 });
  }, [total]);

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
      {/* 哨兵在「本地还有没挂的块」**或「后端还有更早的历史」**时都要在：
       *  本地窗口扩到 0（hidden=0）之后如果没有它，分页就永远触发不了——
       *  长会话上翻到已装载历史的开头就卡住了。 */}
      {(hidden > 0 || state.historyMore) && (
        <WindowSentinel onExpand={loadOlder} label={loadOlderLabel(state.historyMore, hidden)} />
      )}
      {/* 回放帧（换会话的整块历史）的 replayed 只管用户气泡的入场弹动（见
       *  Block.tsx 的 layout effect）——那一帧是要尽快画出内容的一帧。 */}
      {visible.map((block) => (
        <Block key={block.uid} block={block} onConfirm={onConfirm} onAnswer={onAnswer} replayed={replayed} onEdit={onEdit} onRewind={onRewind} onOpenChild={onOpenChild} busy={state.busy} data-uid={block.uid} />
      ))}
      {/* 正在流式的那一块：单独挂（不拼数组——见上面的渲染说明）。
       *  它恒在末尾，所以它的挂载/卸载与「最后一段正文」的视觉顺序完全一致。 */}
      {streaming && (
        <Block key={streaming.uid} block={streaming} onConfirm={onConfirm} onAnswer={onAnswer} replayed={replayed} onEdit={onEdit} onRewind={onRewind} onOpenChild={onOpenChild} busy={state.busy} data-uid={streaming.uid} />
      )}
      {/* 进行中且还没有任何输出时显示思考 shimmer（无角色标签——DSH 形态） */}
      {state.busy && !streaming && (
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
 *  **单一事实源在 ./useWindowedHistory**（主时间线与子会话页共用一份参数——
 *  两处不一致就是两套手感）；这里只做再导出，保持既有 import 面不变。
 *  WINDOW_SOFT_CAP：跳转扩窗的**单次提交**上限（2026-12 渲染性能 Top4）——
 *  目标更远时分批推进（expandWindowForJump 纯函数，测试钉住收敛与上限）。
 *  子会话页没有跳转路径：它的哨兵扩窗本就是每批 WINDOW_BATCH 块，远小于上限。 */
export { WINDOW_INITIAL, WINDOW_BATCH };
export const WINDOW_SOFT_CAP = 400;

/** 跳转扩窗一步（纯函数，单测钉住）：返回新的 windowSize。
 *  - 目标已在窗口内 → 原样返回（调用方 effect 里据此继续滚/高亮）；
 *  - 每步增长 ≤ WINDOW_SOFT_CAP（一次提交的挂载量有界，帧间可画）；
 *  - 不超过「到达目标所需 + 一批余量」（不过冲：目标已够就不多挂）。
 *  收敛性：反复应用必然在 ⌈需要量/上限⌉ 步内让目标进入窗口。 */
export function expandWindowForJump(prev: number, targetIndex: number, blockCount: number): number {
  if (blockCount - targetIndex <= prev) return prev;
  const needed = blockCount - targetIndex + WINDOW_BATCH;
  return Math.min(needed, prev + WINDOW_SOFT_CAP);
}

/** 顶部哨兵：进入视口即请求扩大窗口（IntersectionObserver——比滚动
 *  位置判断便宜且不与跟随状态机打架）。子会话页共用。
 *
 *  三条纪律（「上滑一丢丢就跳到顶」的修复配套）：
 *  ① **onExpand 经 ref 读、IO 只装一次**（依赖 []）：调用方常传内联箭头（身份
 *     每渲染都变），而 IO 每次 observe() 都会**立即交付一条初始状态**。
 *     旧写法把 onExpand 放进依赖：Thread 每渲染（流式 delta 每帧都有）
 *     就拆装重装一次 IO，哨兵在视口内时就会反复触发扩窗——“一次上滑
 *     触发多次加载”的直接来源。
 *  ② **在途锁 + 冷却**：一次扩窗是同步 setState，IO 回调可能同一批里连发条目
 *     （交叉进出、布局变化重评）——锁住一次扩窗后等一小段时间才重新开放。
 *     不锁的下场是一次上滑连发好几批。冷却后重新开放是必需的：
 *     锚定补偿后哨兵仍可能在 rootMargin 内，永不重开放就卡在顶部了。 */
const REARM_MS = 120;
export function WindowSentinel({ onExpand, label }: { onExpand: () => void; label: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const expandRef = useRef(onExpand);
  expandRef.current = onExpand;
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let armed = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) { armed = true; continue; }
        if (!armed) continue;
        armed = false;
        expandRef.current();
        if (timer) clearTimeout(timer);
        timer = setTimeout(() => { timer = null; armed = true; }, REARM_MS);
      }
    }, { rootMargin: "600px 0px" });
    io.observe(el);
    return () => {
      if (timer) clearTimeout(timer);
      io.disconnect();
    };
  }, []);
  return (
    <div className="window-more" ref={ref} onClick={() => expandRef.current()} role="button" tabIndex={0} onKeyDown={(e) => e.key === "Enter" && expandRef.current()}>
      {label}
    </div>
  );
}
