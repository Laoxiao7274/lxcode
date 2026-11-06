// 助手模式的会话列表（助手模式视觉对齐主域，2026-12）：**手写 DOM 用主域
// 行类**（.session-item / .session-line / .s-dot / .title / .time——sidebar.css
// 的既有规则逐像素复用：中性灰选中态、busy 蓝点加大 + spinner + 标题加重）。
//
// 与主域 SessionRow 的分工：主域行带 ⋯ 菜单（重命名/归档）——assistant.*
// 协议面没有这两个方法，助手域走「视觉一致 + 无菜单」（路 A）；时间用全量
// updatedAt 对齐主域（不截断）。选中态/时间格式与主域同款，不发明新视觉。
import { memo } from "react";
import type { SessionMeta } from "../../shared/types";

export const AssistantSessionList = memo(function AssistantSessionList({
  sessions,
  activeId,
  onSelect,
}: {
  sessions: SessionMeta[];
  activeId: string;
  onSelect: (id: string) => void;
}) {
  return (
    <div className="sess-scroll assistant-ws-sessions" role="listbox" aria-label="助手会话列表">
      {sessions.length === 0 && (
        <div className="assistant-ws-sessions-empty">还没有对话——发第一条消息后这里会出现会话。</div>
      )}
      {sessions.map((s) => (
        <div
          key={s.id}
          role="option"
          aria-selected={s.id === activeId}
          aria-current={s.id === activeId ? "page" : undefined}
          className={"session-item" + (s.id === activeId ? " active" : "") + (s.running ? " busy" : "")}
          title={s.title}
          tabIndex={0}
          onClick={() => onSelect(s.id)}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              onSelect(s.id);
            }
          }}
        >
          <div className="session-line">
            <span className={"s-dot" + (s.running ? " live" : "")} aria-hidden />
            <span className="title">{s.title}</span>
            {/* 运行中加强态：标题旁 spinner（与主域 SessionRow 同一件） */}
            {s.running && <span className="mset-spinner session-spinner" aria-hidden />}
            <span className="time">{s.updatedAt}</span>
          </div>
        </div>
      ))}
    </div>
  );
});
