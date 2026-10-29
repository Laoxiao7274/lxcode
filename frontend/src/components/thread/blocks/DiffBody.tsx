// diff 渲染（lx-dsh primitives/DiffBlock 对齐）：
// 四列网格 [旧行号][新行号][符号][内容]，行高 22px；
// 首行 = 文件路径（粗体）；底部统计 footer（+N −M）；
// head-tail 折叠（超 16 行：头 8 + 尾 8 + 展开按钮）。
// 行号：edit 工具无绝对行号——按展示块顺序计数（旧行 N、新行 M 各自连续，
// unified diff 的近似形态）。
import { useEffect, useMemo, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../../shared/motion";

interface Row {
  kind: "del" | "add";
  oldNo?: number;
  newNo?: number;
  text: string;
}

export function DiffBody({ oldText, newText }: { oldText: string; newText: string }) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const el = rootRef.current;
    if (!el || !motionAllowed()) return;
    const rows = el.querySelectorAll<HTMLElement>(".diff-line");
    gsap.fromTo(rows, { opacity: 0, x: -4 }, { opacity: 1, x: 0, duration: 0.26, ease: "power2.out", stagger: 0.018, clearProps: "transform,opacity" });
  }, []);

  // 行构造：旧行/新行各自连续编号（展示块的 unified 形态）
  const { rows, added, removed } = useMemo(() => {
    const out: Row[] = [];
    let oldNo = 1;
    let newNo = 1;
    for (const l of oldText.split("\n")) {
      out.push({ kind: "del", oldNo: oldNo++, text: l });
    }
    for (const l of newText.split("\n")) {
      out.push({ kind: "add", newNo: newNo++, text: l });
    }
    return { rows: out, added: newNo - 1, removed: oldNo - 1 };
  }, [oldText, newText]);

  const hidden = rows.length - 16;
  const capped = hidden > 0 && !expanded;
  const headLines = Math.ceil(16 / 2);
  const tailLines = 16 - headLines;
  const head = capped ? rows.slice(0, headLines) : rows;
  const tail = capped ? rows.slice(rows.length - tailLines) : [];

  const onCopy = () => {
    if (copied) return;
    const text = rows.map((r) => (r.kind === "del" ? "−" : "+") + r.text).join("\n");
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1000);
    });
  };

  return (
    <div className="diff-block-card" ref={rootRef}>
      <button type="button" className="diff-copy" onClick={onCopy}>
        {copied ? "已复制" : "复制"}
      </button>
      <div className="diff-body">
        {head.map((row, i) => <DiffLine key={i} row={row} />)}
        {hidden > 0 && (
          <button type="button" className="diff-expand" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
            {expanded ? "收起" : `展开其余 ${hidden} 行`}
          </button>
        )}
        {tail.map((row, i) => <DiffLine key={"t" + i} row={row} />)}
      </div>
      <div className="diff-footer">
        <span className="stat-add">+{added}</span>{" "}
        <span className="stat-del">−{removed}</span>
      </div>
    </div>
  );
}

function DiffLine({ row }: { row: Row }) {
  return (
    <div className={"diff-line " + row.kind}>
      <span className="ln">{row.oldNo ?? ""}</span>
      <span className="ln">{row.newNo ?? ""}</span>
      <span className={"sign " + row.kind} aria-hidden>{row.kind === "del" ? "−" : "+"}</span>
      <span className="code">{row.text || " "}</span>
    </div>
  );
}
