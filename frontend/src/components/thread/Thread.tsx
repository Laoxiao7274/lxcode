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

export function Thread({
  state,
  onConfirm,
  onSuggestion,
  projectName,
}: {
  state: UIState;
  onConfirm: (id: string, allow: boolean) => void;
  onSuggestion?: (text: string) => void;
  /** 新对话空态的归属项目名（选中项目时显示——新会话将建在该项目下）。 */
  projectName?: string;
}) {
  const endRef = useRef<HTMLDivElement>(null);
  const emptyRef = useRef<HTMLDivElement>(null);
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
      const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 200;
      stickyRef.current = atBottom;
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
        <Block key={block.uid} block={block} onConfirm={onConfirm} />
      ))}
      {/* 进行中且还没有任何输出时显示思考 shimmer（无角色标签——DSH 形态） */}
      {state.busy && !lastIsStreamingAssistant(state.blocks) && (
        <div className="msg">
          <ThinkingState text="正在处理…" />
        </div>
      )}
      <div ref={endRef} />
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
