// 权限选择器（Codex 式）：单项菜单——每项 = 图标 + 名称 + 一行说明，选中打勾。
// 三档策略随消息发送（chat.send 的 approval 参数）：默认 = 低危自动 + 高危
// 确认；完全访问 = 全部自动（仅隔离环境）；只读 = 变更类工具直接拒绝。
import { useSettings, APPROVALS } from "../../shared/settings";
import { usePopover } from "../../shared/popover";
import { IconCheck, IconChevronDown } from "../icons";

const ICONS: Record<string, string> = {
  confirm: "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z",
  auto: "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z",
  strict: "M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z",
};

export function PermPicker() {
  const { settings, set } = useSettings();
  const { open, toggle, requestClose, rootRef } = usePopover();

  const current = APPROVALS.find((p) => p.id === settings.approval) ?? APPROVALS[0];

  return (
    <div className="pop-wrap" ref={rootRef}>
      <span className="pop-trigger" onClick={toggle}>
        <button type="button" className="perm-chip" title={`权限模式：${current.hint}`}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4" />
            <path d="M12 8h.01" />
          </svg>
          {current.label}
          <IconChevronDown size={9} strokeWidth={2.2} />
        </button>
      </span>
      {open && (
        <div className="perm-menu" role="menu" data-pop>
          {APPROVALS.map((p) => (
            <button
              key={p.id}
              type="button"
              className={"perm-item" + (settings.approval === p.id ? " on" : "")}
              role="menuitem"
              title={p.hint}
              onClick={() => {
                set({ approval: p.id });
                requestClose();
              }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d={ICONS[p.id]} />
              </svg>
              <span className="perm-text">
                <span className="perm-label">{p.label}</span>
                <span className="perm-desc">{p.hint}</span>
              </span>
              {settings.approval === p.id && <IconCheck />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
