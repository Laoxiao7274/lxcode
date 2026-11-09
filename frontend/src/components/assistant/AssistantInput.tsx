// 助手模式的输入区（助手模式视觉对齐主域，2026-12）：**Composer 的视觉壳**——
// DOM 与类名直接用主域 composer.css 的全局类（.pi / .pi-row / .piInput / .piBar /
// .send-btn / .busy-row 都是全局类，composer.css 随 global.css 全量加载），
// 视觉逐像素对齐主域输入卡（20px 圆角白卡 + ring shadow + focus-within 提亮、
// 14px 输入、32px 正圆发送钮），不需要复制任何样式。
//
// piBar 四件套（2026-12 piBar 接入）：PermPicker / ModelPicker / ContextIndicator /
// StatsPills——与主域 Composer 的差别：助手域**没有** AgentPicker（内置助手不在
// 库名单，串域不接）。四件套的 props 全部来自助手域 state（approval/model/context/
// stats 由同一份归约器写入 assistantStates），改档/切模型走 assistant.* 方法域。
// 斜杠面板 / 发送队列仍是助手域范围外，刻意不渲染。
//
// 附件（2026-12 用户拍板：assistant.send 也带 images/files）：「+」按钮 = 主域
// Composer 附件区的**图片精简版**（single-menu-item——助手域不带文件入口）；
// 暂存/读入/上限校验复用 shared/attachments + shared/attachment-read 的同一份纯
// 函数与暂存通道（不另写副本），预览行复用 .att-preview 样式与 AttachmentThumb
// 组件（Composer 导出的同一份）；发送组装 attsFromPending → onSend。
// vision 能力门照主域：visionBlocked 非空时「+」禁用 + title 说明（感知尽力而为，
// 后端 vision 校验兜底）。
//
// 保持流内定位（不套 .composer-zone absolute——会破坏 flex 贴底）；
// 不发布 --composer-h（AssistantWorkspace 的归零规则保持）。
// Enter 发送 / Shift+Enter 换行（与 Composer 的 onKey 同语义）。
import { useEffect, useRef, useState } from "react";
import { useEnterRef } from "../../shared/anim";
import { usePopover } from "../../shared/popover";
import { PermPicker } from "../perm-picker";
import { ModelPicker } from "../model-picker";
import { ContextIndicator } from "../context-indicator";
import { StatsPills } from "../stats-pills";
import { AttachmentThumb } from "../composer";
import { TodoList } from "../../aicss/TodoList";
import type { TodoItem } from "../../shared/types";
import {
  addPendingImages,
  attsFromPending,
  formatBytes,
  hasPendingAtts,
  isImageMime,
  MAX_IMAGE_BYTES,
  MAX_IMAGE_COUNT,
  removePending,
  type PendingImage,
} from "../../shared/attachments";
import { stageFile } from "../../shared/attachment-read";
import type { ApprovalMode, ContextUsage, SessionStats, SendAttachments } from "../../shared/types";

