// 拓展条目卡（从 CatalogPage 拆出——渲染器与页面编排分离）：
// EntryCard（工具/技能/模板通用条目卡）+ ToolCard / ModuleCard /
// McServerCard（载荷装配）。两步删除确认统一走 useConfirmClick。
import type { ReactNode } from "react";
import { useAgents, type ContextModuleSpec, type McServerSpec, type ToolSpec } from "../../shared/agents";
import { useConfirmClick } from "../../shared/confirm-click";
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
export function McServerCard({ server, toolCount, onEdit, onToggle, onDelete }: {
  server: McServerSpec;
  toolCount: number;
  onEdit?: () => void;
  onToggle?: () => void;
  onDelete?: () => void;
}) {
  const del = useConfirmClick(onDelete ?? (() => {}));
  const interactive = !!onEdit || !!onDelete;
  const launch = server.transport === "stdio" ? [server.command, ...server.args].filter(Boolean).join(" ") : server.url;
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
          <span className={"ag-pill " + (server.enabled ? "risk-low" : "src")}>
            {server.enabled ? "已连接" : "未连接"}
          </span>
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
        {toolCount} 个工具 · {server.enabled ? "能力可用" : "能力挂起"}
        {server.transport === "stdio" && Object.keys(server.env).length > 0 ? ` · ${Object.keys(server.env).length} 个环境变量` : ""}
      </div>
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

/** MCP 工具计数（server id → 暴露的工具数——McServerCard 的载荷）。 */
export function useMcpToolCounts(): (serverId: string) => number {
  const { tools } = useAgents();
  const counts = new Map<string, number>();
  for (const t of tools) {
    if (t.source === "mcp" && t.server) counts.set(t.server, (counts.get(t.server) ?? 0) + 1);
  }
  return (serverId: string) => counts.get(serverId) ?? 0;
}
