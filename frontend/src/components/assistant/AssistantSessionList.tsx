// 助手模式的会话列表（助手模式视觉对齐主域，2026-12）：**手写 DOM 用主域
// 行类**（.session-item / .session-line / .s-dot / .title / .time——sidebar.css
// 的既有规则逐像素复用：中性灰选中态、busy 蓝点加大 + spinner + 标题加重）。
//
// 行本体直接复用主域 SessionRow（2026-12 补齐助手域行操作）：⋯ 菜单（重命名/
// 归档）、行内重命名、归档确认弹窗、菜单 gsap 退场全部内聚在 SessionRow 里——
// session.rename/archive 不分 space，助手会话同一套方法可用（此前「assistant.*
// 无 rename/archive」的路 A 注记作废）。running 位映射到 SessionRow 的 busy 位
// （加强态 spinner/蓝点同款）。选中态/时间格式与主域同款，不发明新视觉。
//
// 列表尾「已归档」折叠分组（2026-12）：归档 ≠ 消失——归档会话沉到这里，点
// 「恢复」走 unarchiveSession（对齐归档弹窗文案「记录仍保留，可在设置里恢复」
// 的语义；助手域给就近入口而不是让用户去设置里翻）。
import { memo, useState } from "react";
import type { ArchiveOutcome, SessionMeta } from "../../shared/types";
import { SessionRow } from "../sidebar/SessionRow";

/** 行入场 ref 的 noop 实现：助手列表不做主域的晚到行浮现动画（此前没有，
 *  不为新菜单顺手加）。SessionRow 的 enterRow 是必填 prop，这里补个稳定身份。 */
const noopRowRef = () => undefined;

export const AssistantSessionList = memo(function AssistantSessionList({
  sessions,
  archived,
  activeId,
  renamingId,
  menuForId,
  onOpenMenu,
  onCloseMenu,
  onStartRename,
  onRename,
  onArchive,
  onSelect,
  onRestore,
}: {
  sessions: SessionMeta[];
  /** 已归档会话（列表尾「已归档」折叠分组的数据源；空数组不渲染分组）。 */
  archived: SessionMeta[];
  activeId: string;
  renamingId: string | null;
  menuForId: string | null;
  onOpenMenu: (id: string) => void;
  onCloseMenu: () => void;
  onStartRename: (id: string) => void;
  onRename: (s: SessionMeta, title: string) => void;
  onArchive: (id: string) => Promise<ArchiveOutcome>;
  onSelect: (id: string) => void;
  /** 「已归档」分组的恢复入口（unarchiveSession——列表随 session.changed 刷新）。 */
  onRestore: (id: string) => void;
}) {
  // 折叠态是本组件的纯 UI 态（不持久化——归档区是低频入口，每次进来默认收起）。
  const [archivedOpen, setArchivedOpen] = useState(false);
  return (
    <div className="sess-scroll assistant-ws-sessions" aria-label="助手会话列表">
      {sessions.length === 0 && (
        <div className="assistant-ws-sessions-empty">还没有对话——发第一条消息后这里会出现会话。</div>
      )}
      {sessions.map((s) => (
        <SessionRow
          key={s.id}
          session={s}
          current={s.id === activeId}
          busy={Boolean(s.running)}
          renaming={renamingId === s.id}
          menuOpen={menuForId === s.id}
          onOpenMenu={onOpenMenu}
          onCloseMenu={onCloseMenu}
          onStartRename={onStartRename}
          onRename={onRename}
          onArchive={onArchive}
          onResume={onSelect}
          enterRow={noopRowRef}
        />
      ))}
      {archived.length > 0 && (
        <div className="asst-archived">
          <button
            type="button"
            className="asst-archived-head"
            aria-expanded={archivedOpen}
            onClick={() => setArchivedOpen((v) => !v)}
          >
            <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="m9 6 6 6-6 6" />
            </svg>
            已归档（{archived.length}）
          </button>
          {archivedOpen &&
            archived.map((s) => (
              <div key={s.id} className="session-item asst-archived-row" title={s.title}>
                <div className="session-line">
                  <span className="title">{s.title}</span>
                  <span className="time">{s.updatedAt}</span>
                  <button
                    type="button"
                    className="asst-archived-restore"
                    aria-label={`恢复会话 ${s.title}`}
                    onClick={() => onRestore(s.id)}
                  >
                    恢复
                  </button>
                </div>
              </div>
            ))}
        </div>
      )}
    </div>
  );
});
