// 文档弹窗：目录条目（工具/上下文模块）完整文档的阅读视图。
// 从组装编辑器抽出复用——目录管理页点条目直接开弹窗（浏览场景没有
// 侧栏紧凑预览，两级详情是编辑器的组装语境）。阅读器形态（ag-doc-reader）：
// 灰底画布 + 纸面内容卡 + 头部来源图标——文档就该有文档的质感。
import type { ReactNode } from "react";
import { useAgents, type ToolSpec } from "../../shared/agents";
import { Markdown } from "../../shared/markdown";
import { useEscape } from "../../shared/popover";

/** 文档焦点的指向（工具 / 上下文模块）。 */
export type Focus = { kind: "tool" | "module"; id: string };

/** 工具文档体（lede 摘要 + 盒装参数表 + markdown 文档）——DocDialog 与
 *  工具编写器的实时预览共用同一渲染（尺寸由上下文作用域分档）。 */
export function ToolDocBody({ tool }: { tool: ToolSpec }) {
  return (
    <>
      <div className="ag-detail-desc">{tool.desc}</div>
      {tool.params && tool.params.length > 0 && (
        <>
          <div className="ag-sec-label">参数 · {tool.params.length}</div>
          <div className="ag-params-table">
            {tool.params.map((p) => (
              <div className="ag-param" key={p.name} title={p.desc ?? ""}>
                <span className="ag-param-name">{p.name}</span>
                <span className="ag-param-type">{p.type}</span>
                {p.desc && <span className="ag-param-note">{p.desc}</span>}
                {p.required && <span className="ag-param-req">必填</span>}
              </div>
            ))}
          </div>
        </>
      )}
      {tool.doc && (
        <>
          <div className="ag-sec-label">文档</div>
          <div className="ag-detail-md">
            <Markdown text={tool.doc} />
          </div>
        </>
      )}
    </>
  );
}

/** 头部来源图标：黑底白线（内置=扳手 / 外部二进制=终端 / MCP=六边 / 模块=文档）——
 *  条目的视觉身份，比纯文字标题有分量。 */
function DocIcon({ kind }: { kind: "builtin" | "binary" | "mcp" | "module" }) {
  const glyph: Record<typeof kind, ReactNode> = {
    builtin: <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />,
    binary: (
      <>
        <path d="m5 8 4 4-4 4" />
        <path d="M12 17h7" />
      </>
    ),
    mcp: <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />,
    module: (
      <>
        <path d="M13 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V10z" />
        <path d="M13 3v7h7" />
      </>
    ),
  };
  return (
    <span className="dd-icon" aria-hidden="true">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">{glyph[kind]}</svg>
    </span>
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
      <div className="ag-doc ag-doc-reader">
        <div className="ag-doc-head">
          <DocIcon kind={tool ? tool.source : "module"} />
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
          <div className="dd-paper">
            {tool ? (
              <ToolDocBody tool={tool} />
            ) : mod ? (
              <>
                <div className="ag-detail-desc">{mod.desc}</div>
                <div className="ag-sec-label">正文</div>
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
    </div>
  );
}
