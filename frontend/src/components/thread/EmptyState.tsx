// 新对话空态（从 Thread 拆出——建议卡数据 + 身份芯片 + 入场编排）。
// Thread 只保留滚动状态机；空态的交错入场动画在这里自管。
import type { RefObject } from "react";

/** 建议卡图标：应用图标语言（线性描边，无字符圆圈——OS 感重）。 */
const SUGGESTIONS = [
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M12 20h9" />
        <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
      </svg>
    ),
    title: "把工具循环加上超时兜底",
    sub: "单工具卡死不再拖住整轮",
  },
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" />
        <path d="M14 2v6h6" />
        <path d="M9 13h6M9 17h4" />
      </svg>
    ),
    title: "读 config/local.json",
    sub: "看 default 绑定的是哪个模型",
  },
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="m8.5 12.5 2.5 2.5 5-6" />
      </svg>
    ),
    title: "全量测试有红的修掉",
    sub: "go test ./… 一轮到绿",
  },
  {
    icon: (
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M21 15a2 2 0 0 1-2 2H8l-5 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z" />
      </svg>
    ),
    title: "讲讲 runTools 的设计",
    sub: "为什么高危要先确认",
  },
];

export function EmptyState({
  emptyRef,
  agent,
  delegateCount,
  projectName,
  onSuggestion,
}: {
  /** Thread 持有的空态根 ref（入场动画编排仍在 Thread——依赖 blocks.length）。 */
  emptyRef: RefObject<HTMLDivElement | null>;
  /** 当前 Agent 芯片载荷（null = 不显示）。 */
  agent: { name: string; color: string; isMain: boolean } | null;
  /** 主 Agent 的有效委派计数。 */
  delegateCount: number;
  /** 新对话空态的归属项目名。 */
  projectName?: string;
  onSuggestion?: (text: string) => void;
}) {
  return (
    <div className="empty-state" ref={emptyRef}>
      {(agent || projectName) && (
        <div className="empty-meta">
          {agent && (
            <span className="empty-agent" title="当前干活的 Agent——输入区可切换；主 Agent 的可委派名单在菜单里调整">
              <span className="ag-dot" style={{ background: agent.color }} />
              {agent.name}
              {agent.isMain && (
                <span className="empty-agent-sub">· 可委派 {delegateCount}</span>
              )}
            </span>
          )}
          {projectName && (
            <span className="empty-project" title={`新会话归属 ${projectName}`}>
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
              </svg>
              {projectName}
            </span>
          )}
        </div>
      )}
      <h2>我们做点什么？</h2>
      <p>读写代码、改文件、跑命令——高危操作先过你这一关。</p>
      <div className="suggest-grid">
        {SUGGESTIONS.map((s) => (
          <button key={s.title} type="button" className="suggest-card" onClick={() => onSuggestion?.(s.title)}>
            <span className="suggest-icon" aria-hidden>{s.icon}</span>
            <span className="suggest-text">
              <span className="suggest-title">{s.title}</span>
              <span className="suggest-sub">{s.sub}</span>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
