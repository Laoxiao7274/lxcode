// 助手模式的会话标签条（2026-12 方案 B：助手侧多标签，多会话并存、保活切换）。
//
// **轻量自绘**（不复用 TabBar——主域标签条带工作区页签/视图态/溢出动效一整套，
// 助手域只要「会话标签 + 关闭」）：形态对齐主域 .tab 药丸语言，但**类名独立**
//（.asst-tab*），样式只进 assistant-mode.css 且只用 --asst-* 容器级 token
//（选中态 --asst-accent-soft 助手紫 wash）——不动 :root。
//
// 规则全部在 shared/session-tabs.ts 纯函数（seed/append/visible/prune/fallback，
// 零改动复用——session-tabs.test.mjs 钉住），本组件只管渲染：点击 = onSelect
//（AssistantWorkspace 的 handleSelect：append 标签 + 切 active + 清子会话/二级
// 视图），× = onClose（关标签语义在 AssistantWorkspace 收口：非当前只摘标签，
// 当前标签回退最近打开的——绝不新建会话，session-tab 回退语义同主域）。
//
// 简化面（与主域 TabBar 的差异，均为有意）：不做溢出渐隐/滚轮横滚（useStripOverflow
// 不提取——助手标签容量 8 且 252px 侧栏旁的主区宽度足够，先不做溢出动效）；
// 不做活动标签滚进可视区。溢出兜底 = overflow-x: auto（scrollbar 隐藏）。
import { memo } from "react";

export interface AssistantTabItem {
  id: string;
  title: string;
  /** 该会话正在生成（标签上的 spinner 与主域同款 mset-spinner）。 */
  busy: boolean;
}

export const AssistantTabStrip = memo(function AssistantTabStrip({
  tabs,
  activeId,
  onSelect,
  onClose,
}: {
  /** 可见标签（AssistantWorkspace 用 visibleSessionTabs 算——剔除归档/已关闭）。 */
  tabs: AssistantTabItem[];
  /** 当前活跃标签（= sessionId；中性态 "" = 无高亮）。 */
  activeId: string;
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
}) {
  if (tabs.length === 0) return null;
  return (
    <div className="asst-tabstrip" role="tablist" aria-label="助手会话标签" data-tabs={String(tabs.length)}>
      {tabs.map((tab) => {
        const on = tab.id === activeId;
        return (
          <div key={tab.id} className={"asst-tab" + (on ? " on" : "")} role="tab" aria-selected={on} title={tab.title}>
            <button
              type="button"
              className="asst-tab-main"
              onClick={() => onSelect(tab.id)}
            >
              {tab.busy && <span className="mset-spinner asst-tab-spinner" aria-hidden />}
              <span className="asst-tab-title">{tab.title}</span>
            </button>
            <button
              type="button"
              className="asst-tab-close"
              onClick={() => onClose(tab.id)}
              aria-label={`关闭标签「${tab.title}」`}
              title="关闭标签（会话保留在侧栏）"
            >
              <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          </div>
        );
      })}
    </div>
  );
});
