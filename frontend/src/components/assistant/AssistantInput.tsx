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
// 附件（2026-12 用户拍板：assistant.send 也带 images/files；本次补齐文件入口）：
// 「+」按钮 = 主域 Composer 附件区的同款菜单（图片 + 文件两项——与主域
// Composer.tsx 的 .pi-row 附件区同一形态）；图片/文件的暂存、读入、上限校验、
// 粘贴、拖拽（含文件夹拖入）全部复用 shared/attachments + shared/attachment-read
// + shared/folder-drop 的同一份纯函数与暂存通道（不另写副本），文件预览行复用
// Composer 导出的 FolderFileCards（文件夹卡 + 散文件卡同一份渲染）；发送组装
// attsFromPending → onSend（images 与 files 都上）。
// vision 能力门照主域：只禁**图片菜单项**（visionBlocked 非空时该项目禁用 +
// title 说明）——文件不受 vision 门限（感知尽力而为，后端 vision 校验兜底）。
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
import { AttachmentThumb, FolderFileCards, SendQueueBar } from "../composer";
import { TodoList } from "../../aicss/TodoList";
import { routeSubmit } from "../../shared/send-queue";
import type { TodoItem } from "../../shared/types";
import {
  addPendingFiles,
  addPendingImages,
  attsFromPending,
  formatBytes,
  hasPendingAtts,
  isImageMime,
  MAX_FILE_BYTES,
  MAX_FILE_COUNT,
  MAX_IMAGE_BYTES,
  MAX_IMAGE_COUNT,
  removePending,
  type PendingFile,
  type PendingImage,
} from "../../shared/attachments";
import { collectFolderEntries, defaultFolderDeps, type EntryLike } from "../../shared/folder-drop";
import { stageFile } from "../../shared/attachment-read";
import type { ApprovalMode, ContextUsage, SessionStats, SendAttachments } from "../../shared/types";
import type { SendQueueItem } from "../../shared/send-queue";

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
  queue = [],
  queueNotice = "",
  onQueue,
  onQueueEdit,
  onQueueDelete,
  onQueueSend,
  initialText = "",
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
  /** 发送缓冲区（2026-12 助手域接入——与主域同一份 send-queue 纯函数，键域无关）：
   *  当前助手会话的排队条目；空数组 = 不渲染排队条。 */
  queue?: SendQueueItem[];
  /** 一次性提示（「已置顶…」）。 */
  queueNotice?: string;
  /** busy/sending 时 submit 的去向：入队（→ store.enqueueSend）。缺席 = 旧语义
   *  （忙时不发，未接缓冲区的调用方不变）。 */
  onQueue?: (text: string, atts: SendAttachments) => void;
  /** 队列条目「编辑」：把该条回填输入框并从队列移除（AssistantPage 经 key 重挂
   *  装初始文本——AssistantInput 没有受控 text 通道）。 */
  onQueueEdit?: (id: string) => void;
  /** 队列条目「删除」：从队列移除。 */
  onQueueDelete?: (id: string) => void;
  /** 队列条目「直接发送」：空闲立即发，忙时置顶。 */
  onQueueSend?: (id: string) => void;
  /** 挂载时的初始文本（队列「编辑」回填——AssistantPage 经 key 重挂装进来；
   *  只在 useState 初始化器消费一次，运行中变化不生效）。 */
  initialText?: string;
}) {
  const [text, setText] = useState(initialText);
  const [images, setImages] = useState<PendingImage[]>([]);
  const [files, setFiles] = useState<PendingFile[]>([]);
  const [notice, setNotice] = useState("");
  const taRef = useRef<HTMLTextAreaElement>(null);
  const imgInputRef = useRef<HTMLInputElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  /** 暂存区的 ref 镜像（异步读文件期间 state 可能已变——追加要基于最新值）。 */
  const imagesRef = useRef(images);
  imagesRef.current = images;
  const filesRef = useRef(files);
  filesRef.current = files;
  /** 一次性提示的计时器（4s 自动消失，与主域 Composer 同款节奏）。 */
  const noticeTimerRef = useRef<number | null>(null);
  // 「生成中」状态行挂载即上浮淡入（与 Composer 的 busyRowRef 同一件 shared/anim）
  const busyRowRef = useEnterRef<HTMLDivElement>({ opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.26, ease: "power2.out", clearProps: "transform,opacity" });
  // 「+」按钮的弹出菜单（点外/Esc 关闭 + 退场动画——shared/popover 的通用接线）
  const addMenu = usePopover();
  const locked = disabled;
  // submit 的去向判定在 send-queue.ts 的 routeSubmit（纯函数，与主域 Composer
  // 直测同一份）：空闲 → 正常发送路径；busy/sending/压缩中 → 入发送缓冲区
  //（hasQueue = 调用方接了 onQueue；没接的调用方保持旧语义：忙时不发）。
  // hasAttachments：只附件没文本也允许发——后端以「[附件]」行充当正文。
  const route = (t: string) => routeSubmit({
    text: t, busy, sending, locked, hasQueue: Boolean(onQueue),
    hasAttachments: hasPendingAtts(images, files), compacting,
  });
  const canSend = route(text.trim()) !== "none";

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
    const where = route(t);
    if (where === "none") return;
    const atts = attsFromPending(images, files);
    if (where === "queue") onQueue?.(t, atts);
    else onSend(t, atts);
    setText("");
    setImages([]);
    setFiles([]);
    requestAnimationFrame(() => taRef.current?.focus());
  };

  // Enter 发送 / Shift+Enter 换行（与 Composer 的 onKey 同语义；合成中不拦截）
  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  // 附件选入/粘贴/拖入（与主域 Composer 的 ingest 同一条暂存路径——路径优先、
  // base64 兜底、上限先于读入拦截）：图片走 addPendingImages、其余走
  // addPendingFiles，各自校验各自的口径（单图 5MB / 单文件 20MB / 各 4 个）。
  const ingest = async (list: File[]) => {
    if (list.length === 0) return;
    // 尺寸先于暂存拦截：超大文件不碰内容（读 100MB 只为报错是浪费）
    const oversizeImg = list.find((f) => isImageMime(f.type) && f.size > MAX_IMAGE_BYTES);
    if (oversizeImg) {
      flashNotice(`图片 ${oversizeImg.name} 超过 5MB 上限（当前 ${formatBytes(oversizeImg.size)}）`);
      return;
    }
    const oversizeFile = list.find((f) => !isImageMime(f.type) && f.size > MAX_FILE_BYTES);
    if (oversizeFile) {
      flashNotice(`文件 ${oversizeFile.name} 超过 20MB 上限（当前 ${formatBytes(oversizeFile.size)}）`);
      return;
    }
    try {
      const imgs = list.filter((f) => isImageMime(f.type));
      const others = list.filter((f) => !isImageMime(f.type));
      if (imgs.length > 0) {
        const staged = await Promise.all(imgs.map((f) => stageFile(f)));
        // 追加基于 ref 镜像（await 期间用户可能又加了/删了附件）
        const res = addPendingImages(
          imagesRef.current,
          staged.map((s, i) => s.path
            ? { mime: imgs[i].type, name: imgs[i].name, path: s.path, file: s.file, size: s.file.size }
            : { mime: imgs[i].type, name: imgs[i].name, data: s.data ?? "" }),
        );
        setImages(res.images);
        if (res.notice) flashNotice(res.notice);
      }
      if (others.length > 0) {
        const staged = await Promise.all(others.map((f) => stageFile(f)));
        const res = addPendingFiles(
          filesRef.current,
          staged.map((s, i) => s.path
            ? { name: others[i].name, size: others[i].size, path: s.path, file: s.file }
            : { name: others[i].name, size: others[i].size, data: s.data ?? "" }),
        );
        setFiles(res.files);
        if (res.notice) flashNotice(res.notice);
      }
    } catch (err) {
      flashNotice(`读取文件失败: ${err instanceof Error ? err.message : String(err)}`);
    }
  };

  const pickFiles = (e: React.ChangeEvent<HTMLInputElement>) => {
    void ingest(Array.from(e.target.files ?? []));
    e.target.value = ""; // 允许重复选同一个文件
  };

  // 粘贴：剪贴板里的**图片文件**走附件流程（与 Composer.tsx 的 onPaste 同款——
  // 多张也支持；文本粘贴照常不拦，非图片的剪贴板文件忽略）。
  const onPaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (locked) return;
    const items = e.clipboardData?.items;
    if (!items) return;
    const imgs: File[] = [];
    for (const item of Array.from(items)) {
      if (item.kind !== "file") continue;
      const f = item.getAsFile();
      if (f && isImageMime(f.type)) imgs.push(f);
    }
    if (imgs.length === 0) return;
    e.preventDefault();
    void ingest(imgs);
  };

  // 拖拽（与 Composer.tsx 的 onDragOver/onDragLeave/onDrop 同款）：文件拖到
  // 输入卡直接进附件区。dragover 必须 preventDefault——否则浏览器用默认行为
  // （打开文件）顶掉 drop。文件夹拖入复用 folder-drop 的展开收集（与主域同一
  // 口径：≤16 个 / 总量 ≤20MB、隐藏项与超限跳过、截断给人话提示）。
  const [dragOver, setDragOver] = useState(false);
  const onDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    if (locked) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    setDragOver(true);
  };
  const onDragLeave = (e: React.DragEvent<HTMLDivElement>) => {
    if (e.currentTarget === e.target) setDragOver(false);
  };

  /** 文件夹拖入：展开收集 → 逐个读入（个别失败跳过并说明）→ 进附件区。
   *  与 Composer.tsx 的 ingestFolders 同一条判定（folder-drop 纯函数 + 逐个
   *  stageFile），条目带 group（文件夹名）+ relPath（相对路径）——暂存区渲染
   *  成文件夹卡，发送时 relPath 进 wire 的 rel_path。 */
  const ingestFolders = async (dirs: EntryLike[], plain: File[]) => {
    if (plain.length > 0) await ingest(plain); // 文件夹混着散文件：散文件照旧路径
    const notices: string[] = [];
    const folders: { name: string; items: { file: File; relPath: string }[] }[] = [];
    for (const dir of dirs) {
      const r = await collectFolderEntries(dir, defaultFolderDeps);
      if (r.files.length > 0) {
        folders.push({ name: dir.name, items: r.files.map((file, i) => ({ file, relPath: r.relPaths[i] })) });
      }
      if (r.notice) notices.push(`「${dir.name}」${r.notice}`);
    }
    if (folders.length === 0) {
      if (notices.length > 0) flashNotice(notices.join("；"));
      return;
    }
    // 逐个暂存：路径优先（不读内容），无路径的才读 base64——个别失败跳过并
    // 说明，不整批失败。
    const read: { file: File; path: string | null; data: string | null; folder: string; relPath: string }[] = [];
    let readFailed = 0;
    for (const folder of folders) {
      for (const item of folder.items) {
        try {
          const staged = await stageFile(item.file);
          read.push({ file: item.file, path: staged.path, data: staged.data, folder: folder.name, relPath: item.relPath });
        } catch {
          readFailed++;
        }
      }
    }
    if (readFailed > 0) notices.push(`${readFailed} 个文件读取失败已跳过`);
    const imgs = read.filter((r) => isImageMime(r.file.type) && r.file.size <= MAX_IMAGE_BYTES);
    const oversizeImgs = read.filter((r) => isImageMime(r.file.type) && r.file.size > MAX_IMAGE_BYTES).length;
    if (oversizeImgs > 0) notices.push(`${oversizeImgs} 张图片超过 5MB 上限已跳过`);
    const others = read.filter((r) => !isImageMime(r.file.type));
    const roomImgs = Math.max(0, MAX_IMAGE_COUNT - imagesRef.current.length);
    const roomFiles = Math.max(0, MAX_FILE_COUNT - filesRef.current.length);
    if (imgs.length > roomImgs) notices.push(`一条消息最多 ${MAX_IMAGE_COUNT} 张图片：已添加前 ${roomImgs} 张，其余未加入`);
    if (others.length > roomFiles) notices.push(`一条消息最多 ${MAX_FILE_COUNT} 个文件：已添加前 ${roomFiles} 个，其余未加入`);
    if (imgs.length > 0 && roomImgs > 0) {
      const res = addPendingImages(
        imagesRef.current,
        imgs.slice(0, roomImgs).map((r) => r.path
          ? { mime: r.file.type, name: r.file.name, path: r.path, file: r.file, size: r.file.size }
          : { mime: r.file.type, name: r.file.name, data: r.data ?? "" }),
      );
      setImages(res.images);
      if (res.notice) flashNotice(res.notice);
    }
    if (others.length > 0 && roomFiles > 0) {
      const res = addPendingFiles(
        filesRef.current,
        others.slice(0, roomFiles).map((r) => ({
          name: r.file.name,
          size: r.file.size,
          ...(r.path ? { path: r.path, file: r.file } : { data: r.data ?? "" }),
          group: r.folder,
          relPath: r.relPath,
        })),
      );
      setFiles(res.files);
      if (res.notice) flashNotice(res.notice);
    }
    if (notices.length > 0) flashNotice(notices.join("；"));
  };

  const onDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragOver(false);
    if (locked) return;
    const dt = e.dataTransfer;
    if (!dt) return;
    // webkitGetAsEntry 必须在 drop 事件的同步栈里调用——异步再取会被
    // Chromium 置空（该 API 的经典坑），文件夹就识别不出来了。
    const dirs: EntryLike[] = [];
    const plain: File[] = [];
    for (const item of Array.from(dt.items ?? [])) {
      if (item.kind !== "file") continue;
      const entry = (item.webkitGetAsEntry?.() ?? null) as unknown as EntryLike | null;
      if (entry?.isDirectory) {
        dirs.push(entry);
        continue;
      }
      const f = item.getAsFile();
      if (f) plain.push(f);
    }
    // 拿不到 entry 的来源（虚拟文件/旧引擎）回落 dt.files——单文件行为与原先一致
    const files = dirs.length === 0 && plain.length === 0 ? Array.from(dt.files ?? []) : plain;
    if (dirs.length > 0) void ingestFolders(dirs, files);
    else if (files.length > 0) void ingest(files);
  };

  const removeImage = (id: string) => setImages((cur) => removePending(cur, id));
  const removeFile = (id: string) => setFiles((cur) => removePending(cur, id));

  return (
    <div
      className={"assistant-input" + (dragOver ? " composer-dragover" : "")}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
    >
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
        {/* 发送缓冲区：busy 期间提交的消息排队在这里（等空闲自动发出）——
            与主域 Composer 渲染同一份 SendQueueBar（composer.css 全局类）。 */}
        <SendQueueBar
          queue={queue}
          queueNotice={queueNotice}
          onQueueEdit={onQueueEdit}
          onQueueDelete={onQueueDelete}
          onQueueSend={onQueueSend}
        />
        {(images.length > 0 || files.length > 0 || notice) && (
          <div className="att-preview" role="region" aria-label="附件预览">
            {notice && <div className="att-notice" role="status">{notice}</div>}
            {images.length > 0 && (
              <div className="att-thumbs">
                {images.map((im) => (
                  <AttachmentThumb key={im.id} mime={im.mime} data={im.data} file={im.file} alt={`图片附件 ${im.name || ""}`} title={im.name || undefined} onRemove={() => removeImage(im.id)} />
                ))}
              </div>
            )}
            {files.length > 0 && (
              <FolderFileCards
                files={files}
                onRemoveFolder={(folder) => setFiles((cur) => cur.filter((f) => f.group !== folder))}
                onRemoveFile={(id) => removeFile(id)}
              />
            )}
          </div>
        )}
        <div className="pi-row">
          {/* 「+」添加附件（2026-12 助手附件补齐文件入口）：图片 + 文件两项，
              与主域 Composer 的 .pi-row 附件区同一形态——图片项受 vision 门
              （禁用 + title 说明），文件项不受 vision 门限；文件入主域同款
              上限口径（≤4 个、单个 20MB，无类型白名单）。locked（不可用会话）
              不渲染（与主域 Composer 的 locked 语义一致）。 */}
          {!locked && (
            <span className="pop-wrap att-add-wrap" ref={addMenu.rootRef}>
              <button
                type="button"
                className="att-add"
                title="添加图片或文件"
                aria-label="添加图片或文件"
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
                    disabled={Boolean(visionBlocked)}
                    title={visionBlocked ?? "从本地选择图片（png/jpeg/webp/gif，最多 4 张，单张 5MB 内）"}
                    onClick={() => { imgInputRef.current?.click(); addMenu.requestClose(); }}
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <rect x="3" y="3" width="18" height="18" rx="2" />
                      <circle cx="8.5" cy="8.5" r="1.5" />
                      <path d="m21 15-5-5L5 21" />
                    </svg>
                    图片
                  </button>
                  <button
                    type="button"
                    className="att-menu-item"
                    role="menuitem"
                    title="从本地选择文件（最多 4 个，单个 20MB 内；交给智能体读取）"
                    onClick={() => { fileInputRef.current?.click(); addMenu.requestClose(); }}
                  >
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                      <path d="M14 2v6h6" />
                    </svg>
                    文件
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
              <input
                ref={fileInputRef}
                type="file"
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
            onPaste={onPaste}
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
