// 紧凑详情面板（从 AgentEditor 拆出——只读展示组件，消费 agents 域）：
// 跟随 chip 焦点（选中与查看的快速通道）；点整卡打开完整文档弹窗——
// 预览截断，长文进弹窗（DocDialog）。挂载入场 0.2s 上浮。
import { useAgents } from "../../shared/agents";
import { Markdown } from "../../shared/markdown";
import { useEnterRef } from "../../shared/anim";
import type { Focus } from "../catalog/DocDialog";

/** 面板底部的展开提示（hover 点亮）。 */
function OpenHint() {
  return (
    <div className="ag-detail-open-hint">
      <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M15 3h6v6" />
        <path d="M21 3l-9 9" />
        <path d="M9 21H3v-6" />
        <path d="M3 21l9-9" />
      </svg>
      点击查看完整文档
    </div>
  );
}

export function DetailPanel({ focus, onOpen }: { focus: Focus | null; onOpen: () => void }) {
  const enterRef = useEnterRef<HTMLDivElement>(
    { opacity: 0, y: 4 },
    { opacity: 1, y: 0, duration: 0.2, ease: "power2.out", clearProps: "transform,opacity" },
  );
  const { modules, tools } = useAgents();
  const mod = focus?.kind === "module" ? modules.find((m) => m.id === focus.id) : undefined;
  const tool = focus?.kind === "tool" ? tools.find((t) => t.id === focus.id) : undefined;

  if (tool) {
    return (
      <div
        className="ag-detail"
        key={"t" + tool.id}
        ref={enterRef}
        role="button"
        tabIndex={0}
        title="点击查看完整文档"
        onClick={onOpen}
        onKeyDown={(e) => e.key === "Enter" && onOpen()}
      >
        <div className="ag-detail-title">{tool.id}</div>
        <div className="ag-detail-pills">
          <span className={"ag-pill " + (tool.risk === "high" ? "risk-high" : "risk-low")}>
            {tool.risk === "high" ? "高危" : "低危"}
          </span>
          <span className="ag-pill src">
            {tool.source === "builtin" ? "内置" : tool.source === "binary" ? "外部二进制" : "MCP"}
          </span>
        </div>
        <div className="ag-detail-desc">{tool.desc}</div>
        {tool.params && tool.params.length > 0 && (
          <div className="ag-detail-params">
            <div className="ag-sec-label">参数 · {tool.params.length}</div>
            {tool.params.map((p) => (
              <div className="ag-param" key={p.name} title={p.desc ?? ""}>
                <span className="ag-param-name">{p.name}</span>
                <span className="ag-param-type">{p.type}</span>
                {p.required && <span className="ag-param-req">必填</span>}
              </div>
            ))}
          </div>
        )}
        {tool.doc && (
          <div className="ag-detail-md">
            <Markdown text={tool.doc} />
          </div>
        )}
        <OpenHint />
      </div>
    );
  }

  if (mod) {
    return (
      <div
        className="ag-detail"
        key={"m" + mod.id}
        ref={enterRef}
        role="button"
        tabIndex={0}
        title="点击查看完整文档"
        onClick={onOpen}
        onKeyDown={(e) => e.key === "Enter" && onOpen()}
      >
        <div className="ag-detail-title">{mod.id}</div>
        <div className="ag-detail-pills">
          <span className="ag-pill src">{mod.kind === "process" ? "模板" : "技能"}</span>
        </div>
        <div className="ag-detail-desc">{mod.desc}</div>
        <div className="ag-detail-md">
          <Markdown text={mod.body} />
        </div>
        <OpenHint />
      </div>
    );
  }

  return (
    <div className="ag-detail empty">
      <div className="ag-detail-title">详情</div>
      <div className="ag-detail-note">点击左侧的工具 / 模块查看详情</div>
    </div>
  );
}
