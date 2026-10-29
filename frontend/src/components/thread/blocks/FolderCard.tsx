// FolderCard —— 文件夹附件卡（拖文件夹改造，2026-12）：一张卡 = 文件夹图标 +
// 文件夹名 + 「N 个文件 · 总大小」+ 可折叠的内部清单。内部文件按**相对路径**
// 列出（`docs/api/auth.md`——用户要的「内部应该是地址」），不再把顶层文件平铺。
//
// 暂存区（Composer 的文件夹预览）与消息侧（UserAttachmentSection）共用这一个
// 组件形态——两条路径各写一套渲染就必然分叉。
//
// 默认收起：显示计数、点开看清单（16 个文件铺开是噪音；截断说明挂在卡上永远
// 可见——收起也看得见「已收录前 N 个，其余未加入」）。
import { memo, useState } from "react";
import { formatBytes, type FolderGroup } from "../../../shared/attachments";

/** 文件夹图标（实底闭合夹——与散文件卡的线框文件图标区分开）。 */
function FolderIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2Z" />
    </svg>
  );
}

/** 单个文件夹卡。可折叠（默认收起）；onRemove 非空 = 暂存区形态（整卡移除）。 */
export const FolderCard = memo(function FolderCard({ group, onRemove }: {
  group: FolderGroup;
  /** 非空 = 暂存区：整卡移除按钮（移除该文件夹全部内部文件）。 */
  onRemove?: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="folder-card">
      <div className="folder-card-head">
        <span className="folder-card-icon" aria-hidden><FolderIcon /></span>
        <span className="folder-card-name" title={group.name}>{group.name}</span>
        <span className="folder-card-meta">
          {group.files.length} 个文件 · {formatBytes(group.totalBytes)}
        </span>
        <button
          type="button"
          className="folder-card-toggle"
          aria-expanded={open}
          aria-label={open ? `收起 ${group.name} 的文件清单` : `展开 ${group.name} 的文件清单`}
          onClick={() => setOpen((v) => !v)}
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            {open ? <path d="m18 15-6-6-6 6" /> : <path d="m6 9 6 6 6-6" />}
          </svg>
        </button>
        {onRemove && (
          <button type="button" className="att-remove" title={`移除文件夹 ${group.name}`} aria-label={`移除文件夹 ${group.name}`} onClick={onRemove}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        )}
      </div>
      {group.notice && <div className="folder-card-notice" role="status">{group.notice}</div>}
      <ul className={"folder-card-list" + (open ? " open" : "")}>
        {group.files.map((f) => (
          <li key={f.relPath} className="folder-card-file" title={f.relPath}>
            <span className="folder-card-path">{f.relPath}</span>
            {typeof f.size === "number" && f.size > 0 && (
              <span className="folder-card-size">{formatBytes(f.size)}</span>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
});

/** 一组文件夹卡（暂存区 / 消息侧共用）。groups 为空不渲染。 */
export function FolderCards({ groups }: { groups: FolderGroup[] }) {
  if (!groups?.length) return null;
  return (
    <div className="folder-cards" role="list" aria-label="文件夹附件">
      {groups.map((g) => (
        <FolderCard key={g.name} group={g} />
      ))}
    </div>
  );
}
