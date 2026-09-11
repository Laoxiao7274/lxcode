import { useRef, useState } from "react";
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
  const searchRef = useRef<HTMLInputElement>(null);
  const all = source.sessions();
  const list = query.trim()
    ? all.filter((s) => s.title.toLowerCase().includes(query.trim().toLowerCase()))
    : all;

  return (
    <aside className="sidebar">
      {/* 顶部图标行（Codex：侧栏切换 / 搜索 / 新建） */}
      <div className="sidebar-iconbar">
        <button type="button" className="ib-btn" aria-label="折叠侧栏" title="折叠侧栏">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect x="3" y="3" width="18" height="18" rx="2" />
            <path d="M9 3v18" />
          </svg>
        </button>
        <button type="button" className={"ib-btn" + (query ? " on" : "")} aria-label="搜索" title="搜索" onClick={() => searchRef.current?.focus()}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
        </button>
        <button type="button" className="ib-btn" aria-label="新建任务" title="新建任务" onClick={() => !busy && source.newSession()}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      </div>
      <div className="sidebar-pad">
        <div className="search-box">
          <svg className="search-icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
          <input
            ref={searchRef}
            value={query}
            placeholder="搜索"
            aria-label="搜索任务"
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                setQuery("");
                e.currentTarget.blur();
              }
            }}
          />
          {query ? (
            <button type="button" className="search-clear" aria-label="清除" onClick={() => setQuery("")}>
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          ) : (
            <span className="search-kbd">Ctrl K</span>
          )}
        </div>
      </div>
      {/* 会话嵌套分组（Codex Directory 形态）+ Threads 标题行 */}
      <div className="sidebar-label threads-label">
        会话
        <span className="threads-actions" aria-hidden>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <circle cx="5" cy="12" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="19" cy="12" r="1.6" />
          </svg>
        </span>
      </div>
      {(() => {
        const SHOW = 4;
        const groups = new Map<string, typeof all>();
        for (const s of list) {
          const w = s.workspace ?? "（未分组）";
          if (!groups.has(w)) groups.set(w, []);
          groups.get(w)!.push(s);
        }
        return [...groups.entries()].map(([ws, sessions]) => {
          // 搜索时全部显示；平时每组最多 SHOW 条，超出折叠
          const visible = query.trim() ? sessions : sessions.slice(0, SHOW);
          const rest = sessions.length - visible.length;
          return (
            <div key={ws} className="ws-group">
              <div className="ws-row" title={"~/" + ws}>
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
                </svg>
                <span className="ws-name">{ws}</span>
                <span className="ws-actions" aria-hidden>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                    <circle cx="5" cy="12" r="1.5" /><circle cx="12" cy="12" r="1.5" /><circle cx="19" cy="12" r="1.5" />
                  </svg>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
                  </svg>
                </span>
              </div>
              {visible.map((s) => (
                <div
                  key={s.id}
                  className={"session-item" + (s.id === currentId ? " active" : "")}
                  onClick={() => !busy && source.resumeSession(s.id)}
                  title={s.title}
                >
                  <span className={"s-dot" + (s.id === currentId && busy ? " live" : "")} aria-hidden />
                  <span className="title">{s.title}</span>
                  <span className="time">{s.updatedAt}</span>
                </div>
              ))}
              {rest > 0 && (
                <div className="ws-more">Show more（{rest}）</div>
              )}
            </div>
          );
        });
      })()}
      {list.length === 0 && query.trim() && (
        <div className="sidebar-empty">没有匹配「{query.trim()}」的任务</div>
      )}
      <div className="sidebar-footer">
        <div className="settings-row" role="button" tabIndex={0} onClick={onOpenSettings}>
          <span className="settings-label">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="3" />
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
            </svg>
            设置
          </span>
          <span className="ver">eabc22c</span>
        </div>
      </div>
    </aside>
  );
}
