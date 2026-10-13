// 附件卡（图片批次 B 的展示通道）：图片 → 内联缩略图（点击放大预览），
// 其他文件 → 文件卡（图标 + 名字 + 大小，点击下载）。实时（chat.userMessage /
// chat.toolResult 的 attachments）与回放（chat.history 的同一份元数据）两条
// 路径共用这一个组件——两条路径各写一套渲染就必然分叉。
import { memo, useEffect, useState } from "react";
import { attachmentURL, formatBytes, type AttachmentMeta } from "../../../shared/attachments";

/** 一组附件卡（用户气泡 / share_file 工具行共用）。atts 为空不渲染。 */
export function AttachmentCards({ atts }: { atts?: AttachmentMeta[] }) {
  if (!atts?.length) return null;
  return (
    <div className="att-cards">
      {atts.map((a, i) => (
        <AttachmentCard key={`${a.path}-${i}`} att={a} />
      ))}
    </div>
  );
}

/** 单张附件卡。取件 URL 构造失败（引用形态不对/已带穿越）→ 不可点的死卡
 *  + 名字照显：宁可难看，不静默丢内容。 */
const AttachmentCard = memo(function AttachmentCard({ att }: { att: AttachmentMeta }) {
  const url = attachmentURL(att.path);
  if (att.kind === "image") {
    return <ImageCard att={att} url={url} />;
  }
  return (
    <a
      className={"att-file" + (url ? "" : " att-dead")}
      href={url ?? undefined}
      download={url ? att.name : undefined}
      onClick={(e) => {
        if (!url) e.preventDefault();
      }}
      title={att.caption || `${att.name}（点击下载）`}
    >
      <span className="att-file-icon" aria-hidden>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
          <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Z" />
          <path d="M13 2v7h7" />
        </svg>
      </span>
      <span className="att-file-body">
        <span className="att-name">{att.name || "文件"}</span>
        <span className="att-sub">{fileSub(att)}</span>
      </span>
    </a>
  );
});

/** 图片缩略图卡：加载失败降级成文件卡形态（引用指向已清理的文件时——
 *  附件不随会话删除，但历史可能比磁盘活得久），不渲染破图。 */
function ImageCard({ att, url }: { att: AttachmentMeta; url: string | null }) {
  const [broken, setBroken] = useState(false);
  const [zoom, setZoom] = useState(false);
  useEffect(() => {
    if (!zoom) return;
    const close = (e: KeyboardEvent) => {
      if (e.key === "Escape") setZoom(false);
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [zoom]);

  if (!url || broken) {
    return (
      <span className="att-file att-dead" title={att.name}>
        <span className="att-file-icon" aria-hidden>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="3" width="18" height="18" rx="2" />
            <circle cx="9" cy="9" r="2" />
            <path d="m21 15-3.1-3.1a2 2 0 0 0-2.8 0L6 21" />
          </svg>
        </span>
        <span className="att-file-body">
          <span className="att-name">{att.name || "图片"}</span>
          <span className="att-sub">图片不可用（源文件已不在）</span>
        </span>
      </span>
    );
  }
  return (
    <>
      <button
        type="button"
        className="att-image"
        onClick={() => setZoom(true)}
        title={att.caption || `${att.name}（点击放大）`}
      >
        <img src={url} alt={att.name || "图片附件"} loading="lazy" decoding="async" onError={() => setBroken(true)} />
        {(att.caption || att.name) && (
          <span className="att-image-cap">{att.caption || att.name}</span>
        )}
      </button>
      {zoom && (
        <div className="att-lightbox" role="dialog" aria-label="图片预览" onClick={() => setZoom(false)}>
          <img src={url} alt={att.name || "图片附件"} decoding="async" />
          {(att.caption || att.name) && <span className="att-lightbox-cap">{att.caption || att.name}</span>}
        </div>
      )}
    </>
  );
}

/** 文件卡的副行：大小（未知不显示——0 B 是假数据）+ 说明。 */
function fileSub(att: AttachmentMeta): string {
  const parts: string[] = [];
  if (att.size && att.size > 0) parts.push(formatBytes(att.size));
  if (att.caption) parts.push(att.caption);
  return parts.join(" · ");
}
