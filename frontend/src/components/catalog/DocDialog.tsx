// 文档弹窗：目录条目（工具/上下文模块）完整文档的阅读视图。
// 从组装编辑器抽出复用——目录管理页点条目直接开弹窗（浏览场景没有
// 侧栏紧凑预览，两级详情是编辑器的组装语境）。
import { useAgents, type ToolSpec } from "../../shared/agents";
import { Markdown } from "../../shared/markdown";
import { useEscape } from "../../shared/popover";

/** 文档焦点的指向（工具 / 上下文模块）。 */
export type Focus = { kind: "tool" | "module"; id: string };

/** 工具文档体（详情摘要 + 参数表 + markdown 文档）——DocDialog 与
 *  工具编写器的实时预览共用同一渲染。 */
export function ToolDocBody({ tool }: { tool: ToolSpec }) {
  return (
    <>
      <div className="ag-detail-desc">{tool.desc}</div>
      {tool.params && tool.params.length > 0 && (
        <div className="ag-detail-params">
          <div className="ag-detail-params-label">参数</div>
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
    </>
  );
}

export function DocDialog({ focus, onClose }: { focus: Focus; onClose: () => void }) {
  useEscape(true, onClose);
  const { modules, tools } = useAgents();
  const mod = focus.kind === "module" ? modules.find((m) => m.id === focus.id) : undefined;
  const tool = focus.kind === "tool" ? tools.find((t) => t.id === focus.id) : undefined;

  return (
    <div
      className="ag-doc-mask"
      role="dialog"
      aria-modal="true"
      aria-label={tool ? `工具文档 ${tool.id}` : mod ? `模块文档 ${mod.id}` : "文档"}
      onPointerDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="ag-doc">
        <div className="ag-doc-head">
          <span className="ag-doc-title">{tool ? tool.id : mod ? mod.id : ""}</span>
          <div className="ag-detail-pills">
            {tool ? (
              <>
                <span className={"ag-pill " + (tool.risk === "high" ? "risk-high" : "risk-low")}>
                  {tool.risk === "high" ? "高危" : "低危"}
                </span>
                <span className="ag-pill src">
                  {tool.source === "builtin" ? "内置" : tool.source === "binary" ? "外部二进制" : "MCP"}
                </span>
              </>
            ) : mod ? (
              <span className="ag-pill src">{mod.kind === "process" ? "模板" : "技能"}</span>
            ) : null}
          </div>
          <button type="button" className="ag-doc-close" onClick={onClose} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          {tool ? (
            <ToolDocBody tool={tool} />
          ) : mod ? (
            <>
              <div className="ag-detail-desc">{mod.desc}</div>
              <div className="ag-detail-md">
                <Markdown text={mod.body} />
              </div>
              <div className="ag-detail-note">注入上下文——不授予工具权限</div>
            </>
          ) : (
            <div className="ag-detail-note">条目不存在（目录可能已变化）</div>
          )}
        </div>
      </div>
    </div>
  );
}
