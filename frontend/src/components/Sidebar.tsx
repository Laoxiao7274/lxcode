import type { AgentSource } from "../agent/types";

/** 侧栏（Codex 式）：品牌区 → 新建任务 → 任务列表（选中竖条+浅底）→ 底部状态。 */
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
      <div className="brand">
        <span className="brandMark">m</span>
        <span className="brandName">lxcode</span>
      </div>
      <div className="sidebar-pad">
        <button className="new-task-btn" onClick={() => !busy && source.newSession()}>
          <span className="plusGlyph">＋</span> 新建任务
        </button>
      </div>
      <div className="sidebar-label">工作区</div>
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
        <div className="user-chip">
          <span className="userAvatar">x</span>
          <span className="userMeta">
            <span className="userName">本机 · 演示</span>
            <span className="userSub">{source.label}</span>
          </span>
        </div>
      </div>
    </aside>
  );
}
