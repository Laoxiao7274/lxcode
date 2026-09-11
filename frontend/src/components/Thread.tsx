import { useEffect, useRef, useState } from "react";
import type { UIState, ThreadBlock } from "../agent/store";
import { ThinkingReasoning } from "../aicss/ThinkingReasoning";
import { ThinkingState } from "../aicss/ThinkingState";
import { TodoList } from "../aicss/TodoList";
import { ApprovalCard } from "../aicss/ApprovalCard";
import { TextResponse } from "../aicss/TextResponse";
import { StreamingText } from "../aicss/StreamingText";
import { staggerIn, motionAllowed } from "../motion";
import { gsap } from "gsap";
import { useSettings } from "../settings";

const SUGGESTIONS = [
  { icon: "构", title: "把工具循环加上超时兜底", sub: "单工具卡死不再拖住整轮" },
  { icon: "查", title: "读 config/local.json", sub: "看 default 绑定的是哪个模型" },
  { icon: "测", title: "全量测试有红的修掉", sub: "go test ./… 一轮到绿" },
  { icon: "解", title: "讲讲 runTools 的设计", sub: "为什么高危要先确认" },
];

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
  // 判定基于滚动事件本身（用户意图）；内容高度变化（流式增量、gsap
  // 展开动画逐帧撑高）经 ResizeObserver 持续跟随——动画期间每帧置底。
  const stickyRef = useRef(true);
  const scrollRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    scrollRef.current = endRef.current?.parentElement?.parentElement ?? null;
    const el = scrollRef.current;
    if (!el) return;
    // sticky 解除只认用户的主动滚动（滚轮/触摸拖拽/键盘）——程序置底
    // 也会触发 scroll 事件，但那不是用户意图，不能据此解除跟随
    // （否则点确认卡后内容长高把用户"顶离"底部 >200px，跟随就断了）。
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
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchmove", onTouch, { passive: true });
    el.addEventListener("keydown", onKey);
    // sticky 期间内容高度任何变化（含动画逐帧）都置底——工具卡弹出、
    // 工作行展开（gsap 高度动画）、流式文本、确认卡 resolve 都覆盖。
    const ro = new ResizeObserver(() => {
      if (stickyRef.current) el.scrollTop = el.scrollHeight;
    });
    ro.observe(el.firstElementChild ?? el);
    return () => {
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchmove", onTouch);
      el.removeEventListener("keydown", onKey);
      ro.disconnect();
    };
  }, []);

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
          <WorkGroup key={"work-" + item.blocks[0].uid} item={item} onConfirm={onConfirm} />
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

/** 工作组：折叠行（"读了文件、跑了命令 · 用时 8 秒"）+ 展开的工具块。 */
function WorkGroup({ item, onConfirm }: { item: Extract<Item, { kind: "work" }>; onConfirm: (id: string, allow: boolean) => void }) {
  const [open, setOpen] = useState(true);
  const bodyRef = useRef<HTMLDivElement>(null);
  const { blocks, live } = item;
  // 工作摘要：动词归纳（读了文件 / 改了文件 / 跑了命令 / 等待确认…）
  const names = blocks.map((b) => b.kind === "tool" ? b.name : b.kind);
  const verbs = new Set(names.map((n) =>
    n === "read_file" ? "读文件" : n === "search" ? "搜索" : n === "bash" ? "跑命令"
    : n === "edit" || n === "write_file" ? "改文件" : n === "session_search" ? "查历史" : n,
  ));
  const summary = [...verbs].join("、");
  const count = blocks.filter((b) => b.kind === "tool").length;
  const seconds = Math.max(1, Math.round(count * 2.3));

  // 展开/收起：gsap 高度动画（grid-template-rows 技巧避免高度测量）
  const toggle = () => {
    const next = !open;
    setOpen(next);
    const el = bodyRef.current;
    if (!el || !motionAllowed()) return;
    if (next) {
      gsap.fromTo(el, { height: 0, opacity: 0 }, { height: "auto", opacity: 1, duration: 0.32, ease: "power2.out", clearProps: "height,opacity" });
    } else {
      gsap.to(el, { height: 0, opacity: 0, duration: 0.24, ease: "power2.in", onComplete: () => { el.style.height = ""; } });
      // 收起时立即渲染为隐藏态（React 条件渲染改为 CSS 控制）
    }
  };

  return (
    <div className="work-group">
      <button type="button" className="work-row" aria-expanded={open} onClick={toggle}>
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          {live ? (
            <path d="M12 3a9 9 0 1 0 9 9" />
          ) : (
            <path d="M20 6 9 17l-5-5" />
          )}
        </svg>
        {live ? (
          <span className={undefined}>正在{summary}…</span>
        ) : (
          <span>{summary} · 用时 {seconds} 秒</span>
        )}
        <span className="work-chevron">{open ? "▾" : "▸"}</span>
      </button>
      <div className="work-body" ref={bodyRef} style={open ? undefined : { height: 0, opacity: 0, overflow: "hidden" }}>
        {blocks.map((b, i) => (
          <Block key={i} block={b} onConfirm={onConfirm} />
        ))}
      </div>
    </div>
  );
}

