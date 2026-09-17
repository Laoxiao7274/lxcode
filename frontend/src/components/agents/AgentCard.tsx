// 名单卡片：主 Agent 特殊态（「主」徽标 + 无删除 + 无开关），普通卡带启用
// 开关与编辑/删除（删除两步确认，点外失焦复位）。编辑器右侧预览复用同卡
// （preview 隐藏一切操作，只读展示组装中的定义）。
import { useState } from "react";
import type { AgentDef } from "../../shared/agents";
import { useAgents, useModelLabel } from "../../shared/agents";
import { Toggle } from "../form";
import { IconPencil, IconTrash } from "../icons";

export function AgentCard({
  agent,
  onEdit,
  onToggle,
  onDelete,
  preview,
}: {
  agent: AgentDef;
  onEdit?: () => void;
  onToggle?: () => void;
  onDelete?: () => void;
  /** 预览态：不可点击、无操作行。 */
  preview?: boolean;
}) {
  const modelLabel = useModelLabel(agent.model);
  const { agents: roster } = useAgents();
  const [confirming, setConfirming] = useState(false);
  // 主卡的可委派计数：名单默认 ∩ 当前启用的子 Agent
  const delegateCount = agent.isMain
    ? agent.delegates.filter((id) => roster.some((a) => a.id === id && a.enabled && !a.isMain)).length
    : 0;

  const clickable = !preview && !!onEdit;

  return (
    <div
      className={"ag-card" + (agent.isMain ? " main" : "") + (agent.enabled ? "" : " off") + (preview ? " preview" : "")}
      role={clickable ? "button" : undefined}
      tabIndex={clickable ? 0 : undefined}
      aria-label={clickable ? `编辑 ${agent.name}` : undefined}
      onClick={clickable ? onEdit : undefined}
      onKeyDown={clickable ? (e) => e.key === "Enter" && onEdit() : undefined}
    >
      <div className="ag-card-top">
        <span className="ag-dot" style={{ background: agent.color }} />
        <span className="ag-card-name">{agent.name || "未命名"}</span>
        {agent.isMain && <span className="ag-main-badge">主</span>}
        {onToggle && !preview && (
          <Toggle
            on={agent.enabled}
            onChange={() => onToggle()}
            ariaLabel={agent.enabled ? "停用" : "启用"}
          />
        )}
      </div>
      <div className="ag-card-desc">{agent.desc || "（还没有描述——组装时补上职责一句话）"}</div>
      <div className="ag-card-meta">
        <span className="ag-card-model" title={agent.model}>
          {modelLabel}
        </span>
        <span>·</span>
        <span>{agent.tools.length} 工具</span>
        <span>·</span>
        <span>{(agent.workflow ? 1 : 0) + agent.skills.length} 模块</span>
        {agent.isMain && (
          <>
            <span>·</span>
            <span>{delegateCount > 0 ? `可委派 ${delegateCount} 子 Agent` : "未配置委派"}</span>
          </>
        )}
      </div>
      {!preview && (onEdit || onDelete) && (
        <div className="ag-card-actions">
          {onEdit && (
            <button type="button" className="ag-mini-btn" onClick={(e) => { e.stopPropagation(); onEdit(); }}>
              <IconPencil /> 编辑
            </button>
          )}
          {onDelete && !agent.isMain && (
            <button
              type="button"
              className={"ag-mini-btn danger" + (confirming ? " confirm" : "")}
              onClick={(e) => {
                e.stopPropagation();
                if (confirming) onDelete();
                else setConfirming(true);
              }}
              onBlur={() => setConfirming(false)}
            >
              <IconTrash /> {confirming ? "确认删除" : "删除"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
