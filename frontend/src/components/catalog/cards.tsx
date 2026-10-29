// 拓展条目卡（从 CatalogPage 拆出——渲染器与页面编排分离）：
// EntryCard（工具/技能/模板通用条目卡）+ ToolCard / ModuleCard /
// McServerCard（载荷装配）。两步删除确认统一走 useConfirmClick。
import type { ReactNode } from "react";
import { useAgents, type ContextModuleSpec, type McpRuntime, type McServerSpec, type ToolSpec } from "../../shared/agents";
import { useConfirmClick } from "../../shared/confirm-click";
import { mcpStatusPill } from "../../shared/mcp-status";
import { Toggle } from "../form";
import { IconPencil, IconTrash } from "../icons";

/** 条目卡：标题行（mono id + pills）+ 摘要；自建条目带编辑/删除（两步确认）。 */
export function EntryCard({
  onClick,
  id,
  desc,
  pills,
  meta,
  onEdit,
  onDelete,
}: {
  onClick: () => void;
  id: string;
  desc: string;
  pills: ReactNode;
  meta?: string;
  /** 自建条目的操作（内置条目不传——只读）。 */
  onEdit?: () => void;
  onDelete?: () => void;
}) {
  const del = useConfirmClick(onDelete ?? (() => {}));
  const interactive = !!onEdit || !!onDelete;
  return (
    <div
      className="cg-card"
      role="button"
      tabIndex={0}
      title="点击查看完整文档"
      onClick={onClick}
      onKeyDown={(e) => e.key === "Enter" && onClick()}
    >
      <div className="cg-card-top">
        <span className="cg-card-title">{id}</span>
        <span className="cg-card-pills">{pills}</span>
      </div>
      <div className="cg-card-desc">{desc}</div>
      {meta && <div className="cg-card-meta">{meta}</div>}
      {interactive && (
        <div className="cg-card-actions">
          {onEdit && (
            <button type="button" className="ag-mini-btn" onClick={(e) => { e.stopPropagation(); onEdit(); }}>
              <IconPencil /> 编辑
            </button>
          )}
          {onDelete && (
            <button
              type="button"
              className={"ag-mini-btn danger" + (del.confirming ? " confirm" : "")}
              data-cg="del"
              onClick={(e) => { e.stopPropagation(); del.onClick(); }}
              onBlur={del.onBlur}
            >
              <IconTrash /> {del.confirming ? "确认删除" : "删除"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

export function ToolCard({ tool, onOpen, onEdit, onDelete }: {
  tool: ToolSpec;
  onOpen: () => void;
  onEdit?: () => void;
  onDelete?: () => void;
}) {
  // 外部二进制工具必须配 command 才能进注册表——没配的如实标「未配置」，
  // 否则用户勾进 Agent 白名单后只会从模型那里听到「注册表没有」（用户报告过）。
  const unconfigured = tool.source === "binary" && !(tool.command ?? "").trim();
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
          {unconfigured && <span className="ag-pill warn" title="缺 command——不会进注册表，模型看不到它；点开填上 command 即可">未配置</span>}
          {tool.custom && <span className="ag-pill src">自定义</span>}
        </>
      }
      meta={tool.params && tool.params.length > 0 ? `${tool.params.length} 参数` : undefined}
      onEdit={onEdit}
      onDelete={onDelete}
    />
  );
}

export function ModuleCard({ mod, onOpen, onEdit, onDelete }: {
  mod: ContextModuleSpec;
  onOpen: () => void;
  onEdit?: () => void;
  onDelete?: () => void;
}) {
  return (
    <EntryCard
      onClick={onOpen}
      id={mod.id}
      desc={mod.desc}
      pills={
        <>
          <span className="ag-pill src">{mod.kind === "process" ? "模板" : "技能"}</span>
          {mod.custom && <span className="ag-pill src">自定义</span>}
        </>
      }
      onEdit={onEdit}
      onDelete={onDelete}
    />
  );
}

/** MCP 服务器卡：服务器名 + 连接状态 + 启停 + 接入命令/URL + 暴露工具数。 */
export function McServerCard({ server, toolCount, runtime, onEdit, onToggle, onDelete }: {
  server: McServerSpec;
  toolCount: number;
  /** 运行期状态（后端报的）。缺省 = 演示态/老后端，回落到按 enabled 显示。 */
  runtime?: McpRuntime;
  onEdit?: () => void;
  onToggle?: () => void;
  onDelete?: () => void;
}) {
  const del = useConfirmClick(onDelete ?? (() => {}));
  const interactive = !!onEdit || !!onDelete;
  const launch = server.transport === "stdio" ? [server.command, ...server.args].filter(Boolean).join(" ") : server.url;
  // 状态标签：**有运行期状态就以它为准**——enabled 只说「用户想开」，
  // 连不上时它仍是 true，拿它显示「已连接」等于骗用户（本轮之前就是这样）。
  const pill = mcpStatusPill(server.enabled, runtime);
  return (
    <div
      className="cg-card"
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
      title={server.desc}
      onClick={onEdit}
      onKeyDown={interactive && onEdit ? (e) => e.key === "Enter" && onEdit() : undefined}
    >
      <div className="cg-card-top">
        <span className="cg-card-title">{server.id}</span>
        <span className="cg-card-pills">
          <span className={"ag-pill " + pill.cls}>{pill.text}</span>
          <span className="ag-pill src">{server.transport === "stdio" ? "stdio" : "SSE"}</span>
          {server.custom && <span className="ag-pill src">自定义</span>}
          {onToggle && (
            <Toggle on={server.enabled} onChange={onToggle} ariaLabel={server.enabled ? "断开" : "连接"} />
          )}
        </span>
      </div>
      <div className="cg-card-desc">{server.desc}</div>
      <div className="cg-card-meta" title={launch}>{launch}</div>
      <div className="cg-card-meta">
        {toolCount} 个工具 · {pill.note}
        {server.transport === "stdio" && Object.keys(server.env).length > 0 ? ` · ${Object.keys(server.env).length} 个环境变量` : ""}
      </div>
      {/* 失败原因要显示出来（只说「连接失败」用户无从下手——是命令不对、
          网络不通还是缺凭据，得让原因自己说话） */}
      {runtime?.status === "error" && runtime.lastError !== "" && (
        <div className="cg-card-error" title={runtime.lastError}>{runtime.lastError}</div>
      )}
      {interactive && (
        <div className="cg-card-actions">
          {onEdit && (
            <button type="button" className="ag-mini-btn" data-cg="edit" onClick={(e) => { e.stopPropagation(); onEdit(); }}>
              <IconPencil /> 编辑
            </button>
          )}
          {onDelete && server.custom && (
            <button
              type="button"
              data-cg="del"
              className={"ag-mini-btn danger" + (del.confirming ? " confirm" : "")}
              onClick={(e) => { e.stopPropagation(); del.onClick(); }}
              onBlur={del.onBlur}
            >
              <IconTrash /> {del.confirming ? "确认删除" : "删除"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/** MCP 运行期状态查询（server id → 状态）。缺条目 = 后端没报（演示态/
 *  老后端），卡片回落到按 enabled 显示。 */
export function useMcpRuntime(): (serverId: string) => McpRuntime | undefined {
  const { mcpRuntime } = useAgents();
  return (serverId: string) => mcpRuntime[serverId];
}

/** MCP 工具计数（server id → 暴露的工具数——McServerCard 的载荷）。
 *
 * 优先用后端报的 tool_count（运行期事实），没有才回落到按目录里 source=mcp
 * 的条目数——演示态与老后端走回落，行为与加状态之前一致。 */
export function useMcpToolCounts(): (serverId: string) => number {
  const { tools, mcpRuntime } = useAgents();
  const counts = new Map<string, number>();
  for (const t of tools) {
    if (t.source === "mcp" && t.server) counts.set(t.server, (counts.get(t.server) ?? 0) + 1);
  }
  return (serverId: string) => mcpRuntime[serverId]?.toolCount ?? counts.get(serverId) ?? 0;
}
