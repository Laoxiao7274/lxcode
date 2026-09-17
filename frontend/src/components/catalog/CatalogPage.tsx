// 目录管理页：工具 / 技能 / 模板 三类可插拔目录的浏览与查看。
// 页签切目录（Segmented），条目卡网格；点卡片直接开文档弹窗（浏览场景
// 没有组装语境，不需要编辑器的紧凑面板层级）。自定义条目的创建与
// 编辑随目录状态化（后端化第二步）落地。
import { useEffect, useRef, useState, type ReactNode } from "react";
import { BUILTIN_TOOLS, CONTEXT_MODULES, THIRD_PARTY_TOOLS, type ToolSpec } from "../../shared/agents";
import { staggerIn } from "../../shared/motion";
import { Segmented } from "../form";
import { DocDialog, type Focus } from "./DocDialog";

type Tab = "tools" | "skills" | "templates";

/** 条目卡：标题行（mono id + pills）+ 摘要；工具带参数计数。 */
function EntryCard({ onClick, id, desc, pills, meta }: {
  onClick: () => void;
  id: string;
  desc: string;
  pills: ReactNode;
  meta?: string;
}) {
  return (
    <button type="button" className="cg-card" onClick={onClick} title="点击查看完整文档">
      <div className="cg-card-top">
        <span className="cg-card-title">{id}</span>
        <span className="cg-card-pills">{pills}</span>
      </div>
      <div className="cg-card-desc">{desc}</div>
      {meta && <div className="cg-card-meta">{meta}</div>}
    </button>
  );
}

function ToolCard({ tool, onOpen }: { tool: ToolSpec; onOpen: () => void }) {
  return (
    <EntryCard
      onClick={onOpen}
      id={tool.id}
      desc={tool.desc}
      pills={
        <>
          <span className={"ag-pill " + (tool.risk === "high" ? "risk-high" : "risk-low")}>
            {tool.risk === "high" ? "高危" : "低危"}
          </span>
          <span className="ag-pill src">
            {tool.source === "builtin" ? "内置" : tool.source === "binary" ? "外部二进制" : "MCP"}
          </span>
        </>
      }
      meta={tool.params && tool.params.length > 0 ? `${tool.params.length} 参数` : undefined}
    />
  );
}

export function CatalogPage() {
  const [tab, setTab] = useState<Tab>("tools");
  const [focus, setFocus] = useState<Focus | null>(null);
  const gridRef = useRef<HTMLDivElement>(null);

  // 页签切换重播条目交错入场（首次挂载也走这里）
  useEffect(() => {
    const el = gridRef.current;
    if (!el) return;
    staggerIn(el.querySelectorAll(".cg-card"), { each: 0.04 });
  }, [tab]);

  const tools = [...BUILTIN_TOOLS, ...THIRD_PARTY_TOOLS];
  const skills = CONTEXT_MODULES.filter((m) => m.kind === "skill");
  const templates = CONTEXT_MODULES.filter((m) => m.kind === "process");

  const tabs = [
    { value: "tools" as const, label: `工具 · ${tools.length}`, hint: "内置与第三方工具" },
    { value: "skills" as const, label: `技能 · ${skills.length}`, hint: "领域知识与方法（多选注入）" },
    { value: "templates" as const, label: `模板 · ${templates.length}`, hint: "工作方式模板（单选注入）" },
  ];

  return (
    <div className="cg-page">
      <div className="ag-head">
        <div>
          <div className="ag-title">目录</div>
          <div className="ag-sub">可插拔的能力目录——工具、技能与模板；Agent 组装时从这里勾选注入</div>
        </div>
      </div>
      <div className="cg-tabs">
        <Segmented options={tabs} value={tab} onChange={setTab} ariaLabel="目录页签" />
      </div>
      <div className="cg-grid" ref={gridRef}>
        {tab === "tools" && tools.map((t) => (
          <ToolCard key={t.id} tool={t} onOpen={() => setFocus({ kind: "tool", id: t.id })} />
        ))}
        {tab === "skills" && skills.map((m) => (
          <EntryCard
            key={m.id}
            onClick={() => setFocus({ kind: "module", id: m.id })}
            id={m.id}
            desc={m.desc}
            pills={<span className="ag-pill src">技能</span>}
          />
        ))}
        {tab === "templates" && templates.map((m) => (
          <EntryCard
            key={m.id}
            onClick={() => setFocus({ kind: "module", id: m.id })}
            id={m.id}
            desc={m.desc}
            pills={<span className="ag-pill src">模板</span>}
          />
        ))}
      </div>
      {focus && <DocDialog focus={focus} onClose={() => setFocus(null)} />}
    </div>
  );
}
