// 附件卡（图片批次 B 的展示通道）：图片 → 内联缩略图（点击放大预览），
// 其他文件 → 文件卡（图标 + 名字 + 大小，点击下载）。实时（chat.userMessage /
// chat.toolResult 的 attachments）与回放（chat.history 的同一份元数据）两条
// 路径共用这一个组件——两条路径各写一套渲染就必然分叉。
//
// 灯箱（2026-12 重做）：三处内联灯箱实现收口到一个全局 ImageLightbox
// （shared/lightbox.tsx，portal + 缩放/平移/多图切换/工具条）。缩略图只负责
// openImageLightbox(images, index)——「放大看」的状态不再散在每张卡里：
//  - ImageCard / GridImage（卡内自足形态）不带 onClick 时打开以自身为
//    单元素集合的灯箱（与旧行为等价）；
//  - ImageGrid 带整区集合（含未展示的 +N）与起始下标——灯箱内左右切换
//    覆盖该附件区的全部图片；
//  - 溢出层（+N 展开态）点单张同样进灯箱（集合 = 全区图片）。
import { memo, useState } from "react";
import { attachmentURL, formatBytes, groupMetaFolders, type AttachmentMeta } from "../../../shared/attachments";
import { lightboxImages, openImageLightbox } from "../../../shared/lightbox";
import { FolderCard } from "./FolderCard";

/** 一组附件卡（share_file 工具行 / dispatch 成果区共用）。atts 为空不渲染。 */
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

/** 用户消息的附件区（千问式独立区块，2026-12 重做）：**在气泡上方、气泡外**
 *  独立排布，与文字气泡视觉分离——不再是气泡内挤在文字前面的一段内容。
 *
 *  布局语义对齐千问网页版（从其打包 CSS/JS 读到的实际几何）：
 *  - 图片：`common-image-grid`——统一方格网格（repeat(auto-fill,固定边长)、
 *    gap 8px、12px 圆角、object-fit:cover），超出显示上限时末格盖 +N 溢出层
 *    （common-image-grid-overflow：半透明遮罩 + 计数，点开看全部）；
 *  - 文件：`file-card-list`——横排卡行（flex wrap、gap 8px），卡 = 图标 +
 *    文件名 + 大小（attachment-card 的 info/title/size 三段式）；
 *  - 附件区与正文分属两个容器（千问把附件渲染在气泡外、文字在气泡内）。
 *
 *  功能与旧形态一致：点击灯箱/下载、加载失败降级、token URL 机制不动。
 *  atts 为空不渲染。 */
export function UserAttachmentSection({ atts }: { atts?: AttachmentMeta[] }) {
  if (!atts?.length) return null;
  const images = atts.filter((a) => a.kind === "image");
  const files = atts.filter((a) => a.kind !== "image");
  // URL 构造失败（穿越/形态不对）的图片**不进网格**——降级成文件行的死卡
  // （att-dead，名字照显）：网格留死方块或静默消失都比"看得见的坏消息"差。
  const gridImgs = images.filter((a) => attachmentURL(a.path));
  const badImgs = images.filter((a) => !attachmentURL(a.path));
  const fileRow = [...badImgs, ...files];
  // 文件夹分组（拖文件夹改造，2026-12）：带 relPath 的文件按顶层目录归组成
  // 文件夹卡（与暂存区同构）；散文件维持平铺文件卡。归组判定的纯逻辑在
  // shared/attachments.ts 的 groupMetaFolders。
  const { groups, loose } = groupMetaFolders(fileRow);
  if (!gridImgs.length && !groups.length && !loose.length) return null;
  return (
    <div className="user-atts">
      {gridImgs.length > 0 && <ImageGrid atts={gridImgs} />}
      {(groups.length > 0 || loose.length > 0) && (
        <div className="user-att-files">
          {groups.map((g) => (
            <FolderCard key={g.name} group={g} />
          ))}
          {loose.map((a, i) => (
            <AttachmentCard key={`${a.path}-${i}`} att={a} />
          ))}
        </div>
      )}
    </div>
  );
}

/** 千问式图片网格：统一 96px 方格（千问 120px，在气泡流里偏大，96 保留同一
 *  几何：方格 + cover + 12px 圆角 + gap 8px），超过 4 张收进末格的 +N 溢出层。 */
const USER_IMAGE_GRID_MAX = 4;

