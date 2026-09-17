// 对话流：滚动跟随状态机、空态。
// 工具行直接平铺（DSH 形态——每个工具一个独立可折叠的 trow，见
// blocks.tsx 的 ToolBlock；原 WorkGroup「读文件 · 用时 N 秒」外层摘要行
// 是 Codex 语言，已随 DSH 对齐移除）。
// 单块渲染在 ./blocks，纯函数在 ./helpers。
import { useEffect, useRef } from "react";
import type { UIState, ThreadBlock } from "../../shared/store";
import { useAgents } from "../../shared/agents";
import { effectiveDelegates } from "../../shared/agent-delegation";
import { ThinkingState } from "../../aicss/ThinkingState";
import { staggerIn, motionAllowed } from "../../shared/motion";
import { gsap } from "gsap";
import { Block } from "./blocks";

/** 建议卡图标：应用图标语言（线性描边，无字符圆圈——OS 感重）。 */
const SUGGESTIONS = [
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M12 20h9" />
        <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
      </svg>
    ),
    title: "把工具循环加上超时兜底",
    sub: "单工具卡死不再拖住整轮",
  },
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" />
        <path d="M14 2v6h6" />
        <path d="M9 13h6M9 17h4" />
      </svg>
    ),
    title: "读 config/local.json",
    sub: "看 default 绑定的是哪个模型",
  },
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="m8.5 12.5 2.5 2.5 5-6" />
      </svg>
    ),
    title: "全量测试有红的修掉",
    sub: "go test ./… 一轮到绿",
  },
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M21 15a2 2 0 0 1-2 2H8l-5 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z" />
      </svg>
    ),
    title: "讲讲 runTools 的设计",
    sub: "为什么高危要先确认",
  },
];

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

  if (empty) {
    return (
      <div className="empty-state" ref={emptyRef}>
        {(activeAgent || projectName) && (
          <div className="empty-meta">
            {activeAgent && (
              <span className="empty-agent" title="当前干活的 Agent——输入区可切换；主 Agent 的可委派名单在菜单里调整">
                <span className="ag-dot" style={{ background: activeAgent.color }} />
                {activeAgent.name}
                {activeAgent.isMain && (
                  <span className="empty-agent-sub">· 可委派 {delegateCount}</span>
                )}
              </span>
            )}
            {projectName && (
              <span className="empty-project" title={`新会话归属 ${projectName}`}>
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
                </svg>
                {projectName}
              </span>
            )}
          </div>
        )}
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
