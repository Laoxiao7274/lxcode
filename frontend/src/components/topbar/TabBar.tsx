// 会话标签条（浏览器式 tab）：最近会话快速切换 + 新建 + 关闭。
// 顺序语义对齐浏览器：**打开顺序固定**——点标签只切焦点，绝不重排
//（之前把当前会话强制排到首位，点谁谁跳到最前，与浏览器直觉相悖）。
// 新会话/从侧栏点进来的会话追加到右侧（像开新标签），容量满丢最老的。
// 架构对齐（AGENTS.md §2 内核并发）：单活跃会话——tab 是「快速切换的
// 会话历史」而非并行执行。关闭 = 只从标签条隐藏（会话本体与侧栏不动，
// 刷新恢复）。
import { useEffect, useRef, useState } from "react";
import type { AgentSource } from "../../shared/types";

/** 标签条容量：最多同时显示的标签数（超出丢最老的——浏览器同款）。 */
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
  const sessions = source.sessions();
  // 打开顺序（标签 id 列表）；closed = 用户关掉的（纯 UI 态，刷新恢复）
  const [order, setOrder] = useState<string[]>([]);
  const [closed, setClosed] = useState<Set<string>>(new Set());
  const seededRef = useRef(false);

  // 首次拿到会话列表铺开标签：后端序是最近在前 → 反转成「新的在右」
  //（浏览器里后开的标签在右边）。只铺一次——之后顺序由用户操作决定。
  useEffect(() => {
    if (seededRef.current || sessions.length === 0) return;
    seededRef.current = true;
    setOrder(sessions.filter((s) => !s.archived).slice(0, TAB_LIMIT).map((s) => s.id).reverse());
  }, [sessions]);

  // 切到不在标签条里的会话（侧栏点进来 / 新建）→ 追加到右侧。
  // 已在列表里则不动（点标签不重排——浏览器语义）。
  useEffect(() => {
    if (!currentId) return;
    setOrder((prev) => (prev.includes(currentId) ? prev : [...prev.slice(-(TAB_LIMIT - 1)), currentId]));
  }, [currentId]);

  // 渲染集合：按打开顺序，剔除已归档/已关闭的（当前会话恒显示）
  const tabs = order
    .map((id) => ({ id, meta: sessions.find((s) => s.id === id) }))
    .filter(({ id, meta }) => id === currentId || (meta && !meta.archived && !closed.has(id)));

  // 没有任何标签（首次启动）也不显示条——空条占位
  if (tabs.length === 0) return null;

  // 关闭 = 从标签条隐藏（会话保留；关掉当前会话 → 切到新对话）
  const close = (id: string) => {
    if (busy) return;
    if (id === currentId) onNewChat();
    setClosed((prev) => new Set(prev).add(id));
  };

  return (
    <div className="tabbar" data-tabs={String(tabs.length)}>
      <div className="tab-strip">
        {tabs.map(({ id, meta }) => {
          const on = id === currentId;
          const title = meta?.title || "新对话";
          return (
            <div
              key={id}
              className={"tab" + (on ? " on" : "")}
              data-cg="tab"
              data-busy={on && busy ? "true" : undefined}
              title={title}
            >
              <button
                type="button"
                className="tab-main"
                onClick={() => { if (!busy && !on) source.resumeSession(id); }}
              >
                {on && busy && <span className="mset-spinner tab-spinner" aria-hidden />}
                <span className="tab-title">{title}</span>
              </button>
              <button
                type="button"
                className="tab-close"
                onClick={() => close(id)}
                aria-label={`关闭标签「${title}」`}
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