function ImageGrid({ atts }: { atts: AttachmentMeta[] }) {
  const [overflowOpen, setOverflowOpen] = useState(false);
  const shown = atts.slice(0, USER_IMAGE_GRID_MAX);
  const hidden = atts.length - shown.length;
  // 灯箱集合 = 本附件区全部可放大图片（含未展示的 +N）；下标按 atts 对齐。
  const openAt = (i: number) => openImageLightbox(lightboxImages(atts), i);
  return (
    <div className="att-grid" role="list" aria-label={`图片附件 ${atts.length} 张`}>
      {shown.map((a, i) => (
        <GridImage key={`${a.path}-${i}`} att={a} onClick={() => openAt(i)} />
      ))}
      {hidden > 0 && (
        <button
          type="button"
          className="att-grid-more"
          onClick={() => setOverflowOpen(true)}
          aria-label={`查看全部 ${atts.length} 张图片`}
        >
          +{hidden}
        </button>
      )}
      {/* 溢出层（千问 common-image-grid-overflow 的「查看全部」）：全屏遮罩 +
          全量网格。点遮罩空白处关闭；单张仍点开放大灯箱（stopPropagation
          隔离两层的点击关闭语义——外层关全部，内层进灯箱）。 */}
      {overflowOpen && (
        <div
          className="att-grid-full"
          role="dialog"
          aria-label="全部图片"
          onClick={() => setOverflowOpen(false)}
        >
          <div className="att-grid-full-grid">
            {atts.map((a, i) => (
              <FullGridImage key={`${a.path}-${i}`} att={a} onOpen={() => openAt(i)} />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

/** 溢出层里的可放大图。 */
function FullGridImage({ att, onOpen }: { att: AttachmentMeta; onOpen: () => void }) {
  return (
    <span className="att-grid-full-item">
      <GridImage att={att} onClick={onOpen} />
    </span>
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

/** 图片缩略图卡（气泡流 / 卡内形态）：加载失败降级成文件卡形态（引用指向
 *  已清理的文件时——附件不随会话删除，但历史可能比磁盘活得久），不渲染破图。 */
function ImageCard({ att, url }: { att: AttachmentMeta; url: string | null }) {
  const [broken, setBroken] = useState(false);
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
    <button
      type="button"
      className="att-image"
      onClick={() => openImageLightbox(lightboxImages([att]), 0)}
      title={att.caption || `${att.name}（点击放大）`}
    >
      <img src={url} alt={att.name || "图片附件"} loading="lazy" decoding="async" onError={() => setBroken(true)} />
      {(att.caption || att.name) && (
        <span className="att-image-cap">{att.caption || att.name}</span>
      )}
    </button>
  );
}

/** 网格单元里的图（千问 common-image-grid-image 语义）：纯方格 cover，
 *  不带 caption 行——caption 进 title，点击进灯箱（onClick 由上层给——
 *  上层知道整区集合，灯箱才能左右切换；不带 onClick 时自足开单图灯箱）。
 *  加载失败不渲染破图，但**留占位格**（坏图格可辨——静默消失会让网格悄悄
 *  缩水，用户不知道少了一张）。 */
function GridImage({ att, onClick }: { att: AttachmentMeta; onClick?: () => void }) {
  const [broken, setBroken] = useState(false);
  const url = attachmentURL(att.path);
  if (!url) return null;
  return (
    <button
      type="button"
      className={"att-grid-item" + (broken ? " att-grid-broken" : "")}
      role="listitem"
      onClick={() => {
        if (broken) return;
        if (onClick) onClick();
        else openImageLightbox(lightboxImages([att]), 0);
      }}
      title={att.caption || `${att.name}（点击放大）`}
    >
      {broken ? (
        <span className="att-grid-broken-label" title="图片不可用（源文件已不在）">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
            <rect x="3" y="3" width="18" height="18" rx="2" />
            <circle cx="9" cy="9" r="2" />
            <path d="m21 15-3.1-3.1a2 2 0 0 0-2.8 0L6 21" />
          </svg>
        </span>
      ) : (
        <img src={url} alt={att.name || "图片附件"} loading="lazy" decoding="async" onError={() => setBroken(true)} />
      )}
    </button>
  );
}

/** 文件卡的副行：大小（未知不显示——0 B 是假数据）+ 说明。 */
function fileSub(att: AttachmentMeta): string {
  const parts: string[] = [];
  if (att.size && att.size > 0) parts.push(formatBytes(att.size));
  if (att.caption) parts.push(att.caption);
  return parts.join(" · ");
}
