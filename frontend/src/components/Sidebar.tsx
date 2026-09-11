import type { AgentSource } from "../agent/types";

/** 侧栏：会话列表 + 新会话/恢复。 */
export function Sidebar({
  source,
  currentId,
  busy,
}: {
  source: AgentSource;
  currentId: string;
  busy: boolean;
}) {
  const list = source.sessions();
  return (
    <aside className="sidebar">
      <div className="sidebar-label">会话</div>
      {list.map((s) => (
        <div
          key={s.id}
          className={"session-item" + (s.id === currentId ? " active" : "")}
          onClick={() => !busy && source.resumeSession(s.id)}
          title={s.title}
        >
          <span className="title">{s.title}</span>
          <span className="time">{s.updatedAt}</span>
        </div>
      ))}
      <div className="sidebar-footer">
        <button className="sidebar-btn" onClick={() => !busy && source.newSession()}>
          ＋ 新会话
        </button>
      </div>
    </aside>
  );
}
