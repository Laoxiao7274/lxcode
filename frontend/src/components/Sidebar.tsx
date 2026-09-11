import { useState } from "react";
import type { AgentSource } from "../agent/types";

/** 侧栏：品牌 → 新建任务 → 搜索（过滤会话）→ 任务列表 → 底部设置。 */
export function Sidebar({
  source,
  currentId,
  busy,
  onOpenSettings,
}: {
  source: AgentSource;
  currentId: string;
  busy: boolean;
  onOpenSettings: () => void;
}) {
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const all = source.sessions();
  const list = query.trim()
    ? all.filter((s) => s.title.toLowerCase().includes(query.trim().toLowerCase()))
    : all;

  return (
    <aside className="sidebar">
      <div className="sidebar-pad">
        <button className="new-task-btn" onClick={() => !busy && source.newSession()}>
          <span className="plusGlyph">＋</span> 新建任务
        </button>
        {searching ? (
          <div className="sidebar-searching">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="11" cy="11" r="7" />
              <path d="m20 20-3.5-3.5" />
            </svg>
            <input
              autoFocus
              value={query}
              placeholder="搜索任务…"
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setQuery("");
                  setSearching(false);
                }
              }}
              onBlur={() => {
                if (!query.trim()) setSearching(false);
              }}
            />
          </div>
        ) : (
          <div className="sidebar-search" onClick={() => setSearching(true)}>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="11" cy="11" r="7" />
              <path d="m20 20-3.5-3.5" />
            </svg>
            搜索
          </div>
        )}
      </div>
      <div className="sidebar-label">任务{list.length !== all.length ? ` · ${list.length}/${all.length}` : ""}</div>
      {list.map((s) => (
        <div
          key={s.id}
          className={"session-item" + (s.id === currentId ? " active" : "")}
          onClick={() => !busy && source.resumeSession(s.id)}
          title={s.title}
        >
          <span className="title">{s.title}</span>
          {s.id === currentId && busy && <span className="live-dot" />}
          <span className="time">{s.updatedAt}</span>
        </div>
      ))}
      {list.length === 0 && query.trim() && (
        <div className="sidebar-empty">没有匹配「{query.trim()}」的任务</div>
      )}
      <div className="sidebar-footer">
        <div className="settings-row" role="button" tabIndex={0} onClick={onOpenSettings}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
          </svg>
          设置
          <span className="ver">eabc22c</span>
        </div>
      </div>
    </aside>
  );
}
