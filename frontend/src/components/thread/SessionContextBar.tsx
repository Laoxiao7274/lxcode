// SessionContextBar —— 输入区上方的会话上下文条：「现在在哪个分支干活」。
// 侧栏是列表（保持干净），这里是工作上下文——分支/未提交/冲突/合并操作
// 跟输入行为是一体的（发消息 = 在这个分支上干活）。
import type { AgentSource, SessionMeta } from "../../shared/types";

const branchIcon = (
  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="6" cy="6" r="2.6" />
    <circle cx="6" cy="18" r="2.6" />
    <circle cx="18" cy="8" r="2.6" />
    <path d="M6 8.6v6.8M8.6 6.5h4.9a4 4 0 0 1 3.9 3.1" />
  </svg>
);

export function SessionContextBar({
  source,
  currentId,
}: {
  source: AgentSource;
  currentId: string;
}) {
  const s = source.sessions().find((x: SessionMeta) => x.id === currentId);
  if (!s || !s.branch || s.merged) {
    // 无分支（未挂项目）或已合并：不占位——上下文条只在「隔离干活中」出现
    return null;
  }
  const dirty = s.dirty ?? 0;
  const conflicts = s.conflicts ?? 0;

  return (
    <div className="ctx-bar" role="status">
      <span className="ctx-branch" title={`分支 ${s.branch}——改动隔离保存，主目录不受影响`}>
        {branchIcon}
        {s.branch}
      </span>
      {conflicts > 0 ? (
        <span className="ctx-conflict">⚠ {conflicts} 冲突未解</span>
      ) : dirty > 0 ? (
        <span className="ctx-dirty">● {dirty} 未提交</span>
      ) : (
        <span className="ctx-clean">干净</span>
      )}
      <span className="ctx-actions">
        <button type="button" onClick={() => source.mergeSession(s.id)}>合并回主线</button>
        <button type="button" className="ctx-discard" onClick={() => source.discardSession(s.id)}>放弃</button>
      </span>
    </div>
  );
}
