// 对话渲染块：user 气泡 / assistant 回复（打字机 + 轻量 markdown）/
// 工具卡 / 确认卡 / 任务清单 / 错误条。
// Block 用 memo：reduce 只给变化的块换新引用，未动的兄弟块跳过协调；
// 配合稳定的 onConfirm（useAgent/App 的 useCallback）生效。
import { memo, useEffect, useMemo, useRef, type ReactNode } from "react";
import type { ThreadBlock } from "../../shared/store";
import { ThinkingReasoning } from "../../aicss/ThinkingReasoning";
import { TodoList } from "../../aicss/TodoList";
import { ApprovalCard } from "../../aicss/ApprovalCard";
import { TextResponse } from "../../aicss/TextResponse";
import { useStreamReveal } from "../../shared/stream-reveal";
import { playEnter } from "../../shared/anim";
import { useSettings } from "../../shared/settings";
import { clip, shortArgs, prettyCmdline, prettyCommand, prettyCwd } from "./helpers";

export const Block = memo(function Block({ block, onConfirm }: { block: ThreadBlock; onConfirm: (id: string, allow: boolean) => void }) {
  const bubbleRef = useRef<HTMLDivElement>(null);
  const { settings } = useSettings();

  // 用户气泡入场：从 composer 方向的明显回弹（back.out），配合缓动滚底
  // 形成"发送出去"的手感
  useEffect(() => {
    if (block.kind === "user") {
      playEnter(
        bubbleRef.current,
        { y: 14, opacity: 0, scale: 0.93 },
        { y: 0, opacity: 1, scale: 1, duration: 0.5, ease: "back.out(1.8)", transformOrigin: "right bottom", clearProps: "transform" },
      );
    }
  }, [block.kind]);

  // 思考句数组只在 reasoning 字符串真的变了才重切——正文打字机流式阶段
  // 该块每帧都重渲染（块对象每 delta 换新），不锁字符串就每帧 split 一遍
  const reasoningText = block.kind === "assistant" ? block.reasoning : "";
  const sentences = useMemo(
    () => (reasoningText ? reasoningText.split("\n").filter(Boolean) : null),
    [reasoningText],
  );

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
          {settings.showThinking && sentences && (
            <ThinkingReasoning
              sentences={sentences}
              phase={block.streaming ? "thinking" : "done"}
              elapsedMs={4200}
            />
          )}
          {block.content === "" ? null : <AnswerBody text={block.content} streaming={block.streaming} />}
          {!block.streaming && block.usageTokens ? (
            <div className="usage-line">已完成 · {block.usageTokens} tokens</div>
          ) : null}
        </div>
      );

    case "tool":
      return <ToolBlock block={block} />;

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
});

/** 工具卡：挂载上浮淡入；结果到达（执行中→终端块）再淡入一次，
 *  不再瞬间顶出。 */
function ToolBlock({ block }: { block: Extract<ThreadBlock, { kind: "tool" }> }) {
  const rootRef = useRef<HTMLDivElement>(null);
  const resultRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    playEnter(rootRef.current);
  }, []);

  const hadResult = useRef(block.result !== undefined);
  useEffect(() => {
    if (hadResult.current || block.result === undefined) return;
    hadResult.current = true;
    playEnter(resultRef.current, { opacity: 0 }, { opacity: 1, duration: 0.3, ease: "power2.out", clearProps: "opacity" });
  }, [block.result]);

  // JSON 解析只做一次/参数变化（memo(Block) 已挡掉大部分重渲染）
  const { argsSummary, cmdline } = useMemo(
    () => ({ argsSummary: clip(shortArgs(block), 72), cmdline: prettyCmdline(block.name, block.arguments) }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [block.name, block.arguments],
  );

  return (
    <div className="tool-block" ref={rootRef}>
      <div className="tool-head">
        <span className="ticon" aria-hidden>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
            <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
          </svg>
        </span>
        <span className="tname">{block.name}</span>
        <span className="targs">{argsSummary}</span>
      </div>
      {block.result !== undefined ? (
        <pre ref={resultRef} className="tool-result" data-error={block.isError ? "true" : undefined}>
          <span className="tool-cmdline">{cmdline}{"\n"}</span>
          {clip(block.result, 1400)}
        </pre>
      ) : (
        <span className="tool-open-hint">执行中…</span>
      )}
    </div>
  );
}

/** 正文输出：DSH 同款打字机缓冲（useStreamReveal）——新到字符按 0.35s
 *  浇注窗口匀速流出（30~1000 字/秒自适应积压），done 后缓冲继续排空。
 *  揭示前缀实时走 Markdownish：围栏/行内代码跟着逐块淡入。 */
function AnswerBody({ text, streaming }: { text: string; streaming: boolean }) {
  const shown = useStreamReveal(text);
  return (
    <TextResponse>
      <Markdownish text={shown} />
      {streaming && <span className="md-caret" aria-hidden />}
    </TextResponse>
  );
}

/** 轻量 markdown 渲染（流式期间同样实时）：``` 围栏→暗色终端块；
 *  每行一个块级段落——与思考句同款的逐块挂载 420ms 淡入（.md-seg）。
 *  打字机每帧只有尾行文本变化：行/代码段全部 memo 化，稳定块跳过解析与协调。 */
function Markdownish({ text }: { text: string }) {
  const parts = text.split(/```/);
  return (
    <>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <CodeSeg key={i} text={part} />
        ) : (
          <Paragraphs key={i} text={part} />
        ),
      )}
    </>
  );
}

const CodeSeg = memo(function CodeSeg({ text }: { text: string }) {
  return <pre className="tool-result md-seg">{text.replace(/^\w*\n/, "")}</pre>;
});

/** 正文逐行成块：空行=段距（md-para），行内代码照常解析。
 *  每块首次挂载播放 md-seg-in——已有行只是文本增长，不重挂载，不会重复动画。 */
function Paragraphs({ text }: { text: string }) {
  const lines = text.split("\n");
  const out: ReactNode[] = [];
  let gap = false;
  let any = false;
  lines.forEach((line, i) => {
    if (line.trim() === "") {
      gap = true;
      return;
    }
    out.push(<Line key={i} text={line} para={any && gap} />);
    any = true;
    gap = false;
  });
  return <>{out}</>;
}

const Line = memo(function Line({ text, para }: { text: string; para: boolean }) {
  return <p className={"md-text md-seg" + (para ? " md-para" : "")}>{inlineCode(text)}</p>;
});

const CODE_STYLE = { fontFamily: "var(--font-mono)", fontSize: 12.5, background: "#f4f4f5", padding: "2px 5px", borderRadius: 4 } as const;

function inlineCode(text: string) {
  const segs = text.split(/`([^`]+)`/);
  return segs.map((s, i) =>
    i % 2 === 1 ? (
      <code key={i} style={CODE_STYLE}>
        {s}
      </code>
    ) : (
      <span key={i}>{s}</span>
    ),
  );
}
