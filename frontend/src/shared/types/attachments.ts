// 附件引用（对齐 llm.AttachmentRef）。

/** 附件引用（对齐 llm.AttachmentRef）：附件卡的展示元数据（实时事件与历史
 *  回放同一份）。path 是相对后端 attachments 根的路径，前端经
 *  /attachments/<path> 取内容（shared/attachments.ts 的 attachmentURL）。
 *  kind 分流渲染：image = 内联缩略图（点击放大），file = 文件卡（点击下载）。 */
export interface AttachmentRef {
  path: string;
  name: string;
  /** 字节数（0/缺省 = 未知——文件卡不显示大小，不显示 0 B 假数据）。 */
  size?: number;
  mime?: string;
  kind: "image" | "file";
  /** 共享者附的一句话说明（share_file 的可选文案）。 */
  caption?: string;
}
