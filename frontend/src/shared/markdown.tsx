// 完整的流式 markdown 渲染：块级（标题/列表/表格/引用/分割线/代码围栏）
// + 行内（加粗/斜体/删除线/行内代码/链接）。
//
// 流式纪律（用户拍板）：**不等闭合**——逐行解析、到达即渲染：
//   - 表格行到达就渲染（首行暂作表头，分隔线到达后确认对齐）
//   - 代码围栏开着就先渲染已有内容
//   - 未闭合的行内标记（如孤立 **）按原文显示，闭合符到达后自然成型
//   - 任何前缀的渲染结果与最终渲染保持前缀兼容（到达顺序无关）
//
// 性能纪律（对齐 blocks.tsx 的行级 memo）：块组件 memo 在序列化 src 上
// （字符串 primitive 比较），打字期间只有增长的尾块重渲染。
import { memo, type ReactNode } from "react";

// ---------- 描述符（纯数据，测试直接断言） ----------

/** 列表项：level = 缩进级数（2 空格一级）。 */
export interface MdListItem {
  level: number;
  text: string;
}

export type MdBlock =
  | { kind: "heading"; level: number; text: string }
  | { kind: "hr" }
  | { kind: "quote"; lines: string[] }
  | { kind: "list"; ordered: boolean; items: MdListItem[] }
  | { kind: "table"; lines: string[] }
  | { kind: "para"; text: string; gap: boolean };

const RE_HEADING = /^(#{1,6})\s+(.*)$/;
const RE_HR = /^(-{3,}|\*{3,}|_{3,})$/;
const RE_UL = /^(\s*)[-*+]\s+(.*)$/;
const RE_OL = /^(\s*)(\d+)[.)]\s+(.*)$/;
const RE_QUOTE = /^>\s?/;
const isTableRow = (l: string): boolean => l.trimStart().startsWith("|");

/** 块级解析：逐行扫描、连续同类行聚块。空行只产生段距标记（gap）。 */
export function parseBlocks(text: string): MdBlock[] {
  const lines = text.split("\n");
  const out: MdBlock[] = [];
  let gap = false;
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === "") {
      gap = true;
      i++;
      continue;
    }
    const h = RE_HEADING.exec(line);
    if (h) {
      out.push({ kind: "heading", level: h[1].length, text: h[2] });
      i++;
      continue;
    }
    if (RE_HR.test(line.trim())) {
      out.push({ kind: "hr" });
      gap = true; // 分割线是块级边界——后续段落带段距
      i++;
      continue;
    }
    if (RE_QUOTE.test(line)) {
      const ql: string[] = [];
      while (i < lines.length && RE_QUOTE.test(lines[i])) {
        ql.push(lines[i].replace(RE_QUOTE, ""));
        i++;
      }
      out.push({ kind: "quote", lines: ql });
      continue;
    }
    const ul = RE_UL.exec(line);
    const ol = RE_OL.exec(line);
    if (ul || ol) {
      const ordered = !!ol;
      const items: MdListItem[] = [];
      while (i < lines.length) {
        const m = ordered ? RE_OL.exec(lines[i]) : RE_UL.exec(lines[i]);
        if (!m) break; // 异类列表行 / 非列表行 → 开新块
        const indent = Math.floor(m[1].replace(/\t/g, "  ").length / 2);
        items.push({ level: indent, text: ordered ? m[3] : m[2] });
        i++;
      }
      out.push({ kind: "list", ordered, items });
      continue;
    }
    if (isTableRow(line)) {
      const tl: string[] = [];
      while (i < lines.length && lines[i].trim() !== "" && isTableRow(lines[i])) {
        tl.push(lines[i]);
        i++;
      }
      out.push({ kind: "table", lines: tl });
      continue;
    }
    out.push({ kind: "para", text: line, gap });
    gap = false;
    i++;
  }
  return out;
}

// ---------- 表格解析（流式：行到达即渲染） ----------

export interface MdTable {
  header: string[];
  aligns: ("left" | "center" | "right")[];
  body: string[][];
}

/** 单行 → 单元格数组（容忍缺尾竖线——流式截断的常态）。 */
export function splitRow(line: string): string[] {
  let s = line.trim();
  s = s.replace(/^\|/, "").replace(/\|$/, "");
  return s.split("|").map((c) => c.trim());
}

const isSeparatorRow = (cells: string[]): boolean =>
  cells.length > 0 && cells.every((c) => /^:?-{2,}:?$/.test(c));

const alignOf = (cell: string): "left" | "center" | "right" =>
  cell.startsWith(":") && cell.endsWith(":") ? "center" : cell.endsWith(":") ? "right" : "left";

/** 表格结构：首行恒为表头（流式期间暂作——分隔线到达即确认）；
 * 第二行是分隔行则解析对齐并跳过，其余为表体。 */
export function parseTable(lines: string[]): MdTable {
  const rows = lines.map(splitRow);
  let header = rows[0] ?? [];
  let aligns: ("left" | "center" | "right")[] = header.map(() => "left");
  let body: string[][] = rows.slice(1);
  if (rows.length >= 2 && isSeparatorRow(rows[1])) {
    aligns = rows[1].map(alignOf);
    body = rows.slice(2);
  }
  return { header, aligns, body };
}

// ---------- 行内解析（只认闭合对——未闭合按原文，流式安全） ----------

export type MdInline =
  | { t: "text"; v: string }
  | { t: "code"; v: string }
  | { t: "strong"; v: string }
  | { t: "em"; v: string }
  | { t: "del"; v: string }
  | { t: "link"; text: string; url: string };

