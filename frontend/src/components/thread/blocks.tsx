// 对话渲染块：user 气泡 / assistant 回复（打字机 + 轻量 markdown）/
// 工具卡 / 确认卡 / 任务清单 / 错误条。
// Block 用 memo：reduce 只给变化的块换新引用，未动的兄弟块跳过协调；
// 配合稳定的 onConfirm（useAgent/App 的 useCallback）生效。
import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { ThreadBlock } from "../../shared/store";
import type { FileChange } from "../../shared/types";
import { gsap } from "gsap";
import { ThinkingReasoning } from "../../aicss/ThinkingReasoning";
import { TodoList } from "../../aicss/TodoList";
import { ApprovalCard } from "../../aicss/ApprovalCard";
import { TextResponse } from "../../aicss/TextResponse";
import { useStreamReveal } from "../../shared/stream-reveal";
import { playEnter } from "../../shared/anim";
import { motionAllowed, staggerIn } from "../../shared/motion";
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

    case "files":
      return <FilesCard files={block.files} />;

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

/** 工具卡：挂载上浮淡入；结果到达（执行中→终端块）再淡入一次。
 *  edit/write_file 渲染代码 diff（Codex 验收形态）；其他工具保持终端块。 */
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

  // edit 工具：diff 视图（改动即所见——不再让用户读 JSON 参数）
  const editDiff = useMemo((): { path: string; old_string: string; new_string: string } | null => {
    if (block.name !== "edit" || !block.arguments) return null;
    try {
      const a = JSON.parse(block.arguments) as Partial<{ path: string; old_string: string; new_string: string }>;
      if (!a.path || a.old_string === undefined || a.new_string === undefined) return null;
      return { path: a.path, old_string: a.old_string, new_string: a.new_string };
    } catch {
      return null;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [block.arguments]);

  if (editDiff && block.name === "edit") {
    return (
      <div className="tool-block diff-block" ref={rootRef}>
        <div className="diff-file-head">
          <span className="ticon" aria-hidden>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
            </svg>
          </span>
          <span className="tname">{editDiff.path}</span>
          <span className="diff-stats">
            <span className="stat-badge stat-del">−{editDiff.old_string.split("\n").length}</span>
            <span className="stat-badge stat-add">+{editDiff.new_string.split("\n").length}</span>
          </span>
          {block.result === undefined && <span className="tool-open-hint">执行中…</span>}
        </div>
        {block.result !== undefined && (
          <DiffBody oldText={editDiff.old_string} newText={editDiff.new_string} />
        )}
      </div>
    );
  }

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

/** diff 渲染：GitHub 级视觉（行号列 + 左色条 + 淡底色 + hover）；
 *  行交错淡入（gsap stagger，对齐 .md-seg 的逐块揭示节奏）。 */
function DiffBody({ oldText, newText }: { oldText: string; newText: string }) {
  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = rootRef.current;
    if (!el || !motionAllowed()) return;
    const rows = el.querySelectorAll<HTMLElement>(".diff-line");
    gsap.fromTo(rows, { opacity: 0, x: -4 }, { opacity: 1, x: 0, duration: 0.26, ease: "power2.out", stagger: 0.018, clearProps: "transform,opacity" });
  }, []);

  const oldLines = oldText.split("\n");
  const newLines = newText.split("\n");
  let ln = 310; // 行号从演示剧本的上下文起（视觉真实感）
  return (
    <div className="diff-body" ref={rootRef}>
      {oldLines.map((l, i) => (
        <div key={"o" + i} className="diff-line del">
          <span className="ln" aria-hidden>{ln + i}</span>
          <span className="sign" aria-hidden>−</span>
          <span className="code">{l || " "}</span>
        </div>
      ))}
      {newLines.map((l, i) => (
        <div key={"n" + i} className="diff-line add">
          <span className="ln" aria-hidden>{ln + oldLines.length + i}</span>
          <span className="sign" aria-hidden>+</span>
          <span className="code">{l || " "}</span>
        </div>
      ))}
    </div>
  );
}

/** 产物汇总卡：一轮任务的改动文件列表 + 增删统计 + 展开看 diff
 *  （Codex 的 artifact viewer）。文件展开走 gsap 高度动画（WorkGroup 同款），
 *  挂载 playEnter，行交错浮现。 */
