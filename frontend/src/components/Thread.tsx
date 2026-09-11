import { useEffect, useRef } from "react";
import type { UIState, ThreadBlock } from "../agent/store";
import { ThinkingReasoning } from "../aicss/ThinkingReasoning";
import { ThinkingState } from "../aicss/ThinkingState";
import { TodoList } from "../aicss/TodoList";
import { ApprovalCard } from "../aicss/ApprovalCard";
import { TextResponse } from "../aicss/TextResponse";
import { StreamingText } from "../aicss/StreamingText";

const SUGGESTIONS = [
  "把工具循环加上单工具超时兜底，超时不中断整轮",
  "读 config/local.json，告诉我 default 绑定的模型",
  "跑一遍全量测试，有红的修掉",
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

  // 流式期间跟随滚动（用户上翻时不硬拽——仅当已贴底时跟随）
  useEffect(() => {
    const el = endRef.current?.parentElement;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 160;
    if (atBottom) endRef.current?.scrollIntoView({ block: "end" });
  }, [state.blocks]);

  if (state.blocks.length === 0) {
    return (
      <div className="empty-state">
        <div className="glyph">myt-harness</div>
        <h2>给智能体一个任务</h2>
        <p>它在本机读写代码、改文件、跑命令；高危操作会先征求你的同意。</p>
        <div className="suggest-row">
          {SUGGESTIONS.map((s) => (
            <button key={s} type="button" className="suggest-chip" onClick={() => onSuggestion?.(s)}>
              {s}
            </button>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="thread">
      {state.blocks.map((b, i) => (
        <Block key={i} block={b} onConfirm={onConfirm} />
      ))}
      {/* 进行中且还没有任何输出时显示思考 shimmer */}
      {state.busy && !lastIsStreamingAssistant(state.blocks) && (
        <div className="msg">
          <div className="msg-role assistant">myt-harness</div>
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

function Block({ block, onConfirm }: { block: ThreadBlock; onConfirm: (id: string, allow: boolean) => void }) {
  switch (block.kind) {
    case "user":
      return (
        <div className="msg user">
          <div className="resp">{block.text}</div>
        </div>
      );

    case "assistant":
      return (
        <div className="msg">
          <div className="msg-role assistant">myt-harness</div>
          {block.reasoning && (
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
            <span className="tname">{block.name}</span>
            <span className="targs">{clip(shortArgs(block), 72)}</span>
          </div>
          {block.result !== undefined ? (
            <pre className="tool-result" data-error={block.isError ? "true" : undefined}>
              <span className="tool-cmdline">{prettyCmdline(block.name, block.arguments)}</span>
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

/** 极简 markdown 渲染：代码块/行内代码/换行（原型够用；生产换 markdown 引擎）。 */
function Markdownish({ text }: { text: string }) {
  const parts = text.split(/```/);
  return (
    <>
      {parts.map((part, i) =>
        i % 2 === 1 ? (
          <pre key={i} className="tool-result">{part.replace(/^\w*\n/, "")}</pre>
        ) : (
          <span key={i} style={{ whiteSpace: "pre-wrap" }}>{inlineCode(part)}</span>
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