// 匹配顺序即优先级：代码 > 加粗 > 删除线 > 链接 > 斜体（单星边界收紧
// 防乘号误伤：首尾字符都非空白）
const RE_INLINE =
  /`([^`]+)`|\*\*([^*]+)\*\*|~~([^~]+)~~|\[([^\]]+)\]\(([^)\s]+)\)|\*([^*\s](?:[^*]*[^*\s])?)\*/g;

export function parseInline(text: string): MdInline[] {
  const out: MdInline[] = [];
  let last = 0;
  for (const m of text.matchAll(RE_INLINE)) {
    if (m.index > last) out.push({ t: "text", v: text.slice(last, m.index) });
    if (m[1] !== undefined) out.push({ t: "code", v: m[1] });
    else if (m[2] !== undefined) out.push({ t: "strong", v: m[2] });
    else if (m[3] !== undefined) out.push({ t: "del", v: m[3] });
    else if (m[4] !== undefined) out.push({ t: "link", text: m[4], url: m[5] });
    else out.push({ t: "em", v: m[6] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ t: "text", v: text.slice(last) });
  return out;
}

/** 行内描述符 → ReactNode（链接渲染为带 URL 提示的文本——渲染层无
 * 导航面，IPC 桥边界只在窗口控制，诚实展示 URL 供复制）。 */
function renderInline(text: string): ReactNode {
  return parseInline(text).map((seg, i) => {
    switch (seg.t) {
      case "code":
        return <code key={i} className="md-code">{seg.v}</code>;
      case "strong":
        return <strong key={i} className="md-strong">{seg.v}</strong>;
      case "em":
        return <em key={i} className="md-em">{seg.v}</em>;
      case "del":
        return <del key={i} className="md-del">{seg.v}</del>;
      case "link":
        return (
          <span key={i} className="md-a" title={seg.url}>
            {seg.text}
          </span>
        );
      default:
        return <span key={i}>{seg.v}</span>;
    }
  });
}

// ---------- 渲染层（块组件 memo 在序列化 src 上——字符串 primitive） ----------

/** 正文入口：``` 围栏先切（围栏内不解析任何语法），其余走块级解析。 */
export function Markdown({ text }: { text: string }): ReactNode {
  const parts = text.split("```");
  return (
    <>
      {parts.map((part, i) =>
        i % 2 === 1 ? <CodeSeg key={i} text={part} /> : <TextBlocks key={i} text={part} />,
      )}
    </>
  );
}

/** 代码围栏：去语言行，其余原样（.tool-result 终端风格，样式沿用）。 */
const CodeSeg = memo(function CodeSeg({ text }: { text: string }) {
  return <pre className="tool-result md-seg">{text.replace(/^\w*\n/, "")}</pre>;
});

const TextBlocks = memo(function TextBlocks({ text }: { text: string }) {
  return (
    <>
      {parseBlocks(text).map((b, i) => {
        switch (b.kind) {
          case "heading":
            return <Heading key={i} level={b.level} text={b.text} />;
          case "hr":
            return <div key={i} className="md-hr md-seg" />;
          case "quote":
            return <QuoteBlock key={i} src={b.lines.join("\n")} />;
          case "list":
            return <ListBlock key={i} ordered={b.ordered} src={JSON.stringify(b.items)} />;
          case "table":
            return <TableBlock key={i} src={b.lines.join("\n")} />;
          default:
            return <Line key={i} text={b.text} para={b.gap} />;
        }
      })}
    </>
  );
});

const Heading = memo(function Heading({ level, text }: { level: number; text: string }) {
  return (
    <p className={"md-h md-h" + level + " md-seg"}>{renderInline(text)}</p>
  );
});

const QuoteBlock = memo(function QuoteBlock({ src }: { src: string }) {
  return (
    <blockquote className="md-quote md-seg">
      {src.split("\n").map((l, i) => (
        <p key={i} className="md-quote-line">{renderInline(l)}</p>
      ))}
    </blockquote>
  );
});

const ListBlock = memo(function ListBlock({ ordered, src }: { ordered: boolean; src: string }) {
  const items: MdListItem[] = JSON.parse(src);
  return (
    <div className="md-list md-seg">
      {items.map((it, i) => (
        <div key={i} className="md-li" style={{ paddingLeft: it.level * 14 }}>
          <span className="md-li-marker" aria-hidden>
            {ordered ? `${i + 1}.` : "·"}
          </span>
          <span className="md-li-text">{renderInline(it.text)}</span>
        </div>
      ))}
    </div>
  );
});

const TableBlock = memo(function TableBlock({ src }: { src: string }) {
  const { header, aligns, body } = parseTable(src.split("\n"));
  const align = (i: number) => aligns[i] ?? "left";
  return (
    <div className="md-table-wrap md-seg">
      <table className="md-table">
        <thead>
          <tr>
            {header.map((cell, i) => (
              <th key={i} style={{ textAlign: align(i) }}>{renderInline(cell)}</th>
            ))}
          </tr>
        </thead>
        {body.length > 0 && (
          <tbody>
            {body.map((row, r) => (
              <tr key={r}>
                {row.map((cell, i) => (
                  <td key={i} style={{ textAlign: align(i) }}>{renderInline(cell)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        )}
      </table>
    </div>
  );
});

/** 段落行（原有形态不变：pre-wrap 保留段内换行，空行后段距）。 */
const Line = memo(function Line({ text, para }: { text: string; para: boolean }) {
  return (
    <p className={"md-text md-seg" + (para ? " md-para" : "")}>{renderInline(text)}</p>
  );
});