function FilesCard({ files }: { files: FileChange[] }) {
  const [open, setOpen] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const headRowRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    playEnter(rootRef.current, { y: 10, opacity: 0 }, { y: 0, opacity: 1, duration: 0.34, ease: "power2.out", clearProps: "transform,opacity" });
    const rows = rootRef.current?.querySelectorAll<HTMLElement>(".files-file");
    if (rows && motionAllowed()) {
      gsap.fromTo(rows, { opacity: 0, y: 5 }, { opacity: 1, y: 0, duration: 0.24, ease: "power2.out", stagger: 0.05, delay: 0.08, clearProps: "transform,opacity" });
    }
    staggerIn(headRowRef.current ? [headRowRef.current] : [], {});
  }, []);

  const added = files.reduce((n, f) => n + f.added, 0);
  const deleted = files.reduce((n, f) => n + f.deleted, 0);

  return (
    <div className="files-card" ref={rootRef}>
      <div className="files-head" ref={headRowRef}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Z" />
          <path d="M13 2v7h7" />
        </svg>
        <span className="files-title">改动 {files.length} 个文件</span>
        <span className="diff-stats">
          <span className="stat-badge stat-del">−{deleted}</span>
          <span className="stat-badge stat-add">+{added}</span>
        </span>
      </div>
      {files.map((f) => (
        <FilesRow key={f.path} file={f} open={open === f.path} onToggle={() => setOpen(open === f.path ? null : f.path)} />
      ))}
    </div>
  );
}

/** 单文件行 + 展开动画（gsap 高度补间，WorkGroup 同款纪律：
 *  收起保持 height:0，展开收尾 clearProps 让内容自然伸展）。 */
function FilesRow({ file, open, onToggle }: { file: FileChange; open: boolean; onToggle: () => void }) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const firstRun = useRef(true);

  useEffect(() => {
    const el = bodyRef.current;
    if (!el) return;
    const first = firstRun.current;
    firstRun.current = false;
    const COLLAPSED = { height: 0, opacity: 0, overflow: "hidden" };
    if (first || !motionAllowed()) {
      if (!open) gsap.set(el, COLLAPSED);
      else if (!first) gsap.set(el, { clearProps: "height,opacity,overflow" });
      return;
    }
    if (open) {
      gsap.set(el, { overflow: "hidden" });
      gsap.fromTo(el, { height: 0, opacity: 0 }, {
        height: "auto", opacity: 1, duration: 0.3, ease: "power2.out",
        onComplete: () => gsap.set(el, { clearProps: "height,opacity,overflow" }),
      });
      const rows = el.querySelectorAll<HTMLElement>(".diff-line");
      gsap.fromTo(rows, { opacity: 0, x: -4 }, { opacity: 1, x: 0, duration: 0.24, ease: "power2.out", stagger: 0.014, delay: 0.05, clearProps: "transform,opacity" });
    } else {
      gsap.to(el, { ...COLLAPSED, duration: 0.22, ease: "power2.in" });
    }
  }, [open]);

  return (
    <div className="files-row">
      <button type="button" className="files-file" aria-expanded={open} onClick={onToggle}>
        <span className="files-chevron">{open ? "▾" : "▸"}</span>
        <span className="files-path">{file.path}</span>
        <span className="diff-stats">
          <span className="stat-badge stat-del">−{file.deleted}</span>
          <span className="stat-badge stat-add">+{file.added}</span>
        </span>
      </button>
      <div className="files-diff-wrap" ref={bodyRef}>
        <div className="diff-body">
          {file.diff.split("\n").map((l, i) => (
            <div
              key={i}
              className={"diff-line" + (l.startsWith("+") ? " add" : l.startsWith("-") && !l.startsWith("---") ? " del" : "")}
            >
              <span className="ln" aria-hidden>{l.startsWith("@") ? "@" : ""}</span>
              <span className="sign" aria-hidden>{l.startsWith("+") ? "+" : l.startsWith("-") ? "−" : ""}</span>
              <span className="code">{l || " "}</span>
            </div>
          ))}
        </div>
      </div>
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
