// 权限选择器（Codex 式）：单项菜单——每项 = 图标 + 名称 + 一行说明，选中打勾。
// 三档策略：默认 = 低危自动 + 高危确认；完全访问 = 全部自动（仅隔离环境）；
// 只读 = 变更类工具直接拒绝。
//
// 改档走 applyApproval：本地持久化 + **立刻发给后端**（chat.approval）。两者缺一
// 不可——只改本地设置的话，正在跑的那一轮还在按开轮时的档位弹确认（用户实测）。
//
// **切「完全访问」要弹窗确认**（2026-10-09 用户拍板，取代 10-07 的「再次点击」
// 两步确认）：auto = 高危工具不再问人，误触一下就把执行面全放开——先弹
// ApprovalConfirmDialog 说清后果，确认才真的改档；降险方向直接切。
// 分流规则在 shared/approval.ts（approvalPick），与设置面板共用一份。
import { useEffect, useState } from "react";
import { useSettings, APPROVALS } from "../../shared/settings";
import { approvalPick } from "../../shared/approval";
import { usePopover } from "../../shared/popover";
import { IconCheck, IconChevronDown } from "../icons";
import { ApprovalConfirmDialog } from "./ApprovalConfirmDialog";
import type { ApprovalMode } from "../../shared/types";

const ICONS: Record<string, string> = {
  confirm: "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z",
  auto: "M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z",
  strict: "M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z",
};

export function PermPicker() {
  const { settings, applyApproval } = useSettings();
  const { open, toggle, requestClose, rootRef } = usePopover();
  // 「完全访问」确认弹窗（只在菜单开着时有意义；菜单收起即复位——下次打开
  // 必须重新确认，不能带着上次的半确认态）
  const [confirmOpen, setConfirmOpen] = useState(false);
  useEffect(() => {
    if (!open) setConfirmOpen(false);
  }, [open]);

  const current = APPROVALS.find((p) => p.id === settings.approval) ?? APPROVALS[0];

  // 升险到 auto 先弹窗（askConfirm），其余直接改——分流规则与设置面板共用一份
  const pick = (id: ApprovalMode) => approvalPick(settings.approval, id, applyApproval, () => setConfirmOpen(true));

  const confirmAuto = () => {
    setConfirmOpen(false);
    applyApproval("auto");
    requestClose();
  };

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
        <>
          <div className="perm-menu" role="menu" data-pop>
            {APPROVALS.map((p) => {
              return (
                <button
                  key={p.id}
                  type="button"
                  className={"perm-item" + (settings.approval === p.id ? " on" : "")}
                  role="menuitem"
                  title={p.hint}
                  onClick={() => pick(p.id)}
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
              );
            })}
          </div>
          {confirmOpen && (
            <ApprovalConfirmDialog onConfirm={confirmAuto} onCancel={() => setConfirmOpen(false)} />
          )}
        </>
      )}
    </div>
  );
}
