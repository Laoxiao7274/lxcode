// 对话流：滚动跟随状态机、空态。
// 工具行直接平铺（DSH 形态——每个工具一个独立可折叠的 trow，见
// blocks.tsx 的 ToolBlock；原 WorkGroup「读文件 · 用时 N 秒」外层摘要行
// 是 Codex 语言，已随 DSH 对齐移除）。
// 单块渲染在 ./blocks，纯函数在 ./helpers。
import { useEffect, useRef } from "react";
import type { UIState, ThreadBlock } from "../../shared/store";
import { ThinkingState } from "../../aicss/ThinkingState";
import { staggerIn, motionAllowed } from "../../shared/motion";
import { gsap } from "gsap";
import { Block } from "./blocks";

const SUGGESTIONS = [
  { icon: "构", title: "把工具循环加上超时兜底", sub: "单工具卡死不再拖住整轮" },
  { icon: "查", title: "读 config/local.json", sub: "看 default 绑定的是哪个模型" },
  { icon: "测", title: "全量测试有红的修掉", sub: "go test ./… 一轮到绿" },
  { icon: "解", title: "讲讲 runTools 的设计", sub: "为什么高危要先确认" },
];

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
      {state.blocks.map((block) => (
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

function lastIsStreamingAssistant(blocks: ThreadBlock[]): boolean {
  const last = blocks[blocks.length - 1];
  return last?.kind === "assistant" && last.streaming;
}
