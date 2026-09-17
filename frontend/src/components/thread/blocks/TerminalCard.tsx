// 终端卡（lx-dsh primitives/TerminalBlock 全量对齐）：
// banner = [StateDot + 状态标签] [cwd段名 $ 命令行（多行续行只有 $）]
//           [退出码 pill（非零时红）] [复制按钮]；
// 输出 = ANSI 颜色行 + head-tail 折叠（超 16 行显示头 8 + 尾 8 +
// 中间展开按钮——DSH 的 headTailCap 同款）。
// 非 bash 工具也用同一张卡：命令行按工具语义还原（cat/grep 等）。
import { useMemo, useState } from "react";
import { toolCmdline, cwdSegment } from "./terminal-helpers";

/** ANSI SGR → 样式（常见 8 色 + 亮色 + 粗体；够覆盖 ls/git/go test 输出）。 */
const SGR_COLORS: Record<number, string> = {
  30: "#4a4a4a", 31: "#d0342c", 32: "#157f3d", 33: "#9a6a00",
  34: "#2057c9", 35: "#8c37c9", 36: "#0f7e8c", 37: "#5a5a5a",
  90: "#8a8a8a", 91: "#ff6b62", 92: "#37b45f", 93: "#c9a227",
  94: "#5f8fe0", 95: "#b368e0", 96: "#3fb4c1", 97: "#8a8a8a",
};

interface Span {
  text: string;
  color?: string;
  bold?: boolean;
}

/** 解析一行 ANSI（转义序列切分成带样式的 span；无样式行返回裸文本数组）。 */
function parseAnsiLine(line: string): Span[] {
  if (!line.includes("\u001b[")) return [{ text: line }];
  const spans: Span[] = [];
  let color: string | undefined;
  let bold = false;
  for (const part of line.split(/\u001b\[([0-9;]*)m/)) {
    if (part === "") continue;
    // 分隔符段（参数）与文本段交替出现：奇数次是参数
    if (/^[0-9;]*$/.test(part) && spans.length % 2 === 0) {
      for (const code of part.split(";").map(Number)) {
        if (SGR_COLORS[code]) color = SGR_COLORS[code];
        else if (code === 1) bold = true;
        else if (code === 0 || code === 39) { color = undefined; bold = false; }
      }
      continue;
    }
    if (part) spans.push({ text: part, color, bold });
  }
  return spans.length ? spans : [{ text: line }];
}

/** head-tail 折叠几何（DSH headTailCap 同款：头 ceil(N/2) + 尾 N-头）。 */
function headTailCap(total: number, max: number, expanded: boolean) {
  const hidden = total - max;
  const capped = hidden > 0 && !expanded;
  const head = Math.ceil(max / 2);
  return { hidden, capped, headLines: head, tailLines: max - head };
}

export function TerminalCard({ command, cwd, toolName, toolArgs, output, running, isError }: {
  command: string;
  cwd?: string;
  toolName: string;
  toolArgs: string;
  output?: string;
  running: boolean;
  isError?: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);

  // 命令行（工具语义还原）+ 多行拆分（DSH：多行命令逐行渲染，首行带 cwd）
  const cmdline = useMemo(() => toolCmdline(toolName, toolArgs, command), [toolName, toolArgs, command]);
  const commandLines = useMemo(
    () => (cmdline.endsWith("\n") ? cmdline.slice(0, -1) : cmdline).split("\n").filter((l) => l !== "" || true),
    [cmdline],
  );

  // 输出行（去尾空行——DSH 同款）+ ANSI 解析
  const lines = useMemo(() => {
    const text = output ?? "";
    const parsed = text.split("\n");
    if (parsed.length > 1 && parsed[parsed.length - 1].trim() === "") parsed.pop();
    return parsed;
  }, [output]);
  const parsedLines = useMemo(() => lines.map(parseAnsiLine), [lines]);
  const empty = lines.every((l) => l.trim() === "") && !running;

  const { hidden, capped, headLines, tailLines } = headTailCap(lines.length, 16, expanded);
  const prompt = cwdSegment(cwd);

  const onCopy = () => {
    if (copied) return;
    void navigator.clipboard?.writeText(output ?? "").then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1000);
    });
  };

  // 状态（DSH runState：running=ongoing 蓝 / isError=error 红 / 否则 done 绿点级）
  const stateLabel = running ? "运行中" : isError ? "失败" : "完成";
  const stateClass = running ? "run" : isError ? "err" : "ok";

  return (
    <div className="tterm" data-running={running ? "" : undefined}>
      {/* banner：[状态点+标签] [cwd $ 命令] [退出码 pill] [复制] */}
      <div className="tterm-header">
        <div className="tterm-prompt">
          <span className="sr-only">{stateLabel}</span>
          {commandLines.map((line, i) => (
            <div className="tterm-prompt-line" key={i}>
              {i === 0 && <span className={"sdot " + stateClass} aria-hidden />}
              <span className="tterm-cwd">{i > 0 || prompt === undefined ? "$" : prompt + " $"}</span>
              <span className="tterm-command">{line}</span>
            </div>
          ))}
        </div>
        {isError && !running && <span className="tterm-pill err">非零退出</span>}
        {!running && !empty && (
          <button type="button" className="tterm-copy" onClick={onCopy}>
            {copied ? "已复制" : "复制"}
          </button>
        )}
      </div>
      {/* 输出：ANSI 行 + head-tail 折叠 */}
      {!running && (empty ? (
        <div className="tterm-empty">（无输出）</div>
      ) : (
        <div className="tterm-output">
          {(capped ? parsedLines.slice(0, headLines) : parsedLines).map((line, i) => (
            <div className="tterm-line" key={i}>
              {line.map((span, j) =>
                span.color || span.bold
                  ? <span key={j} style={{ color: span.color, fontWeight: span.bold ? 600 : undefined }}>{span.text}</span>
                  : <span key={j}>{span.text}</span>,
              )}
            </div>
          ))}
          {hidden > 0 && (
            <button type="button" className="tterm-expand" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
              {expanded ? "收起" : `展开其余 ${hidden} 行`}
            </button>
          )}
          {capped && parsedLines.slice(parsedLines.length - tailLines).map((line, i) => (
            <div className="tterm-line" key={"t" + i}>
              {line.map((span, j) =>
                span.color || span.bold
                  ? <span key={j} style={{ color: span.color, fontWeight: span.bold ? 600 : undefined }}>{span.text}</span>
                  : <span key={j}>{span.text}</span>,
              )}
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}
