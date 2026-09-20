// 会话标签条（浏览器式 tab）：最近会话快速切换 + 新建 + 关闭。
// 架构对齐（AGENTS.md §2 内核并发）：当前是单活跃会话——tab 的语义是
// 「快速切换的会话历史」而非并行执行；点标签 = resumeSession（唯一
// 活跃位切换）。关闭 = 只从标签条隐藏（会话本体与侧栏列表不动——
// 浏览器关 tab 不删历史）；刷新/重开后恢复完整列表。
import { useMemo, useState } from "react";
import type { AgentSource } from "../../shared/types";

/** 标签条容量：最近的 N 个会话（侧栏承载完整列表）。 */
const TAB_LIMIT = 8;

export function TabBar({
  source,
  currentId,
  busy,
  onNewChat,
}: {
  source: AgentSource;
  currentId: string;
  busy: boolean;
  onNewChat: () => void;
}) {
  // 用户关闭的标签（本会话内隐藏——纯 UI 态，刷新即恢复）
  const [closed, setClosed] = useState<Set<string>>(new Set());

  // 标签集合：当前会话恒在（被关闭的当前会话也强制显示——正在看着的
  // 东西不能凭空消失），其余按更新时间取最近 N-1 个。
  const sessions = source.sessions();
  const tabs = useMemo(() => {
    const visible = sessions.filter((s) => !s.archived && (!closed.has(s.id) || s.id === currentId));
    const cur = visible.find((s) => s.id === currentId);
    const rest = visible.filter((s) => s.id !== currentId).slice(0, TAB_LIMIT - 1);
    return cur ? [cur, ...rest] : rest.slice(0, TAB_LIMIT - 1);
  }, [sessions, currentId, closed]);

  // 没有任何会话（首次启动）也不显示条——空条占位
  if (tabs.length === 0) return null;

  // 关闭 = 从标签条隐藏（会话保留；关掉当前会话 → 切到新对话）
  const close = (id: string) => {
    if (busy) return;
    if (id === currentId) {
      onNewChat(); // 当前位切走；新会话标签由 pendingNewId 懒建语义接手
    }
    setClosed((prev) => new Set(prev).add(id));
  };

  return (
    <div className="tabbar" data-tabs={String(tabs.length)}>
      <div className="tab-strip">
        {tabs.map((s) => {
          const on = s.id === currentId;
          return (
            <div
              key={s.id}
              className={"tab" + (on ? " on" : "")}
              data-cg="tab"
              data-busy={on && busy ? "true" : undefined}
              title={s.title}
            >
              <button
                type="button"
                className="tab-main"
                onClick={() => { if (!busy && !on) source.resumeSession(s.id); }}
              >
                {on && busy && <span className="mset-spinner tab-spinner" aria-hidden />}
                <span className="tab-title">{s.title || "新对话"}</span>
              </button>
              <button
                type="button"
                className="tab-close"
                onClick={() => close(s.id)}
                aria-label={`关闭标签「${s.title || "新对话"}」`}
                title="关闭标签（会话保留在侧栏）"
              >
                <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                  <path d="M18 6 6 18M6 6l12 12" />
                </svg>
              </button>
            </div>
          );
        })}
        <button type="button" className="tab-new" onClick={onNewChat} aria-label="新对话" title="新对话">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </button>
      </div>
    </div>
  );
}