export function AssistantInput({
  busy,
  sending = false,
  onSend,
  onCancel,
  disabled = false,
  approval,
  onPickApproval,
  model,
  onPickModel,
  context,
  stats,
  onCompact,
  compacting = false,
  visionBlocked = null,
  todos = [],
}: {
  busy: boolean;
  /** 发送中态（乐观气泡已插、busy 还没到）：与主域 Composer 同语义——
   *  busy-row 显示「发送中…」，到达后无缝切「生成中」。 */
  sending?: boolean;
  /** 发送（atts 非空 = 随消息的图片附件；只附件不打字也允许——后端以
   *  「[附件]」行充当正文）。 */
  onSend: (text: string, atts: SendAttachments) => void;
  onCancel: () => void;
  disabled?: boolean;
  /** 当前助手会话的权限档（state.approval；null = 未知——PermPicker 回落默认档显示）。 */
  approval?: ApprovalMode | null;
  /** 改档入口（assistant.setApproval——乐观对齐由 assistant.approvalChanged 广播回写）。 */
  onPickApproval?: (mode: ApprovalMode) => void;
  /** 当前助手会话的模型 id（state.model；空 = 未知——ModelPicker 显示中性态）。 */
  model?: string;
  /** 切模型入口（assistant.model——不乐观改，由 assistant.modelChanged 对齐）。 */
  onPickModel?: (id: string) => void;
  /** 上下文占用（state.context；null = 未知——指示器显示中性态「—」）。 */
  context?: ContextUsage | null;
  /** 整段会话统计（state.stats；null = 还没有任何一步——不渲染统计胶囊）。 */
  stats?: SessionStats | null;
  /** 手动压缩入口（assistant.compact——空闲才可用）。 */
  onCompact?: () => void;
  /** 压缩中态（useCompaction 的 compacting——ContextIndicator 的按钮反馈）。 */
  compacting?: boolean;
  /** 非空 = 当前模型未声明 vision 能力（值为人话原因）：「+」禁用；null/undefined =
   *  能力未知（老后端/模型没配）→ 不禁用，发送时依赖后端 vision 校验兜底。 */
  visionBlocked?: string | null;
  /** 任务清单（state.todos；空数组 = 无清单——照主域 Composer 的 .pi-plan 形态，
   *  白卡顶部区块不渲染，.pi 形态与无清单时一致）。 */
  todos?: TodoItem[];
}) {
  const [text, setText] = useState("");
  const [images, setImages] = useState<PendingImage[]>([]);
  const [notice, setNotice] = useState("");
  const taRef = useRef<HTMLTextAreaElement>(null);
  const imgInputRef = useRef<HTMLInputElement>(null);
  /** 暂存区的 ref 镜像（异步读文件期间 state 可能已变——追加要基于最新值）。 */
  const imagesRef = useRef(images);
  imagesRef.current = images;
  /** 一次性提示的计时器（4s 自动消失，与主域 Composer 同款节奏）。 */
  const noticeTimerRef = useRef<number | null>(null);
  // 「生成中」状态行挂载即上浮淡入（与 Composer 的 busyRowRef 同一件 shared/anim）
  const busyRowRef = useEnterRef<HTMLDivElement>({ opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
  // 「+」按钮的弹出菜单（点外/Esc 关闭 + 退场动画——shared/popover 的通用接线）
  const addMenu = usePopover();
  const locked = disabled;
  // 发送中不可直发（消息已交出去）——与 Composer 的 route 语义一致（简化版：
  // 助手域没有队列，busy/sending 之外只有「有没有文本/附件」一个维度；
  // hasAttachments：只附件没文本也允许发——后端以「[附件]」行充当正文）。
  const canSend = !locked && !sending && !busy && (text.trim().length > 0 || hasPendingAtts(images, []));

  // 压缩中态（compacting）由上层持有（useCompaction——超时尾巴/重入闸在钩子里，
  // 这里只透传；与主域 Composer 的 onCompact/compacting 同一形状）。

  // 自增高（DSH 形态，与 Composer 同一段逻辑）：随内容长高到上限
  //（CSS max-height 200px），之内不出内部滚动条，到上限后才允许内部滚动。
  // 发送清空走 text 变化 → 自动复位。
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    const max = 200;
    ta.style.height = Math.min(ta.scrollHeight, max) + "px";
    ta.style.overflowY = ta.scrollHeight > max ? "auto" : "hidden";
  }, [text]);

  const flashNotice = (msg: string) => {
    setNotice(msg);
    if (noticeTimerRef.current !== null) window.clearTimeout(noticeTimerRef.current);
    noticeTimerRef.current = window.setTimeout(() => setNotice(""), 4000);
  };

  const submit = () => {
    const t = text.trim();
    if ((!t && images.length === 0) || locked || sending) return;
    onSend(t, attsFromPending(images, []));
    setText("");
    setImages([]);
    requestAnimationFrame(() => taRef.current?.focus());
  };

  // Enter 发送 / Shift+Enter 换行（与 Composer 的 onKey 同语义；合成中不拦截）
  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  // 图片选入（「+」菜单触发；与主域 Composer 的 ingest 同一条暂存路径——
  // 路径优先、base64 兜底、上限先于读入拦截）：
  const pickFiles = (e: React.ChangeEvent<HTMLInputElement>) => {
    const list = Array.from(e.target.files ?? []);
    e.target.value = ""; // 允许重复选同一个文件
    if (list.length === 0) return;
    const oversize = list.find((f) => isImageMime(f.type) && f.size > MAX_IMAGE_BYTES);
    if (oversize) {
      flashNotice(`图片 ${oversize.name} 超过 5MB 上限（当前 ${formatBytes(oversize.size)}）`);
      return;
    }
    void (async () => {
      try {
        const staged = await Promise.all(list.map((f) => stageFile(f)));
        // 追加基于 ref 镜像（await 期间用户可能又加了/删了附件）
        const res = addPendingImages(
          imagesRef.current,
          staged.map((s, i) => s.path
            ? { mime: list[i].type, name: list[i].name, path: s.path, file: s.file, size: s.file.size }
            : { mime: list[i].type, name: list[i].name, data: s.data ?? "" }),
        );
        setImages(res.images);
        if (res.notice) flashNotice(res.notice);
      } catch (err) {
        flashNotice(`读取文件失败: ${err instanceof Error ? err.message : String(err)}`);
      }
    })();
  };

  const removeImage = (id: string) => setImages((cur) => removePending(cur, id));

  return (
    <div className="assistant-input">
      {/* busy/sending 状态行：浮在输入卡上方（与主域 Composer 同一拍——
          busy 到达后文案无缝从「发送中…」切到「生成中」） */}
      {(busy || sending) && (
        <div className="busy-row" ref={busyRowRef}>
          <span className="mset-spinner" aria-hidden />
          <span className="busy-text">{busy ? "生成中" : "发送中…"}</span>
        </div>
      )}
      <div className="pi">
        {/* 任务清单区：照抄主域 Composer 的 .pi-plan 形态（白卡顶部区块，
            清单 + 发丝分隔线 + 输入区 = 一张卡）；todos 为空不渲染。 */}
        {todos.length > 0 && (
          <div className="pi-plan">
            <TodoList items={todos} />
          </div>
        )}
        {(images.length > 0 || notice) && (
          <div className="att-preview" role="region" aria-label="附件预览">
            {notice && <div className="att-notice" role="status">{notice}</div>}
            {images.length > 0 && (
              <div className="att-thumbs">
                {images.map((im) => (
                  <AttachmentThumb key={im.id} mime={im.mime} data={im.data} file={im.file} alt={`图片附件 ${im.name || ""}`} title={im.name || undefined} onRemove={() => removeImage(im.id)} />
                ))}
              </div>
            )}
          </div>
        )}
        <div className="pi-row">
          {/* 「+」添加图片（2026-12 助手附件）：主域 att-add 的图片精简版——
              32px 正圆、菜单向上弹、vision 门禁用。locked（不可用会话）不渲染
              （与主域 Composer 的 locked 语义一致）。 */}
          {!locked && (
            <span className="pop-wrap att-add-wrap" ref={addMenu.rootRef}>
              <button
                type="button"
                className="att-add"
                disabled={Boolean(visionBlocked)}
                title={visionBlocked ?? "添加图片"}
                aria-label="添加图片"
                aria-haspopup="menu"
                aria-expanded={addMenu.open}
                onClick={addMenu.toggle}
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                  <path d="M12 5v14M5 12h14" />
                </svg>
              </button>
              {addMenu.open && (
                <div className="att-menu" role="menu" data-pop>
                  <button
                    type="button"
                    className="att-menu-item"
                    role="menuitem"
                    title="从本地选择图片（png/jpeg/webp/gif，最多 4 张，单张 5MB 内）"
                    onClick={() => { imgInputRef.current?.click(); addMenu.requestClose(); }}
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <rect x="3" y="3" width="18" height="18" rx="2" />
                      <circle cx="8.5" cy="8.5" r="1.5" />
                      <path d="m21 15-5-5L5 21" />
                    </svg>
                    图片
                  </button>
                </div>
              )}
              {/* 隐藏的文件选择入口（菜单项触发；multiple 一次选多个） */}
              <input
                ref={imgInputRef}
                type="file"
                accept="image/png,image/jpeg,image/webp,image/gif"
                multiple
                className="att-input"
                aria-hidden="true"
                tabIndex={-1}
                onChange={pickFiles}
              />
            </span>
          )}
          <textarea
            ref={taRef}
            className="piInput"
            placeholder={locked ? "当前会话不可用" : busy ? "生成中… 可先输入" : "把日常事务交给助手…"}
            rows={1}
            value={text}
            disabled={locked}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKey}
          />
        </div>
        <div className="piBar">
          {/* 权限与模型一组（与主域 Composer 的 .pi-controls 同款分组；助手域
              没有 AgentPicker）；ContextIndicator/StatsPills 是读数，留在组外。 */}
          <span className="pi-controls">
            <PermPicker
              approval={approval ?? undefined}
              onPick={onPickApproval ? (mode) => onPickApproval(mode) : undefined}
            />
            <span className="pi-model-slot">
              <ModelPicker model={model} onPickModel={onPickModel} chipTitle={busy ? "正在运行，切换将在下一轮生效" : undefined} />
            </span>
          </span>
          <ContextIndicator
            usage={context ?? null}
            stats={stats ?? null}
            onCompact={onCompact}
            busy={busy}
            compacting={compacting}
          />
          <StatsPills stats={stats ?? null} />
          <span className="piTips" />
          {busy ? (
            <button type="button" className="send-btn stop" onClick={onCancel} aria-label="停止生成" title="停止生成">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                <rect x="5" y="5" width="14" height="14" rx="2.5" />
              </svg>
            </button>
          ) : (
            <button
              type="button"
              className="send-btn"
              disabled={!canSend}
              onClick={submit}
              aria-label="发送"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M12 19V5" />
                <path d="m5 12 7-7 7 7" />
              </svg>
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
