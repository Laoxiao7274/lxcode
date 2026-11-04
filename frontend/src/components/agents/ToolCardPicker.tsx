// AgentEditor 工具区的卡片选择器：每个工具一张小卡片（名称 + 一句话描述 +
// 风险徽标 + 来源小标 + 图标），替代原胶囊 Chips。四个来源组共用一份白名单
// （def.tools），交互与原 Chips 对齐：点卡片 = 勾选 + 焦点（DetailPanel 跟随，
// 点面板进 DocDialog 的链路不变）。
//
// - 搜索框按名称与描述过滤（大小写不敏感），只影响显示、不动已选；
// - 卡片容器限高滚动（对齐 .mset-candidate-scroll 的 260px 档）——分组标题
//   保留，各组在同一滚动容器内（组数少、每条卡片矮，整体一个容器比四组各自
//   滚动省空间也不丢分组语义）；
// - 图标是组件内的轻量映射（常用工具 20 条上下），未命中回落统一扳手。
import { useState } from "react";
import { IconCheck } from "../icons";
import type { ToolSpec } from "../../shared/agents";

/** 常用工具的图标映射（emoji 轻量方案——emoji 跨平台渲染一致、零维护成本）；
 *  未命中的统一回落 🔧。 */
const TOOL_ICONS: Record<string, string> = {
  read_file: "📄",
  write_file: "✏️",
  edit: "🩹",
  bash: "⌨️",
  bash_readonly: "⌨️",
  search: "🔍",
  session_search: "🕘",
  web_search: "🌐",
  web_fetch: "🌐",
  read_skill: "📚",
  share_file: "📤",
  read_image: "🖼️",
  todo: "☑️",
  agent_dispatch: "🤖",
  agent_read: "🤖",
  agent_write: "🤖",
  merge_request: "🔀",
  workspace_status: "🧭",
  workspace_sync: "🔁",
  workspace_rollback: "⏪",
  ripgrep: "🔍",
  browser: "🧭",
};

const FALLBACK_ICON = "🔧";

/** 来源小标文案。 */
function sourceLabel(t: ToolSpec): string {
  if (t.custom) return "自定义";
  if (t.source === "mcp") return `MCP · ${t.server ?? ""}`;
  if (t.source === "binary") return "外部";
  return "内置";
}

export interface ToolCardData extends ToolSpec {
  /** 中文名短名（ToolBlock 的映射口径；无映射回落 id）。 */
  label: string;
  icon: string;
  sourceText: string;
}

/** 把工具目录条目装配成卡片数据（AgentEditor 四个分组各调一次）。 */
export function toToolCard(t: ToolSpec): ToolCardData {
  return {
    ...t,
    label: TOOL_NAMES[t.id] ?? t.id,
    icon: TOOL_ICONS[t.id] ?? FALLBACK_ICON,
    sourceText: sourceLabel(t),
  };
}

/** 中文短名（ToolBlock.tsx 的映射口径，覆盖目录里的常用条目）。 */
const TOOL_NAMES: Record<string, string> = {
  read_file: "读文件",
  write_file: "写文件",
  edit: "改文件",
  bash: "bash",
  bash_readonly: "bash（只读）",
  search: "搜索",
  session_search: "查历史",
  web_search: "网页搜索",
  web_fetch: "网页抓取",
  read_skill: "读技能",
  share_file: "共享文件",
  read_image: "读图片",
  todo: "任务清单",
  agent_dispatch: "派发任务",
  agent_read: "读 Agent",
  agent_write: "写 Agent",
  merge_request: "合并请求",
  workspace_status: "工作区状态",
  workspace_sync: "工作区同步",
  workspace_rollback: "工作区回滚",
  ripgrep: "ripgrep",
  browser: "浏览器",
};

const searchIcon = (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="11" cy="11" r="7" />
    <path d="m21 21-4.3-4.3" />
  </svg>
);

/** 工具卡片网格（一组）。选中态与原 Chips 一致（勾选标识 + 深底），
 *  焦点态正交（细描边环）。 */
function ToolCardGrid({
  items,
  selected,
  focused,
  onPick,
}: {
  items: ToolCardData[];
  selected: string[];
  focused: string | null;
  onPick: (id: string) => void;
}) {
  return (
    <div className="ag-tool-grid" role="group">
      {items.map((t) => {
        const on = selected.includes(t.id);
        return (
          <button
            key={t.id}
            type="button"
            className={"ag-tool-card" + (on ? " on" : "") + (focused === t.id ? " focus" : "")}
            title={t.desc}
            aria-pressed={on}
            onClick={() => onPick(t.id)}
          >
            <span className="ag-tool-icon" aria-hidden="true">{t.icon}</span>
            <span className="ag-tool-body">
              <span className="ag-tool-name">
                {t.label}
                {on && <IconCheck size={10} strokeWidth={2.6} />}
              </span>
              <span className="ag-tool-desc">{t.desc}</span>
              <span className="ag-tool-meta">
                <span className={"ag-tool-risk" + (t.risk === "high" ? " high" : "")} aria-label={t.risk === "high" ? "高危" : "低危"} />
                <span className="ag-tool-src">{t.sourceText}</span>
              </span>
            </span>
          </button>
        );
      })}
    </div>
  );
}

/** 工具选择器整体：搜索框 + 限高滚动容器（四个来源组同容器滚动）+ 计数。
 *  计数沿用「工具 N」的口径：搜索时显示「匹配 M / 共 N」。 */
export function ToolCardPicker({
  groups,
  value,
  onChange,
  focusedValue,
  onFocus,
  total,
}: {
  /** 分组（标题 + 该组工具），顺序即渲染顺序。 */
  groups: { title: string; items: ToolSpec[] }[];
  value: string[];
  onChange: (tools: string[]) => void;
  focusedValue: string | null;
  onFocus: (id: string) => void;
  /** 目录工具总数（计数用——不是过滤后的）。 */
  total: number;
}) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();

  const shownGroups = q
    ? groups
        .map((g) => ({
          title: g.title,
          items: g.items.filter((t) => `${t.id} ${TOOL_NAMES[t.id] ?? ""} ${t.desc}`.toLowerCase().includes(q)),
        }))
        .filter((g) => g.items.length > 0)
    : groups;
  const matched = shownGroups.reduce((n, g) => n + g.items.length, 0);

  const pick = (id: string) => {
    onChange(value.includes(id) ? value.filter((v) => v !== id) : [...value, id]);
    onFocus(id);
  };

  return (
    <div className="ag-tool-picker">
      <label className="mset-search ag-tool-search">
        {searchIcon}
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索工具（名称或描述）"
          spellCheck={false}
          aria-label="搜索工具"
        />
      </label>
      <div className="ag-tool-scroll">
        {shownGroups.length === 0 ? (
          <div className="ag-tool-empty">没有匹配的工具</div>
        ) : (
          shownGroups.map((g) => (
            <div key={g.title}>
              <div className="ag-chip-label">{g.title}</div>
              <ToolCardGrid items={g.items.map(toToolCard)} selected={value} focused={focusedValue} onPick={pick} />
            </div>
          ))
        )}
      </div>
      {/* 选中计数在分节标题的 ag-count（「工具 N」）；搜索时这里给匹配口径 */}
      {q && <div className="ag-tool-count">匹配 {matched} / 共 {total}</div>}
    </div>
  );
}