function Block({ block, onConfirm }: { block: ThreadBlock; onConfirm: (id: string, allow: boolean) => void }) {
  const bubbleRef = useRef<HTMLDivElement>(null);
  const { settings } = useSettings();

  // 用户气泡入场：轻微回弹（比通用 block-in 更有"发送出去"的手感）
  useEffect(() => {
    if (block.kind === "user" && bubbleRef.current && motionAllowed()) {
      gsap.fromTo(bubbleRef.current, { y: 10, opacity: 0, scale: 0.97 }, { y: 0, opacity: 1, scale: 1, duration: 0.42, ease: "back.out(1.6)" });
    }
  }, [block.kind]);

  switch (block.kind) {
    case "user":
      return (
        <div className="msg user">
          <div className="bubble" ref={bubbleRef}>{block.text}</div>
        </div>
      );

    case "assistant":
      return (
        <div className="msg">
          <div className="msg-role assistant">lxcode</div>
          {settings.showThinking && block.reasoning && (
            <ThinkingReasoning
              sentences={block.reasoning.split("\n").filter(Boolean)}
              phase={block.streaming ? "thinking" : "done"}
              elapsedMs={4200}
            />
          )}
          {block.content === "" ? null : block.streaming ? (
            <StreamingText text={block.content} />
          ) : (
            <TextResponse>
              <Markdownish text={block.content} />
            </TextResponse>
          )}
          {!block.streaming && block.usageTokens ? (
            <div className="usage-line">已完成 · {block.usageTokens} tokens</div>
          ) : null}
        </div>
      );

    case "tool":
      return (
        <div className="tool-block">
          <div className="tool-head">
            <span className="ticon" aria-hidden>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
                <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
              </svg>
            </span>
            <span className="tname">{block.name}</span>
            <span className="targs">{clip(shortArgs(block), 72)}</span>
          </div>
          {block.result !== undefined ? (
            <pre className="tool-result" data-error={block.isError ? "true" : undefined}>
              <span className="tool-cmdline">{prettyCmdline(block.name, block.arguments)}{"\n"}</span>
              {clip(block.result, 1400)}
            </pre>
          ) : (
            <span className="tool-open-hint">执行中…</span>
          )}
        </div>
      );

    case "confirm":
      return (
        <ApprovalCard
          command={prettyCommand(block.request)}
          cwd={prettyCwd(block.request)}
          resolved={block.resolved ?? null}
          autoFocus={!block.resolved}
          onDecide={(allow) => onConfirm(block.request.id, allow)}
        />
      );

    case "todo":
      return <TodoList items={block.items} />;

    case "error":
      return (
        <div className="error-block" data-aborted={block.aborted ? "true" : undefined}>
          {block.message}
        </div>
      );
  }
}

/** 极简 markdown 渲染：代码块/行内代码/段落（空行 = 段落间距，
 *  而不是 pre-wrap 的连续换行——那会产生双倍行高的怪异空白）。 */
function Markdownish({ text }: { text: string }) {
  const parts = text.split(/```/);
  return (
    <>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <pre key={i} className="tool-result">{part.replace(/^\w*\n/, "")}</pre>
        ) : (
          <Paragraphs key={i} text={part} />
        ),
      )}
    </>
  );
}

/** 正文按空行分段：段间用 margin（0.5em 段距），段内单换行保留。 */
function Paragraphs({ text }: { text: string }) {
  const paras = text.split(/\n{2,}/);
  return (
    <>
      {paras.map((p, i) =>
        i === 0 ? (
          <span key={i} className="md-text">{inlineCode(p)}</span>
        ) : (
          <span key={i} className="md-text md-para">{inlineCode(p)}</span>
        ),
      )}
    </>
  );
}

function inlineCode(text: string) {
  const segs = text.split(/`([^`]+)`/);
  return segs.map((s, i) =>
    i % 2 === 1 ? (
      <code key={i} style={{ fontFamily: "var(--font-mono)", fontSize: 12.5, background: "#f4f4f5", padding: "2px 5px", borderRadius: 4 }}>
        {s}
      </code>
    ) : (
      <span key={i}>{s}</span>
    ),
  );
}

/** 工具头部的参数摘要（bash 显示命令，别的显示 path/pattern 等首字段）。 */
function shortArgs(block: { name: string; arguments: string }): string {
  try {
    const a = JSON.parse(block.arguments);
    const first = a.command ?? a.path ?? a.pattern ?? a.cwd ?? "";
    return String(first);
  } catch {
    return block.arguments;
  }
}

/** 终端块首行：还原"看起来像命令"的那一行（bash 原样，read_file 拼成 cat）。 */
function prettyCmdline(name: string, args: string): string {
  try {
    const a = JSON.parse(args);
    switch (name) {
      case "bash":
        return a.command ?? "";
      case "read_file":
        return "cat " + (a.path ?? "");
      case "search":
        return "grep " + (a.pattern ?? "") + " " + (a.path ?? ".");
      case "write_file":
        return "write " + (a.path ?? "");
      case "edit":
        return "edit " + (a.path ?? "");
      default:
        return name;
    }
  } catch {
    return name;
  }
}

function prettyCommand(req: { arguments: string }): string {
  try {
    const a = JSON.parse(req.arguments);
    return a.command ?? req.arguments;
  } catch {
    return req.arguments;
  }
}

function prettyCwd(req: { arguments: string }): string | undefined {
  try {
    const a = JSON.parse(req.arguments);
    return a.cwd;
  } catch {
    return undefined;
  }
}

function clip(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max) + "…";
}
