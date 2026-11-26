// 发送缓冲区排队条（共享组件，2026-12 助手域接入发送缓冲区时抽出）：主域
// Composer 与助手域 AssistantInput 渲染**同一份**排队条 JSX——原先整段长在
// Composer.tsx 里，助手域复用被抽出来。样式类（.send-queue 系）是 composer.css
// 的全局类，两域同视觉零复制。
//
// 纯展示组件：动作回调可缺席（AssistantInput 的编辑动作经 key 重挂回填，见
// AssistantPage 的接线——缺席的按钮渲染但 no-op 违反组件纪律，所以缺席的
// 回调连按钮都不渲染，主域行为不变）。
import type { SendQueueItem } from "../../shared/send-queue";
import { AttachmentThumb, clipOneLine } from "./Composer";

export function SendQueueBar({
  queue = [],
  queueNotice = "",
  onQueueEdit,
  onQueueDelete,
  onQueueSend,
}: {
  queue?: SendQueueItem[];
  /** 一次性提示（「已置顶…」——use-send-queue 的 queueNotice）。 */
  queueNotice?: string;
  /** 队列条目「编辑」：把该条回填输入框并从队列移除（缺席 = 不渲染该按钮）。 */
  onQueueEdit?: (id: string) => void;
  /** 队列条目「删除」：从队列移除。 */
  onQueueDelete?: (id: string) => void;
  /** 队列条目「直接发送」：空闲立即发，忙时置顶。 */
  onQueueSend?: (id: string) => void;
}) {
  if (queue.length === 0) return null;
  return (
    <div className="send-queue" role="list" aria-label="发送缓冲区">
      {queueNotice && (
        <div className="send-queue-notice" role="status">{queueNotice}</div>
      )}
      {queue.map((item, index) => (
        <div className="send-queue-item" role="listitem" key={item.id}>
          <span className="send-queue-index" aria-hidden="true">{index + 1}</span>
          <span className="send-queue-text" title={item.text}>{clipOneLine(item.text, 80)}</span>
          {(item.images?.length || item.files?.length) ? (
            <span className="send-queue-atts" aria-label={`附件：图片 ${item.images?.length ?? 0} 张，文件 ${item.files?.length ?? 0} 个`}>
              {item.images?.map((im, i) => (
                <AttachmentThumb key={i} mime={im.mime} data={im.data} file={im.file} alt="排队消息的图片附件" small />
              ))}
              {item.files?.map((f, i) => (
                <span key={i} className="send-queue-file" title={f.name}>{clipOneLine(f.name, 18)}</span>
              ))}
            </span>
          ) : null}
          {onQueueSend && (
            <button type="button" className="send-queue-act" title="直接发送" aria-label="直接发送该条" onClick={() => onQueueSend(item.id)}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M12 19V5" />
                <path d="m5 12 7-7 7 7" />
              </svg>
            </button>
          )}
          {onQueueEdit && (
            <button type="button" className="send-queue-act" title="编辑（放回输入框）" aria-label="编辑该条（放回输入框）" onClick={() => onQueueEdit(item.id)}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" />
              </svg>
            </button>
          )}
          {onQueueDelete && (
            <button type="button" className="send-queue-act" title="删除" aria-label="删除该条" onClick={() => onQueueDelete(item.id)}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          )}
        </div>
      ))}
    </div>
  );
}
